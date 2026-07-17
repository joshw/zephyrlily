package lily

import (
	"bufio"
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joshw/zephyrlily/internal/version"

	"github.com/joshw/zephyrlily/internal/slcp"
)

// wireLogEnv names an environment variable that, when set to a file path, makes
// every Conn append a line-oriented transcript of its socket I/O (one entry per
// line/prompt read and per line sent, with timestamps and a per-connection
// header). Used to compare the login and reconnect handshakes.
const wireLogEnv = "ZLILY_WIRELOG"

// ErrAuthFailed indicates the Lily server rejected the supplied credentials
// (it re-prompted for login instead of confirming with %options).
var ErrAuthFailed = errors.New("invalid username or password")

// handshakeTimeout bounds how long the dial + login sequence may take before we
// give up. It guards against a server/network that never completes the login
// (e.g. an unreachable server, or a reconnect where the server is slow to reap
// the previous session). It is deliberately generous so a legitimately slow
// reconnect still succeeds rather than being cut off.
const handshakeTimeout = 60 * time.Second

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

	// loginResult reports the outcome of the login from readLoop back to
	// Connect: nil once %connected confirms a successful login, or an error
	// (e.g. ErrAuthFailed) if the server rejected the credentials. It is
	// buffered and written at most once.
	loginResult chan error

	// Events is the channel on which parsed messages are delivered to the
	// proxy layer. The proxy reads from this and fans out to WebSocket clients.
	Events chan *slcp.Message

	// ctx controls the read loop lifetime.
	ctx    context.Context
	cancel context.CancelFunc

	// wire, when non-nil, receives a transcript of socket I/O (see wireLogEnv).
	wire   *os.File
	wireMu sync.Mutex
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
		loginResult:  make(chan error, 1),
	}
}

// SyncComplete returns a channel that is closed when the initial SLCP sync
// finishes (%connected is received and state is fully populated).
func (c *Conn) SyncComplete() <-chan struct{} {
	return c.syncComplete
}

// describeCertError enriches a TLS handshake failure with concrete details
// about the certificate the server offered. It exists because of how Go reports
// verification failures on macOS: chain validation is delegated to Apple's
// Security framework, which returns opaque strings like "certificate is not
// standards compliant" without naming the rule that was broken. Go surfaces
// that string verbatim, so the bare error tells the operator nothing
// actionable.
//
// The verification error still carries the offered (unverified) chain, so we
// decode the leaf ourselves and point at the fields Apple most commonly
// rejects: an over-long validity period, a missing Subject Alternative Name, a
// weak signature algorithm, or an undersized RSA key. If none of those match we
// at least print the subject/issuer/validity so the cert can be identified.
func describeCertError(err error) error {
	var ve *tls.CertificateVerificationError
	if !errors.As(err, &ve) || len(ve.UnverifiedCertificates) == 0 {
		return err
	}
	leaf := ve.UnverifiedCertificates[0]

	var notes []string
	if days := leaf.NotAfter.Sub(leaf.NotBefore).Hours() / 24; days > 398 {
		notes = append(notes, fmt.Sprintf("validity is %.0f days (Apple rejects TLS certs valid >398 days)", days))
	}
	if len(leaf.DNSNames) == 0 && len(leaf.IPAddresses) == 0 {
		notes = append(notes, fmt.Sprintf("no Subject Alternative Name (CN=%q alone is no longer accepted)", leaf.Subject.CommonName))
	}
	switch leaf.SignatureAlgorithm {
	case x509.MD5WithRSA, x509.SHA1WithRSA, x509.DSAWithSHA1, x509.ECDSAWithSHA1:
		notes = append(notes, "weak signature algorithm "+leaf.SignatureAlgorithm.String())
	}
	if pk, ok := leaf.PublicKey.(*rsa.PublicKey); ok && pk.N.BitLen() < 2048 {
		notes = append(notes, fmt.Sprintf("RSA key is %d bits (<2048)", pk.N.BitLen()))
	}

	detail := fmt.Sprintf("offered cert subject=%q issuer=%q notBefore=%s notAfter=%s SANs=%v",
		leaf.Subject, leaf.Issuer,
		leaf.NotBefore.Format(time.RFC3339), leaf.NotAfter.Format(time.RFC3339),
		leaf.DNSNames)
	if len(notes) > 0 {
		detail += "; likely cause: " + strings.Join(notes, "; ")
	}
	return fmt.Errorf("%w (%s)", err, detail)
}

