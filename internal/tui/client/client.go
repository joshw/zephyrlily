// Package client connects the TUI to the zlily-proxy over HTTP and WebSocket.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/joshw/zephyrlily/internal/proxy/api"
)

// Client is a connection from the TUI to the proxy.
type Client struct {
	proxyAddr string // e.g. "localhost:7888"
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
}

// New creates a Client pointing at the given proxy address.
func New(proxyAddr string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		proxyAddr: proxyAddr,
		ctx:       ctx,
		cancel:    cancel,
		Events:    make(chan *api.WSServerMsg, 256),
	}
}

// Auth authenticates against the proxy and stores the session token.
func (c *Client) Auth(username, password string) error {
	body, _ := json.Marshal(api.AuthRequest{Username: username, Password: password})
	resp, err := http.Post("http://"+c.proxyAddr+"/auth", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("auth request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Read the error body to get the detailed error message
		var errBody bytes.Buffer
		errBody.ReadFrom(resp.Body)
		errMsg := errBody.String()
		if errMsg != "" {
			return fmt.Errorf("auth failed: %s", errMsg)
		}
		return fmt.Errorf("auth failed: %s", resp.Status)
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
	req, _ := http.NewRequest(http.MethodGet, "http://"+c.proxyAddr+"/state", nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("state request: %w", err)
	}
	defer resp.Body.Close()
	var sr api.StateResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("state decode: %w", err)
	}
	return &sr, nil
}

// FetchEvents retrieves buffered events from the proxy after the given ID.
func (c *Client) FetchEvents(afterID int64, limit int) ([]api.WSServerMsg, bool, error) {
	url := fmt.Sprintf("http://%s/events?after=%d&limit=%d", c.proxyAddr, afterID, limit)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("events request: %w", err)
	}
	defer resp.Body.Close()
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
	req, _ := http.NewRequest(http.MethodPost, "http://"+c.proxyAddr+"/seen", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("seen request: %w", err)
	}
	resp.Body.Close()
	c.lastReportedSeenID.Store(lastSeenID)
	return nil
}

// Expand searches the proxy entity state for names matching partial.
// The proxy returns exact matches first; if none, prefix matches.
// Callers apply the "unique match wins" rule to the result.
// When validDestOnly is true the proxy excludes discussions the current user
// is not a member of.
func (c *Client) Expand(partial string, validDestOnly bool) ([]api.EntityJSON, error) {
	u := "http://" + c.proxyAddr + "/expand?q=" + url.QueryEscape(partial)
	if validDestOnly {
		u += "&valid_dest_only=1"
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("expand request: %w", err)
	}
	defer resp.Body.Close()
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
	u := fmt.Sprintf("http://%s/fetch?type=%s&target=%s",
		c.proxyAddr, url.QueryEscape(contentType), url.QueryEscape(target))
	if name != "" {
		u += "&name=" + url.QueryEscape(name)
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch request: %w", err)
	}
	defer resp.Body.Close()
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
	req, _ := http.NewRequest(http.MethodPost, "http://"+c.proxyAddr+"/store", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("store request: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("store: HTTP %s", resp.Status)
	}
	return nil
}

// Connect upgrades to a WebSocket and starts delivering events on c.Events.
func (c *Client) Connect() error {
	ws, _, err := websocket.Dial(c.ctx, "ws://"+c.proxyAddr+"/ws?token="+c.token, nil)
	if err != nil {
		return fmt.Errorf("ws connect: %w", err)
	}
	ws.SetReadLimit(-1) // no limit — command results can be arbitrarily large
	c.ws = ws
	go c.readLoop()
	return nil
}

// Send sends a command to the proxy (which forwards it to Lily).
func (c *Client) Send(text string) error {
	return wsjson.Write(c.ctx, c.ws, api.WSClientMsg{Type: "command", Text: text})
}

// Close shuts down the WebSocket connection.
func (c *Client) Close() {
	c.cancel()
	if c.ws != nil {
		c.ws.Close(websocket.StatusNormalClosure, "")
	}
}

func (c *Client) readLoop() {
	// Capture the channel at goroutine start so that a reconnect that replaces
	// c.Events doesn't cause this goroutine to close the new channel.
	ch := c.Events
	defer close(ch)
	for {
		var msg api.WSServerMsg
		if err := wsjson.Read(c.ctx, c.ws, &msg); err != nil {
			return
		}
		ch <- &msg
	}
}

// Reconnect closes the current connection and returns a fresh Client using the
// same proxy address and stored credentials.  The caller should replace its
// client reference with the returned one.
func (c *Client) Reconnect() (*Client, error) {
	c.Close()
	nc := New(c.proxyAddr)
	if err := nc.Auth(c.username, c.password); err != nil {
		return nil, fmt.Errorf("reconnect auth: %w", err)
	}
	if err := nc.Connect(); err != nil {
		return nil, fmt.Errorf("reconnect ws: %w", err)
	}
	return nc, nil
}
