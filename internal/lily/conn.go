package lily

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"

	"strings"
	"sync"
	"time"

	"github.com/joshw/zephyrlily/internal/version"

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
	addr        string
	username    string
	password    string
	tls         bool
	tlsInsecure bool

	conn   net.Conn
	reader *bufio.Reader
	wmu    sync.Mutex // guards writes to conn

	phase ConnPhase
	state *State

	// syncComplete is closed when the initial %SLCP-SYNC completes (%connected
	// received).  Callers that need fully-populated state (e.g. /state) block
	// on this channel before reading entity data.
	syncComplete chan struct{}

	// Events is the channel on which parsed messages are delivered to the
	// proxy layer. The proxy reads from this and fans out to WebSocket clients.
	Events chan *slcp.Message

	// ctx controls the read loop lifetime.
	ctx    context.Context
	cancel context.CancelFunc
}

// NewConn creates a Conn but does not connect yet.
func NewConn(addr, username, password string, tlsEnabled, tlsInsecure bool) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{
		addr:         addr,
		username:     username,
		password:     password,
		tls:          tlsEnabled,
		tlsInsecure:  tlsInsecure,
		state:        NewState(),
		Events:       make(chan *slcp.Message, 256),
		ctx:          ctx,
		cancel:       cancel,
		syncComplete: make(chan struct{}),
	}
}

// SyncComplete returns a channel that is closed when the initial SLCP sync
// finishes (%connected is received and state is fully populated).
func (c *Conn) SyncComplete() <-chan struct{} {
	return c.syncComplete
}

// Connect dials the Lily server and runs the handshake, blocking until
// %connected is received or an error occurs.
func (c *Conn) Connect() error {
	slog.Debug("lily: dialing", "addr", c.addr, "tls", c.tls)
	var nc net.Conn
	var err error
	if c.tls {
		host, _, _ := net.SplitHostPort(c.addr)
		nc, err = tls.Dial("tcp", c.addr, &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: c.tlsInsecure, //nolint:gosec
		})
	} else {
		nc, err = net.Dial("tcp", c.addr)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.addr, err)
	}
	slog.Debug("lily: connected", "addr", c.addr)
	c.conn = nc
	c.reader = bufio.NewReader(nc)
	c.phase = PhaseFirstPrompt

	if err := c.runHandshake(); err != nil {
		_ = nc.Close()
		return err
	}

	slog.Debug("lily: login confirmed, starting readLoop")
	go c.readLoop()
	return nil
}

// Close shuts down the connection.
func (c *Conn) Close() {
	c.cancel()
	if c.conn != nil {
		_ = c.conn.Close()
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
	slog.Debug("lily: send", "line", line)
	_, err := fmt.Fprintf(c.conn, "%s\n", line)
	return err
}

// sendRaw writes bytes directly without adding a newline.

// runHandshake drives the login/options/sync sequence synchronously.
// Returns once %connected is received.
func (c *Conn) runHandshake() error {
	for {
		line, err := c.readLine()
		if err != nil {
			return fmt.Errorf("handshake read: %w", err)
		}
		slog.Debug("lily: recv", "phase", c.phase, "line", line)

		switch c.phase {
		case PhaseFirstPrompt:
			// Parse welcome banner to extract server name
			if strings.HasPrefix(line, "Welcome to lily at ") {
				serverName := strings.TrimSpace(line[len("Welcome to lily at "):])
				c.state.Name = serverName
				slog.Debug("lily: server name", "name", serverName)
			}

			// Wait for the actual "login:" prompt before sending options.
			if strings.HasPrefix(line, "login:") || line == "login:" {
				slog.Debug("lily: sending options and credentials")
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
				slog.Debug("slcp parse error (pre-sync)", "err", err)
				continue
			}

			switch msg.Type {
			case slcp.MsgServer:
				// %server carries version/name metadata; apply it and keep reading.
				if err := c.applySync(msg); err != nil {
					slog.Debug("sync apply error", "err", err)
				}

			case slcp.MsgOptions:
				// %options confirms the server accepted our credentials.
				// Return immediately so readLoop can handle everything that
				// follows (raw text, interactive prompts, entity data,
				// %connected) and forward it to the TUI for the user to see
				// and respond to.
				if err := c.validateOptions(msg.Text); err != nil {
					return err
				}
				slog.Debug("lily: login confirmed")
				c.phase = PhaseReady
				return nil

			default:
				// Unexpected message before %options (rare); skip it.
				slog.Debug("lily: pre-options message", "type", msg.Type, "text", msg.Text)
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

	slog.Debug("lily: options validated")
	return nil
}

// readLoop runs after the initial login handshake and delivers messages on
// Events.  It handles the full SLCP sync (entity data, prompts, %connected)
// and silently intercepts the /where me command response used to seed disc
// membership.
func (c *Conn) readLoop() {
	defer close(c.Events)

	// inSync is true while we are processing the initial %SLCP-SYNC block.
	// Messages are applied with fromSync=true so disc membership is seeded.
	inSync := true

	// waitingForWhere becomes true after we send /where me on %connected, and
	// reverts to false once the response is fully received and processed.
	var whereCmdID int
	waitingForWhere := false

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		line, err := c.readLine()
		if err != nil {
			slog.Debug("lily read error", "err", err)
			return
		}

		msg, err := slcp.Parse(line)
		if err != nil {
			slog.Debug("slcp parse error", "err", err)
			continue
		}

		// Apply to state (sync mode until %connected so disc membership is set).
		if inSync {
			_ = c.applySync(msg)
		} else {
			c.applyLive(msg)
		}

		// %connected marks the end of the initial sync.
		if msg.Type == slcp.MsgConnected {
			inSync = false
			slog.Debug("lily: sync complete")
			if err := c.Send(fmt.Sprintf("#$# client zlily %s", version.Version)); err != nil {
				slog.Debug("lily: client name send error", "err", err)
			}
			// Ask for disc membership before signalling sync complete so that
			// handleState sees the populated discMembership map.
			// syncComplete is closed once the /where me response arrives.
			if err := c.Send("/where me"); err != nil {
				slog.Debug("lily: where me send error", "err", err)
				// If the send fails, unblock callers anyway.
				close(c.syncComplete)
			} else {
				waitingForWhere = true
			}
			continue // %connected itself is not forwarded to clients
		}

		// Intercept the /where me response before forwarding to clients.
		suppress := false
		if waitingForWhere {
			switch msg.Type {
			case slcp.MsgCmdBegin:
				slog.Debug("lily: where-wait begin", "cmdID", msg.CmdID, "text", msg.Text)
				if strings.Contains(msg.Text, "/where") {
					whereCmdID = msg.CmdID
					suppress = true
				}
			case slcp.MsgRaw:
				slog.Debug("lily: where-wait raw", "cmdID", whereCmdID, "text", msg.Text)
				if whereCmdID > 0 {
					c.state.ApplyWhereResponse([]string{msg.Text})
					suppress = true
				}
			case slcp.MsgCmdEnd:
				slog.Debug("lily: where-wait end", "cmdID", msg.CmdID, "whereCmdID", whereCmdID)
				if whereCmdID > 0 && msg.CmdID == whereCmdID {
					whereCmdID = 0
					waitingForWhere = false
					suppress = true
					// Membership is now populated; unblock handleState.
					close(c.syncComplete)
				}
			}
		}

		if suppress {
			continue
		}

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
		_ = c.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		b, err := c.reader.ReadByte()
		_ = c.conn.SetReadDeadline(time.Time{})

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
		slog.Debug("state apply error", "err", err)
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
