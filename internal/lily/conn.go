package lily

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joshw/zephyrlily/internal/slcp"
)

// ConnPhase tracks where in the login/handshake sequence we are.
type ConnPhase int

const (
	PhaseFirstPrompt ConnPhase = iota // waiting for any prompt to send options + login
	PhaseSync                         // inside %SLCP-SYNC block
	PhaseReady                        // fully connected and synced
)

// Conn is a single user's persistent connection to the Lily server.
type Conn struct {
	addr     string
	username string
	password string

	conn   net.Conn
	reader *bufio.Reader
	wmu    sync.Mutex // guards writes to conn

	phase ConnPhase
	state *State

	// nextCmdID is incremented for each command we originate (if needed).
	nextCmdID atomic.Int64

	// Events is the channel on which parsed messages are delivered to the
	// proxy layer. The proxy reads from this and fans out to WebSocket clients.
	Events chan *slcp.Message

	// ctx controls the read loop lifetime.
	ctx    context.Context
	cancel context.CancelFunc
}

// NewConn creates a Conn but does not connect yet.
func NewConn(addr, username, password string) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{
		addr:     addr,
		username: username,
		password: password,
		state:    NewState(),
		Events:   make(chan *slcp.Message, 256),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Connect dials the Lily server and runs the handshake, blocking until
// %connected is received or an error occurs.
func (c *Conn) Connect() error {
	log.Printf("lily: dialing %s", c.addr)
	nc, err := net.Dial("tcp", c.addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.addr, err)
	}
	log.Printf("lily: connected to %s", c.addr)
	c.conn = nc
	c.reader = bufio.NewReader(nc)
	c.phase = PhaseFirstPrompt

	if err := c.runHandshake(); err != nil {
		nc.Close()
		return err
	}

	log.Printf("lily: handshake complete")
	go c.readLoop()
	return nil
}

// Close shuts down the connection.
func (c *Conn) Close() {
	c.cancel()
	if c.conn != nil {
		c.conn.Close()
	}
}

// State returns the live state for this connection.
func (c *Conn) State() *State {
	return c.state
}

// Send writes a raw command line to the server.
func (c *Conn) Send(line string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	log.Printf("lily: send: %q", line)
	_, err := fmt.Fprintf(c.conn, "%s\n", line)
	return err
}

// sendRaw writes bytes directly without adding a newline.
func (c *Conn) sendRaw(s string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	log.Printf("lily: send raw: %q", s)
	_, err := fmt.Fprint(c.conn, s)
	return err
}

// runHandshake drives the login/options/sync sequence synchronously.
// Returns once %connected is received.
func (c *Conn) runHandshake() error {
	for {
		line, err := c.readLine()
		if err != nil {
			return fmt.Errorf("handshake read: %w", err)
		}
		log.Printf("lily: [%v] recv: %q", c.phase, line)

		switch c.phase {
		case PhaseFirstPrompt:
			// Parse welcome banner to extract server name
			if strings.HasPrefix(line, "Welcome to lily at ") {
				serverName := strings.TrimSpace(line[len("Welcome to lily at "):])
				c.state.Name = serverName
				log.Printf("lily: detected server name: %s", serverName)
			}

			// Wait for the actual "login:" prompt before sending options.
			if strings.HasPrefix(line, "login:") || line == "login:" {
				log.Printf("lily: got login prompt, sending options and credentials")
				if err := c.Send("#$# options +version +prompt +prompt2 +leaf-notify +leaf-cmd +connected"); err != nil {
					return err
				}
				if err := c.Send(c.username + " " + c.password); err != nil {
					return err
				}
				c.phase = PhaseSync
			}
			// Ignore other lines (blank lines, etc.)

		case PhaseSync:
			msg, err := slcp.Parse(line)
			if err != nil {
				log.Printf("slcp parse during sync: %v", err)
				continue
			}

			// Validate server options
			if msg.Type == slcp.MsgOptions {
				if err := c.validateOptions(msg.Text); err != nil {
					return err
				}
			}

			// Handle prompts during sync (blurb, etc.) - send blank line
			if msg.Type == slcp.MsgPrompt {
				log.Printf("lily: got prompt during sync, sending blank line")
				if err := c.Send(""); err != nil {
					return err
				}
				continue
			}

			if err := c.applySync(msg); err != nil {
				log.Printf("sync apply: %v", err)
			}

			// Wait for %connected to indicate login/sync is complete
			if msg.Type == slcp.MsgConnected {
				log.Printf("lily: got %%connected, sending client name")
				// After %connected, send client name
				if err := c.Send("#$# client zlily 0.1.0"); err != nil {
					return err
				}
				c.phase = PhaseReady
				return nil
			}
		}
	}
}