// Connect dials the Lily server and runs the handshake, blocking until login is
// confirmed or an error occurs. The whole operation is bounded by
// handshakeTimeout so a stalled or unreachable server (e.g. during a reconnect)
// cannot hang the caller indefinitely.
func (c *Conn) Connect() error {
	slog.Debug("lily: dialing", "addr", c.addr, "tls", c.tls)
	dialer := &net.Dialer{Timeout: handshakeTimeout}
	var nc net.Conn
	var err error
	if c.tls {
		host, _, _ := net.SplitHostPort(c.addr)
		nc, err = tls.DialWithDialer(dialer, "tcp", c.addr, &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: c.tlsInsecure, //nolint:gosec
		})
	} else {
		nc, err = dialer.Dial("tcp", c.addr)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.addr, describeCertError(err))
	}
	slog.Debug("lily: connected", "addr", c.addr)
	c.conn = nc
	c.reader = bufio.NewReader(nc)
	c.phase = PhaseFirstPrompt
	c.openWireLog()

	// Bound the whole login. readLine blocks until it gets a complete line or a
	// recognized prompt and only returns early on a socket error, so the only
	// reliable way to time out a stalled handshake is to close the socket — that
	// makes the in-flight read fail. The watchdog does that; loginDone disarms it
	// once login resolves so the long-lived readLoop is not affected.
	var timedOut atomic.Bool
	loginDone := make(chan struct{})
	go func() {
		select {
		case <-loginDone:
		case <-time.After(handshakeTimeout):
			slog.Debug("lily: login timed out, closing connection")
			timedOut.Store(true)
			_ = nc.Close()
		}
	}()
	defer close(loginDone)

	if err := c.runHandshake(); err != nil {
		_ = nc.Close()
		if timedOut.Load() {
			return fmt.Errorf("login timed out")
		}
		return err
	}

	// runHandshake only gets us through options negotiation (%options). Whether
	// the credentials were actually accepted is not known until the server
	// either prints "*** Connected ***" / %connected (success) or re-prompts for
	// login (failure). readLoop reads those lines and reports the outcome on
	// loginResult.
	slog.Debug("lily: options negotiated, starting readLoop")
	go c.readLoop()

	if err := <-c.loginResult; err != nil {
		c.Close()
		if timedOut.Load() {
			return fmt.Errorf("login timed out")
		}
		return err
	}
	slog.Debug("lily: login confirmed")
	return nil
}

// Close shuts down the connection.
func (c *Conn) Close() {
	c.cancel()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.wireMu.Lock()
	if c.wire != nil {
		c.logWireLocked("==", "close")
		_ = c.wire.Close()
		c.wire = nil
	}
	c.wireMu.Unlock()
}

// openWireLog opens the socket transcript file named by ZLILY_WIRELOG (if set)
// and writes a header so the login and reconnect handshakes can be told apart in
// a shared file. Failures are non-fatal — capture is a best-effort debug aid.
func (c *Conn) openWireLog() {
	path := os.Getenv(wireLogEnv)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Debug("lily: wire log open failed", "path", path, "err", err)
		return
	}
	c.wireMu.Lock()
	c.wire = f
	c.logWireLocked("==", fmt.Sprintf("connect user=%s addr=%s tls=%v", c.username, c.addr, c.tls))
	c.wireMu.Unlock()
}

// logWire appends one directional transcript entry. dir is "<<" (received from
// server), ">>" (sent to server), or "==" (annotation).
func (c *Conn) logWire(dir, line string) {
	c.wireMu.Lock()
	c.logWireLocked(dir, line)
	c.wireMu.Unlock()
}

// logWireLocked is logWire with c.wireMu already held.
func (c *Conn) logWireLocked(dir, line string) {
	if c.wire == nil {
		return
	}
	// Never write the plaintext password (it appears in the credentials line).
	if c.password != "" {
		line = strings.ReplaceAll(line, c.password, "***")
	}
	// Best-effort wire log; ignore write errors.
	_, _ = fmt.Fprintf(c.wire, "%s %s %q\n", time.Now().Format("15:04:05.000"), dir, line)
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
	c.logWire(">>", line)
	_, err := fmt.Fprintf(c.conn, "%s\n", line)
	return err
}

// sendRaw writes bytes directly without adding a newline.

