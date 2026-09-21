// Package client connects the TUI to the zlily-proxy over HTTP and WebSocket.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/joshw/zephyrlily/internal/proxy/api"
)

// ErrAuthFailed indicates the proxy/Lily server rejected the supplied
// credentials (as opposed to a network or connection error). Callers use this
// to decide whether to re-prompt for credentials.
var ErrAuthFailed = errors.New("invalid username or password")

// ErrSessionGone indicates the proxy no longer has the session a client holds
// a token for: it ended (the Lily connection closed), or the proxy restarted
// and never heard of it. Like ErrAuthFailed it means "ask the user to log in",
// but it is a separate answer because the cause is different and the wording
// the TUI shows should be too — "invalid username or password" for a session
// that simply ended reads as an accusation about a password the user never
// typed.
var ErrSessionGone = errors.New("session ended")

// clientGen numbers Clients in creation order. A reconnect abandons one Client
// for another, and the abandoned one's read loop and Events channel outlive it
// for a while; the generation is what lets a message be traced back to the
// socket it came off, rather than to whichever Client happens to be current
// when it is read.
var clientGen atomic.Int64

// Client is a connection from the TUI to the proxy.
type Client struct {
	proxyAddr string // e.g. "localhost:7888"
	secure    bool   // talk https/wss rather than http/ws
	token     string
	username  string // stored for reconnection
	password  string // stored for reconnection
	ws        *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc

	// Events is the channel of messages received from the proxy.
	Events chan *api.WSServerMsg

	// lastReportedSeenID is the most recent value successfully sent to /seen.
	// ReportSeen skips the HTTP call when the value hasn't changed.
	lastReportedSeenID atomic.Int64

	// closeOnce ensures the Events channel is only closed once.
	closeOnce sync.Once

	// closed tracks whether this client has been closed to prevent operations on old clients.
	closed atomic.Bool

	// gen identifies this Client among the ones a session has been through.
	gen int64

	// connectedAt is when Connect last opened this client's WebSocket, and
	// closeReason is why the read loop then stopped ("" while it is running).
	// Neither changes what the client does: they are here so that a dropped
	// socket leaves behind something to read afterwards, which is exactly what
	// the first round of "it keeps reconnecting" reports had none of.
	connectedAt atomic.Int64 // UnixNano; 0 before the first Connect
	closeReason atomic.Value // string
}

// New creates a Client pointing at the given proxy address over plain HTTP.
func New(proxyAddr string) *Client { return newClient(proxyAddr, false) }

// NewSecure is New over TLS. The browser build needs it: a page served over
// https cannot open http or ws connections back to its own origin, and the
// proxy serves its API and the web assets from the same listener (--web-tls).
func NewSecure(proxyAddr string) *Client { return newClient(proxyAddr, true) }

func newClient(proxyAddr string, secure bool) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		proxyAddr: proxyAddr,
		secure:    secure,
		ctx:       ctx,
		cancel:    cancel,
		Events:    make(chan *api.WSServerMsg, 256),
		gen:       clientGen.Add(1),
	}
}

// httpURL builds an absolute proxy URL for path, which must start with "/".
func (c *Client) httpURL(path string) string {
	if c.secure {
		return "https://" + c.proxyAddr + path
	}
	return "http://" + c.proxyAddr + path
}

// wsURL is httpURL for the WebSocket schemes.
func (c *Client) wsURL(path string) string {
	if c.secure {
		return "wss://" + c.proxyAddr + path
	}
	return "ws://" + c.proxyAddr + path
}