// validateOptions checks that the server supports all required options.
func (c *Conn) validateOptions(optionsText string) error {
	required := []string{"+prompt", "+prompt2", "+leaf-notify", "+leaf-cmd", "+connected"}

	// Parse the options string into a set
	serverOpts := make(map[string]bool)
	for _, opt := range strings.Fields(optionsText) {
		serverOpts[opt] = true
	}

	// Check for missing required options
	var missing []string
	for _, req := range required {
		if !serverOpts[req] {
			missing = append(missing, req)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("server does not support required options: %s", strings.Join(missing, " "))
	}

	log.Printf("lily: server supports all required options")
	return nil
}

// readLoop runs after the handshake and delivers messages on Events.
func (c *Conn) readLoop() {
	defer close(c.Events)
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		line, err := c.readLine()
		if err != nil {
			log.Printf("lily read error: %v", err)
			return
		}

		msg, err := slcp.Parse(line)
		if err != nil {
			log.Printf("slcp parse: %v", err)
			continue
		}

		// Keep state up to date
		c.applyLive(msg)

		select {
		case c.Events <- msg:
		case <-c.ctx.Done():
			return
		}
	}
}

// readLine reads one line from the server, trimming CR/LF.
// It handles prompts that lack trailing newlines by using a timeout
// and checking for known prompt patterns.
func (c *Conn) readLine() (string, error) {
	var buf []byte
	for {
		c.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		b, err := c.reader.ReadByte()
		c.conn.SetReadDeadline(time.Time{})

		if err == nil {
			if b == '\n' {
				// Got a complete line
				return strings.TrimRight(string(buf), "\r"), nil
			}
			buf = append(buf, b)
			continue
		}

		// Check for timeout
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// No newline arrived; check if what we have is a prompt
			s := strings.TrimRight(string(buf), " \r")
			if strings.HasPrefix(s, "login:") || s == "login:" {
				return s, nil
			}
			if strings.HasPrefix(s, "password:") || s == "password:" {
				return s, nil
			}
			// Keep waiting for more data
			continue
		}

		// Real error
		return "", err
	}
}

// applySync updates state from messages received during the sync phase.
func (c *Conn) applySync(msg *slcp.Message) error {
	return c.applyMsg(msg)
}

// applyLive updates state from messages received after handshake.
func (c *Conn) applyLive(msg *slcp.Message) {
	if err := c.applyMsg(msg); err != nil {
		log.Printf("state apply: %v", err)
	}
}

func (c *Conn) applyMsg(msg *slcp.Message) error {
	switch msg.Type {
	case slcp.MsgUser:
		u, err := slcp.ParseUser(msg)
		if err != nil {
			return err
		}
		c.state.ApplyUser(u)

	case slcp.MsgDisc:
		d, err := slcp.ParseDisc(msg)
		if err != nil {
			return err
		}
		c.state.ApplyDisc(d)

	case slcp.MsgGroup:
		g, err := slcp.ParseGroup(msg)
		if err != nil {
			return err
		}
		c.state.ApplyGroup(g)

	case slcp.MsgData:
		c.state.SetData(msg.Params["NAME"], msg.Params["VALUE"])

	case slcp.MsgServer:
		// Extract server metadata from %server message
		if version, ok := msg.Params["version"]; ok {
			c.state.Version = version
		}
		if name, ok := msg.Params["name"]; ok {
			c.state.Name = name
		}

	case slcp.MsgNotify:
		ev, err := slcp.ParseNotify(msg)
		if err != nil {
			return err
		}
		c.state.ApplyNotify(ev)
	}
	return nil
}