// runHandshake drives the login/options/sync sequence synchronously.
// Returns once %connected is received.
func (c *Conn) runHandshake() error {
	deadline := time.Now().Add(handshakeTimeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("login timed out")
		}
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
				// %options is only the server's reply to our "#$# options"
				// negotiation. It is sent regardless of whether the credentials
				// were valid, so it does NOT confirm login. Validate the advertised
				// options and hand off to readLoop, which watches for the real
				// outcome: %connected (success) or a login re-prompt (failure).
				if err := c.validateOptions(msg.Text); err != nil {
					return err
				}
				slog.Debug("lily: options negotiated")
				c.phase = PhaseReady
				return nil

			case slcp.MsgLoginPrompt, slcp.MsgPassPrompt:
				// The server re-prompted before even acknowledging our options,
				// which means the credentials were rejected outright. (Normally
				// %options arrives first; this is the defensive early-reject path.)
				slog.Debug("lily: credentials rejected before options")
				return ErrAuthFailed

			default:
				// Other pre-options lines (blank lines, banner text); skip.
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

	// loginSignalled guards loginResult so we report the login outcome to
	// Connect exactly once. Until %connected confirms success, a login/password
	// re-prompt (or the loop exiting) means the credentials were rejected.
	loginSignalled := false
	signalLogin := func(err error) {
		if !loginSignalled {
			loginSignalled = true
			c.loginResult <- err
		}
	}
	defer func() {
		// If we exit before login was confirmed, the connection dropped during
		// login; unblock Connect with an error rather than letting it wait.
		signalLogin(fmt.Errorf("connection closed during login"))
	}()

	// inSync is true while we are processing the initial %SLCP-SYNC block.
	// Messages are applied with fromSync=true so disc membership is seeded.
	inSync := true

	// waitingForWhere becomes true after we send /where me, and reverts to false
	// once the response is fully received and processed.
	var whereCmdID int
	waitingForWhere := false

	// startWhere sends the client name and "/where me" to seed disc membership,
	// then arranges for syncComplete to close once the response arrives. It runs
	// at most once, triggered by %connected (which marks the end of the login
	// sequence and entity sync).
	whereStarted := false
	startWhere := func() {
		if whereStarted {
			return
		}
		whereStarted = true
		if err := c.Send(fmt.Sprintf("#$# client zlily %s", version.String())); err != nil {
			slog.Debug("lily: client name send error", "err", err)
		}
		if err := c.Send("/where me"); err != nil {
			slog.Debug("lily: where me send error", "err", err)
			// If the send fails, unblock callers waiting on full state anyway.
			close(c.syncComplete)
		} else {
			waitingForWhere = true
		}
	}

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

		// Before login is confirmed, decide the outcome:
		//   - "*** Connected ***" is printed as soon as the credentials are
		//     accepted — well before the SLCP sync block and %connected — so we
		//     treat it as the success signal and let Connect return promptly.
		//   - "*** Redirecting old connection to this port ***" replaces that
		//     banner when the user was already logged in from another client and
		//     this login takes over the session. It is equally proof the
		//     credentials were accepted; without it we would sit in the auth
		//     dialog while the server waits at the blurb prompt, and time out.
		//   - A fresh login/password prompt means the server rejected the
		//     credentials ("Login in the wrong."); report the failure and stop so
		//     Connect surfaces ErrAuthFailed and the user can retry.
		if !loginSignalled {
			switch {
			case msg.Type == slcp.MsgRaw && strings.Contains(msg.Text, "*** Connected ***"):
				slog.Debug("lily: login confirmed (*** Connected ***)")
				signalLogin(nil)
			case msg.Type == slcp.MsgRaw && strings.Contains(msg.Text, "*** Redirecting old connection"):
				slog.Debug("lily: login confirmed (session redirect)")
				signalLogin(nil)
			case msg.Type == slcp.MsgLoginPrompt || msg.Type == slcp.MsgPassPrompt:
				slog.Debug("lily: credentials rejected, server re-prompted")
				signalLogin(ErrAuthFailed)
				return
			}
		}

		// Apply to state (sync mode until %connected so disc membership is set).
		if inSync {
			_ = c.applySync(msg)
		} else {
			c.applyLive(msg)
		}

		// %connected marks login fully complete (after any interactive login
		// prompts such as "enter a blurb" and the entity sync).
		if msg.Type == slcp.MsgConnected {
			// Fallback login confirmation: normally "*** Connected ***" already
			// signalled success earlier, but if a server omits that banner,
			// %connected still confirms it (guarded, so this is a no-op when
			// already signalled).
			signalLogin(nil)
			inSync = false
			slog.Debug("lily: connected")
			startWhere()
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
				line := strings.TrimRight(string(buf), "\r")
				c.logWire("<<", line)
				return line, nil
			}
			buf = append(buf, b)
			continue
		}

		// Check for timeout
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// No newline arrived; check if what we have is a prompt
			s := strings.TrimRight(string(buf), " \r")
			if strings.HasPrefix(s, "login:") || s == "login:" {
				c.logWire("<<", s)
				return s, nil
			}
			if strings.HasPrefix(s, "password:") || s == "password:" {
				c.logWire("<<", s)
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