// ResumeSession adopts a token obtained earlier — from browser storage, say —
// and confirms with the proxy that it still names a live session, returning the
// username it belongs to.
//
// A resumed client knows no password, so it cannot Reconnect() on its own: if
// the session later dies, the caller has to ask for credentials again. That is
// the whole trade, and it is why this checks the token up front rather than
// letting the first real request fail somewhere less recoverable.
func (c *Client) ResumeSession(token string) (string, error) {
	if token == "" {
		return "", errors.New("no token")
	}
	c.token = token

	req, err := http.NewRequest(http.MethodGet, c.httpURL("/session"), nil)
	if err != nil {
		c.token = ""
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The proxy could not be reached — which says nothing about the
		// session, and a page loading a second before the network is ready
		// hits this. Drop the token from this client so HasToken() is honest
		// and the login dialog appears, but report it as what it is: the
		// caller discards the token it has stored on ErrAuthFailed, and doing
		// that here would throw away a live session over a bad second.
		c.token = ""
		return "", fmt.Errorf("session check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// The token is stale, or this proxy has never heard of it. Drop it so
		// HasToken() is honest and the caller falls back to logging in.
		c.token = ""
		if resp.StatusCode == http.StatusUnauthorized {
			return "", fmt.Errorf("%w: the proxy does not know this session", ErrAuthFailed)
		}
		return "", fmt.Errorf("session check: HTTP %s", resp.Status)
	}
	var sr api.SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		c.token = ""
		return "", fmt.Errorf("session check: %w", err)
	}
	// A session is not usable until its WebSocket is open: the TUI reads events
	// from it and Send writes commands to it. Auth's caller pairs Auth with
	// Connect for exactly this reason (see attemptAuthCmd), and resuming has to
	// do the same or it yields a client that looks authenticated, reads no
	// events, and panics on the first thing typed.
	if err := c.Connect(); err != nil {
		c.token = ""
		return "", fmt.Errorf("resume connect: %w", err)
	}

	c.username = sr.Username
	return sr.Username, nil
}

// HasToken returns true if the client has been authenticated and has a token.
func (c *Client) HasToken() bool {
	return c.token != ""
}

// Token returns the proxy session token from the last successful Auth (empty
// before authenticating). It identifies the proxy-side session, which callers
// need because message IDs are only meaningful within one session: a token
// change means a fresh session whose ID counter restarted.
func (c *Client) Token() string {
	return c.token
}

// Info asks the proxy what it is connected to. It needs no token (the caller
// has none yet) and is the first call the TUI makes: saved credentials are
// keyed by Lily server, which only the proxy knows.
func (c *Client) Info() (*api.InfoResponse, error) {
	resp, err := http.Get(c.httpURL("/info"))
	if err != nil {
		return nil, fmt.Errorf("info request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("info: HTTP %s", resp.Status)
	}
	var ir api.InfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&ir); err != nil {
		return nil, fmt.Errorf("info decode: %w", err)
	}
	return &ir, nil
}

// Auth authenticates against the proxy and stores the session token.
func (c *Client) Auth(username, password string) error {
	body, _ := json.Marshal(api.AuthRequest{Username: username, Password: password})
	resp, err := http.Post(c.httpURL("/auth"), "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("auth request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Read the error body to get the detailed error message
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(resp.Body)
		errMsg := strings.TrimSpace(errBody.String())
		if errMsg == "" {
			errMsg = resp.Status
		}
		// Distinguish a credential rejection from other failures so callers can
		// decide whether to re-prompt for credentials.
		if strings.Contains(errMsg, ErrAuthFailed.Error()) {
			return fmt.Errorf("%w", ErrAuthFailed)
		}
		return fmt.Errorf("auth failed: %s", errMsg)
	}
	var ar api.AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return fmt.Errorf("auth decode: %w", err)
	}
	c.token = ar.Token
	c.username = username
	c.password = password
	return nil
}

// FetchState retrieves the current state snapshot from the proxy.
func (c *Client) FetchState() (*api.StateResponse, error) {
	req, _ := http.NewRequest(http.MethodGet, c.httpURL("/state"), nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("state request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var sr api.StateResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("state decode: %w", err)
	}
	return &sr, nil
}

// FetchEvents retrieves buffered events from the proxy after the given ID.
func (c *Client) FetchEvents(afterID int64, limit int) ([]api.WSServerMsg, bool, error) {
	url := c.httpURL(fmt.Sprintf("/events?after=%d&limit=%d", afterID, limit))
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("events request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("events: HTTP %s", resp.Status)
	}
	var er api.EventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, false, fmt.Errorf("events decode: %w", err)
	}
	return er.Events, er.More, nil
}

// ReportSeen tells the proxy the last message ID the user has seen.
// It skips the HTTP call when the value hasn't changed since the last report.
func (c *Client) ReportSeen(lastSeenID int64) error {
	if c.lastReportedSeenID.Load() == lastSeenID {
		return nil
	}
	body, _ := json.Marshal(api.SeenRequest{LastSeenID: lastSeenID})
	req, _ := http.NewRequest(http.MethodPost, c.httpURL("/seen"), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("seen request: %w", err)
	}
	_ = resp.Body.Close()
	c.lastReportedSeenID.Store(lastSeenID)
	return nil
}

// Expand searches the proxy entity state for names matching partial.
// The proxy returns exact matches first; if none, prefix matches.
// Callers apply the "unique match wins" rule to the result.
// When validDestOnly is true the proxy excludes discussions the current user
// is not a member of.
func (c *Client) Expand(partial string, validDestOnly bool) ([]api.EntityJSON, error) {
	u := c.httpURL("/expand?q=" + url.QueryEscape(partial))
	if validDestOnly {
		u += "&valid_dest_only=1"
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("expand request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expand: HTTP %s", resp.Status)
	}
	var er api.ExpandResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("expand decode: %w", err)
	}
	return er.Matches, nil
}

// FetchContent fetches the content of an info or memo from the proxy.
// contentType is "info" or "memo". target is "me" or a handle. name is the
// memo name (empty for info). Returns parsed content lines (stripped of "* " prefix).
func (c *Client) FetchContent(contentType, target, name string) ([]string, error) {
	u := c.httpURL(fmt.Sprintf("/fetch?type=%s&target=%s",
		url.QueryEscape(contentType), url.QueryEscape(target)))
	if name != "" {
		u += "&name=" + url.QueryEscape(name)
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: HTTP %s", resp.Status)
	}
	var fr api.FetchResponse
	if err := json.NewDecoder(resp.Body).Decode(&fr); err != nil {
		return nil, fmt.Errorf("fetch decode: %w", err)
	}
	return fr.Lines, nil
}

// StoreContent stores new content for an info or memo via the proxy.
func (c *Client) StoreContent(contentType, target, name string, lines []string) error {
	if lines == nil {
		lines = []string{}
	}
	body, _ := json.Marshal(api.StoreRequest{
		Type:   contentType,
		Target: target,
		Name:   name,
		Lines:  lines,
	})
	req, _ := http.NewRequest(http.MethodPost, c.httpURL("/store"), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("store request: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("store: HTTP %s", resp.Status)
	}
	return nil
}

// Connect upgrades to a WebSocket and starts delivering events on c.Events.
func (c *Client) Connect() error {
	ws, _, err := websocket.Dial(c.ctx, c.wsURL("/ws?token="+c.token), nil)
	if err != nil {
		return fmt.Errorf("ws connect: %w", err)
	}
	ws.SetReadLimit(-1) // no limit — command results can be arbitrarily large
	c.ws = ws
	c.connectedAt.Store(time.Now().UnixNano())
	c.closeReason.Store("")
	go c.readLoop()
	return nil
}

// Gen identifies this Client among the ones a session has been through; see
// clientGen. It is stable for the client's whole life.
func (c *Client) Gen() int64 { return c.gen }

// ConnectedAt is when Connect last opened this client's WebSocket, or the zero
// time if it never did.
func (c *Client) ConnectedAt() time.Time {
	ns := c.connectedAt.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// CloseReason is why this client's read loop stopped — a WebSocket close code
// and reason where the peer sent one — or "" while it is still running. It is
// the only record of why a socket went away: nothing else in the client keeps
// the read error, and in the browser build there is no other place to look.
func (c *Client) CloseReason() string {
	s, _ := c.closeReason.Load().(string)
	return s
}

// describeCloseErr renders a read error the way a bug report needs it: the
// WebSocket status code where the peer sent a close frame, since that is what
// separates a proxy that dropped us (an abrupt 1006 from its keepalive) from a
// browser or a network that closed the connection itself.
func describeCloseErr(err error) string {
	if err == nil {
		return "read loop ended"
	}
	if code := websocket.CloseStatus(err); code != -1 {
		return fmt.Sprintf("close code %d: %v", int(code), err)
	}
	return err.Error()
}

// Send sends a command to the proxy (which forwards it to Lily).
func (c *Client) Send(text string) error {
	if c.closed.Load() {
		return fmt.Errorf("client is closed")
	}
	// Without a socket there is nothing to write to, and wsjson.Write would
	// dereference the nil and take the whole program down from inside Update.
	// A client can be authenticated but unconnected — Auth and Connect are
	// separate steps — so this is reachable, and an error the TUI can show
	// beats a panic that loses the session.
	if c.ws == nil {
		return errors.New("not connected to the proxy")
	}
	return wsjson.Write(c.ctx, c.ws, api.WSClientMsg{Type: "command", Text: text})
}

// Close shuts down the WebSocket connection.
func (c *Client) Close() {
	c.closed.Store(true)
	c.cancel()
	if c.ws != nil {
		_ = c.ws.Close(websocket.StatusNormalClosure, "")
	}
}

func (c *Client) readLoop() {
	// Capture the channel at goroutine start so that a reconnect that replaces
	// c.Events doesn't cause this goroutine to close the new channel.
	ch := c.Events
	defer c.closeOnce.Do(func() { close(ch) })
	for {
		var msg api.WSServerMsg
		if err := wsjson.Read(c.ctx, c.ws, &msg); err != nil {
			// Record before closing the channel: the listener the close wakes
			// reads this to say why, and it must not race the write.
			c.closeReason.Store(describeCloseErr(err))
			return
		}
		// A plain send could block forever after Close: a Reconnect abandons this
		// client's Events channel, and context cancellation does not unblock a
		// channel send, which would strand this goroutine (and the WebSocket)
		// permanently once the buffer filled.
		select {
		case ch <- &msg:
		case <-c.ctx.Done():
			return
		}
	}
}

// Resume re-opens the WebSocket for the proxy session this client already
// holds, without logging in again. It returns a fresh Client carrying the same
// token, credentials and address, because a Client whose read loop has exited
// has closed its Events channel and cannot be listened to a second time.
//
// This is the right recovery for a socket that died underneath a session that
// did not: a laptop that slept, a tab the browser paused, an idle NAT entry
// that got reaped. The proxy session (and with it the Lily connection, the
// message-ID space and the event ring the client catches up from) is still
// there, so re-attaching costs one handshake and loses nothing. Reconnect, by
// contrast, builds a whole new Lily session and is what to fall back to when
// this fails because the session really is gone.
func (c *Client) Resume() (*Client, error) {
	// The caller is abandoning c either way, so retire it before anything can
	// fail and leave two clients holding sockets for one session.
	c.Close()
	nc := newClient(c.proxyAddr, c.secure)
	nc.token = c.token
	nc.username = c.username
	nc.password = c.password
	if c.token == "" {
		return nc, errors.New("no session to resume")
	}
	if err := nc.Connect(); err != nil {
		// A failed WebSocket dial does not say why it failed, and in the
		// browser build it cannot: the JS WebSocket API never exposes the
		// handshake's HTTP status, so "the proxy has forgotten this session"
		// and "the network was away for a second" arrive as the same error.
		// Ask over HTTP, where the status is visible, before concluding the
		// session is gone — mistaking a blip for a dead session costs the user
		// the session itself, because the caller answers that by dropping the
		// stored token and putting up the login dialog.
		if nc.sessionGone() {
			return nc, fmt.Errorf("resume: %w", ErrSessionGone)
		}
		return nc, fmt.Errorf("resume: %w", err)
	}
	return nc, nil
}

// sessionGone reports whether the proxy has definitively disowned this client's
// token. Only an explicit 401 counts: a proxy that could not be reached at all,
// or that answered anything else, leaves the session possibly alive, and the
// caller should keep retrying rather than tear it down.
func (c *Client) sessionGone() bool {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.httpURL("/session"), nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusUnauthorized
}

// Reconnect closes the current connection and returns a fresh Client using the
// same proxy address and stored credentials — i.e. it re-runs the normal login
// path without re-prompting the user. The caller should replace its client
// reference with the returned one. The fresh client is returned even on error so
// the caller can reuse it for a credential re-prompt retry; the error preserves
// ErrAuthFailed when the credentials were rejected.
func (c *Client) Reconnect() (*Client, error) {
	c.Close()
	nc := newClient(c.proxyAddr, c.secure)

	// A client resumed from a stored token holds no password, so there is
	// nothing to log back in with. Say so as ErrSessionGone: that, like a
	// credential rejection, is what the TUI turns into a login prompt, whereas
	// a generic error becomes an offer to retry that could never succeed. It is
	// deliberately not ErrAuthFailed — nothing here was rejected, and the user
	// should not be told their password was wrong when they never gave one.
	if c.password == "" {
		nc.username = c.username
		return nc, fmt.Errorf("%w: log in again to start a new one", ErrSessionGone)
	}

	if err := nc.Auth(c.username, c.password); err != nil {
		return nc, err
	}
	if err := nc.Connect(); err != nil {
		return nc, err
	}
	return nc, nil
}
