package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/joshw/zephyrlily/internal/lily"
	"github.com/joshw/zephyrlily/internal/proxy/commands"
	"github.com/joshw/zephyrlily/internal/slcp"
)

const (
	keepalivePingInterval = 150 * time.Second
	keepalivePongTimeout  = 5 * time.Second
)

// Config holds proxy server configuration.
type Config struct {
	ListenAddr      string // e.g. ":7888"
	LilyAddr        string // e.g. "rpi.lily.org:7777"
	LilyTLS         bool   // connect to Lily over TLS
	LilyTLSInsecure bool   // skip TLS certificate verification

	// Web UI
	ServeWeb    bool   // serve the embedded Svelte web app
	WebTLS      bool   // serve the web interface over HTTPS
	WebCertFile string // path to TLS cert PEM (empty = self-signed)
	WebKeyFile  string // path to TLS key PEM (empty = self-signed)
}

const maxEventBuf = 5000

// Server is the proxy HTTP/WebSocket server.
type Server struct {
	cfg        Config
	sessions   sync.Map // token -> *Session
	userTokens sync.Map // username -> token (for finding an existing session by username)
}

// Session represents an authenticated proxy session for one Lily user.
type Session struct {
	token    string
	username string
	conn     *lily.Conn

	subsMu      sync.Mutex
	subscribers map[*wsClient]struct{}

	// Per-session command aliases (see %alias / commands.AliasTable).
	aliases *commands.AliasTable

	// Per-session scheduled tasks (%after / %every / %cron).
	cron *commands.CronTable

	// Per-session event triggers (%on).
	on *commands.OnTable

	// Command output buffering
	cmdMu     sync.Mutex
	cmdBuffer map[int][]string // cmdID -> lines of output

	// Message ID counter
	nextMsgID atomic.Int64

	// Event history buffer
	eventBufMu sync.RWMutex
	eventBuf   []*WSServerMsg // capped circular buffer of recent events
	lastSeenID atomic.Int64   // last seen ID reported by a TUI client

	// Keepalive: unix nanoseconds of the most recent %pong received from Lily.
	// Written by fanOut, read by runKeepalive. Zero until the first pong arrives.
	lastPongReceivedAt atomic.Int64

	// Fetch coordination: an HTTP handler sets fetchResultCh before sending
	// the fetch command to Lily; fanOut routes the first command result that
	// arrives while the channel is set back through it instead of broadcasting.
	fetchMu       sync.Mutex
	fetchResultCh chan []string
	fetchCmdID    int

	// Store coordination: set before sending #$# export_file; fanOut routes
	// the %export_file OKAY/ERROR message back through the channel.
	storeMu       sync.Mutex
	storeResultCh chan string
}

// wsClient is a single WebSocket connection to the proxy.
type wsClient struct {
	ws   *websocket.Conn
	ctx  context.Context
	send chan *WSServerMsg
}

// assignID assigns the next message ID to a WSServerMsg.
func (s *Session) assignID(msg *WSServerMsg) {
	msg.ID = s.nextMsgID.Add(1)
}

// bufferableType reports whether a message type is retained in the event history
// buffer for catch-up by clients that connect after it was sent.
func bufferableType(t string) bool {
	switch t {
	case "event", "text", "commandresult", "clientcommand", "prompt":
		return true
	}
	return false
}

// publish assigns msg an ID, retains it in the event buffer when appropriate, and
// broadcasts it to every current subscriber.
func (s *Session) publish(msg *WSServerMsg) {
	s.assignID(msg)
	if bufferableType(msg.Type) {
		s.eventBufMu.Lock()
		s.eventBuf = append(s.eventBuf, msg)
		if len(s.eventBuf) > maxEventBuf {
			s.eventBuf = s.eventBuf[len(s.eventBuf)-maxEventBuf:]
		}
		s.eventBufMu.Unlock()
	}
	s.broadcast(msg)
}

// broadcast sends msg to every current subscriber without buffering it. Slow
// clients are skipped rather than blocking the sender.
func (s *Session) broadcast(msg *WSServerMsg) {
	s.subsMu.Lock()
	for client := range s.subscribers {
		select {
		case client.send <- msg:
		default:
		}
	}
	s.subsMu.Unlock()
}

// New creates a Server.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Run starts the HTTP server bound to ListenAddr and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	l, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.ListenAddr, err)
	}
	return s.RunWithListener(ctx, l)
}

// registerAPIRoutes mounts all proxy API endpoints on mux.
func (s *Server) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/auth", s.handleAuth)
	mux.HandleFunc("/state", s.handleState)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/seen", s.handleSeen)
	mux.HandleFunc("/expand", s.handleExpand)
	mux.HandleFunc("/fetch", s.handleFetch)
	mux.HandleFunc("/store", s.handleStore)
}

// RunWithListener starts the HTTP server using the provided listener and blocks
// until ctx is cancelled.  Use this to start on an OS-assigned ephemeral port
// by passing a listener created with net.Listen("tcp", "127.0.0.1:0").
func (s *Server) RunWithListener(ctx context.Context, l net.Listener) error {
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)

	// Mount the web UI catch-all last so API routes take priority.
	if s.cfg.ServeWeb {
		if err := addWebHandler(mux); err != nil {
			return fmt.Errorf("web handler: %w", err)
		}
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		mux.ServeHTTP(w, r)
	})

	if s.cfg.WebTLS {
		tlsCfg, err := s.buildTLSConfig()
		if err != nil {
			return fmt.Errorf("TLS config: %w", err)
		}
		l = tls.NewListener(l, tlsCfg)
		slog.Debug("zlily-proxy listening (TLS)", "addr", l.Addr())
	} else {
		slog.Debug("zlily-proxy listening", "addr", l.Addr())
	}

	srv := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleAuth authenticates a user against the Lily server and returns a token.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleAuth", "method", r.Method, "path", r.URL.Path)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	// Return the existing session token if this user is already connected.
	if existing, ok := s.userTokens.Load(req.Username); ok {
		if _, ok := s.sessions.Load(existing.(string)); ok {
			writeJSON(w, AuthResponse{Token: existing.(string)})
			return
		}
	}

	conn := lily.NewConn(s.cfg.LilyAddr, req.Username, req.Password, s.cfg.LilyTLS, s.cfg.LilyTLSInsecure)
	if err := conn.Connect(); err != nil {
		if errors.Is(err, lily.ErrAuthFailed) {
			http.Error(w, lily.ErrAuthFailed.Error(), http.StatusUnauthorized)
			return
		}
		http.Error(w, "lily connect failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	token, err := generateToken()
	if err != nil {
		http.Error(w, "token generation failed", http.StatusInternalServerError)
		return
	}

	sess := &Session{
		token:       token,
		username:    req.Username,
		conn:        conn,
		subscribers: make(map[*wsClient]struct{}),
		cmdBuffer:   make(map[int][]string),
		aliases:     commands.NewAliasTable(),
	}

	// Timer- and event-driven commands re-inject their actions through
	// dispatchInput and publish their output to every client (they fire outside
	// any single client's request), mirroring how %startup replays the memo.
	sessFire := func(line string) { _ = sess.dispatchInput(line, sess.publish) }
	sessAnnounce := func(lines []string) {
		for _, line := range lines {
			sess.publish(&WSServerMsg{Type: "text", Data: TextData{Text: line}})
		}
	}
	sess.cron = commands.NewCronTable(sessFire, sessAnnounce)
	sess.on = commands.NewOnTable(sessFire, sessAnnounce)

	// A (re)connect always starts a fresh session: a new Lily login replays the
	// full login sequence (banner, blurb/review prompts, entity sync), and the
	// TUI's existing scrollback is preserved client-side. Carrying over the prior
	// event buffer / last-seen ID here would mismatch the new session's restarted
	// message-ID counter and suppress the very prompts (e.g. "enter a blurb") the
	// user must answer to finish logging in — so reconnect is just login again.

	s.sessions.Store(token, sess)
	s.userTokens.Store(req.Username, token)

	go s.fanOut(sess)
	go s.runStartup(sess)

	writeJSON(w, AuthResponse{Token: token})
}

// handleState returns the current Lily state snapshot for the authenticated session.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Block until the initial SLCP sync completes so the caller receives fully-
	// populated entity data. Login can include interactive prompts (e.g. "enter a
	// blurb", "review now?") that gate the sync until the user answers, so the
	// timeout is generous; it only fires if the server or user never proceeds.
	select {
	case <-sess.conn.SyncComplete():
	case <-time.After(60 * time.Second):
		slog.Debug("handleState: sync timeout", "user", sess.username)
	}

	st := sess.conn.State()
	entities := st.AllEntities()
	sess.eventBufMu.RLock()
	eventBufSize := len(sess.eventBuf)
	sess.eventBufMu.RUnlock()

	resp := StateResponse{
		Whoami:       st.Whoami,
		Version:      st.Version,
		Server:       st.Name,
		Entities:     make([]EntityJSON, 0, len(entities)),
		LastSeenID:   sess.lastSeenID.Load(),
		EventBufSize: eventBufSize,
	}
	for _, e := range entities {
		j := entityToJSON(e)
		if e.Kind == lily.KindDisc {
			j.Member = st.IsDiscMember(e.Handle)
		}
		resp.Entities = append(resp.Entities, j)
	}
	writeJSON(w, resp)
}

// handleWS upgrades the connection to WebSocket and wires up the session.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // CORS — clients are same-host or trusted
	})
	if err != nil {
		slog.Debug("ws accept error", "err", err)
		return
	}
	defer func() { _ = ws.CloseNow() }()

	ctx := r.Context()
	client := &wsClient{
		ws:   ws,
		ctx:  ctx,
		send: make(chan *WSServerMsg, 64),
	}

	sess.subsMu.Lock()
	sess.subscribers[client] = struct{}{}
	sess.subsMu.Unlock()

	// For fresh sessions (no prior history), replay the buffered event ring
	// immediately so the subscriber receives messages — including interactive
	// prompts and raw server text — that arrived before it connected.
	// Reconnecting sessions retrieve missed history via GET /events instead.
	if sess.lastSeenID.Load() == 0 {
		sess.eventBufMu.RLock()
		snapshot := make([]*WSServerMsg, len(sess.eventBuf))
		copy(snapshot, sess.eventBuf)
		sess.eventBufMu.RUnlock()
		for _, msg := range snapshot {
			select {
			case client.send <- msg:
			default:
			}
		}
	}

	go client.writeLoop()
	client.readLoop(sess) // blocks until client disconnects

	sess.subsMu.Lock()
	delete(sess.subscribers, client)
	sess.subsMu.Unlock()

	_ = ws.Close(websocket.StatusNormalClosure, "")
}

// fanOut reads from sess.conn.Events and broadcasts to all WebSocket subscribers.
// It also handles command output buffering, collecting all output between
// %begin and %end into a single message.
func (s *Server) fanOut(sess *Session) {
	doneCh := make(chan struct{})
	defer close(doneCh)

	go s.runKeepalive(sess, doneCh)

	for msg := range sess.conn.Events {
		// Pong responses update keepalive state and are not forwarded to clients.
		if msg.Type == slcp.MsgPong {
			sess.lastPongReceivedAt.Store(time.Now().UnixNano())
			continue
		}

		// %export_file OKAY/ERROR — route to a waiting store handler.
		if msg.Type == slcp.MsgExportFile {
			sess.storeMu.Lock()
			if sess.storeResultCh != nil {
				select {
				case sess.storeResultCh <- msg.Text:
				default:
				}
				sess.storeResultCh = nil
			}
			sess.storeMu.Unlock()
			continue
		}

		// Handle command leafing: buffer output between %begin and %end
		sess.cmdMu.Lock()
		switch msg.Type {
		case slcp.MsgCmdBegin:
			// Start buffering for this command
			id := msg.CmdID
			sess.cmdBuffer[id] = []string{}
			sess.cmdMu.Unlock()
			// If a fetch is pending and we haven't pinned a command ID yet,
			// claim this one.  We don't match on text because the server may
			// echo a different form of the command than we sent.
			sess.fetchMu.Lock()
			if sess.fetchResultCh != nil && sess.fetchCmdID == 0 {
				slog.Debug("fetch pinning", "cmdID", id, "beginText", msg.Text)
				sess.fetchCmdID = id
			}
			sess.fetchMu.Unlock()
			continue // don't send individual begin messages

		case slcp.MsgCmdEnd:
			// Send the complete buffered output
			id := msg.CmdID
			if lines, ok := sess.cmdBuffer[id]; ok {
				delete(sess.cmdBuffer, id)
				sess.cmdMu.Unlock()

				// Route to a waiting fetch handler instead of broadcasting.
				sess.fetchMu.Lock()
				if sess.fetchResultCh != nil && id == sess.fetchCmdID {
					select {
					case sess.fetchResultCh <- lines:
					default:
					}
					sess.fetchResultCh = nil
					sess.fetchCmdID = 0
					sess.fetchMu.Unlock()
					continue
				}
				sess.fetchMu.Unlock()

				sess.publish(&WSServerMsg{
					Type: "commandresult",
					Data: CommandResultData{
						CmdID: id,
						Lines: lines,
					},
				})
				continue
			}
			sess.cmdMu.Unlock()
			continue

		case slcp.MsgRaw:
			// Check if this belongs to any active command
			// We'll add it to all active command buffers
			// (in practice, there's usually only one active command at a time)
			added := false
			for id := range sess.cmdBuffer {
				text := msg.Text
				// Strip "%command [id] " prefix if present
				prefix := fmt.Sprintf("%%command [%d] ", id)
				text = strings.TrimPrefix(text, prefix)
				sess.cmdBuffer[id] = append(sess.cmdBuffer[id], text)
				added = true
			}
			if added {
				sess.cmdMu.Unlock()
				continue // don't send raw lines that are part of command output
			}
		}
		sess.cmdMu.Unlock()

		// Not part of command output - convert and send normally
		wsMsg := msgToWS(msg, sess)
		if wsMsg == nil {
			continue
		}
		sess.publish(wsMsg)

		// Evaluate %on triggers against notification events.
		if msg.Type == slcp.MsgNotify {
			if ev, err := slcp.ParseNotify(msg); err == nil {
				sess.on.Dispatch(ev, sess.conn.State())
			}
		}
	}

	// Stop any scheduled tasks; their session is gone.
	sess.cron.StopAll()

	// Lily connection closed — notify subscribers.
	sess.publish(&WSServerMsg{Type: "error", Data: "lily connection closed"})
	s.sessions.Delete(sess.token)
	s.userTokens.Delete(sess.username)

	// Fully close our side of the Lily socket. On a natural drop we have only
	// read EOF (leaving the socket in CLOSE_WAIT); sending our FIN tells the Lily
	// server the session is gone so it reaps it promptly, which lets a follow-up
	// reconnect log in without waiting for the old session to time out.
	sess.conn.Close()
}

// startupMemoName is the memo fetched from the user after login whose lines are
// replayed as commands. Persisting an alias (or any startup command) is done by
// adding the line to this memo.
const startupMemoName = "zlilyStartup"

// runStartup waits for the initial Lily sync, then fetches the user's
// zlilyStartup memo and replays each non-comment, non-blank line as a command.
// Output is published (buffered + broadcast) so a client that connects after
// login still receives it through the /events catch-up path.
func (s *Server) runStartup(sess *Session) {
	select {
	case <-sess.conn.SyncComplete():
	case <-time.After(60 * time.Second):
		slog.Debug("runStartup: sync timeout", "user", sess.username)
		return
	}

	if err := sess.replayStartup(sess.publish, false); err != nil {
		slog.Debug("runStartup: " + err.Error())
	}
}

// replayStartup fetches the user's zlilyStartup memo and replays each
// non-comment, non-blank line as a command, emitting output through emit. It is
// invoked both after the initial login sync and on demand via the %startup
// command. When announce is true (the %startup path) a fetch failure or empty
// memo is reported to the user; on login it is silent. Only a genuine Lily send
// error is returned, so a missing memo never tears down the session.
func (sess *Session) replayStartup(emit func(*WSServerMsg), announce bool) error {
	lines, err := sess.fetchLines("/memo me " + startupMemoName)
	if err != nil {
		// No memo (or fetch failed) — nothing to replay. Not a Lily send error,
		// so don't propagate it (which would tear down the session).
		if announce {
			emit(&WSServerMsg{Type: "text",
				Data: TextData{Text: "(no " + startupMemoName + " memo to run)"}})
		}
		slog.Debug("replayStartup fetch: " + err.Error())
		return nil
	}

	var toRun []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		toRun = append(toRun, line)
	}
	if len(toRun) == 0 {
		if announce {
			emit(&WSServerMsg{Type: "text",
				Data: TextData{Text: "(" + startupMemoName + " memo has no commands to run)"}})
		}
		return nil
	}

	emit(&WSServerMsg{Type: "text",
		Data: TextData{Text: "(found " + startupMemoName + " memo, running its commands)"}})

	for _, line := range toRun {
		if err := sess.dispatchInput(line, emit); err != nil {
			return err
		}
	}
	return nil
}

// handleEvents returns buffered event and text messages after a given ID.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	afterID := int64(0)
	if v := r.URL.Query().Get("after"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			afterID = parsed
		}
	}

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			limit = parsed
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	sess.eventBufMu.RLock()
	collected := make([]WSServerMsg, 0)
	for _, msg := range sess.eventBuf {
		if msg.ID > afterID {
			collected = append(collected, *msg)
		}
	}
	sess.eventBufMu.RUnlock()

	more := false
	if len(collected) > limit {
		more = true
		collected = collected[:limit]
	}

	writeJSON(w, EventsResponse{Events: collected, More: more})
}

// handleSeen records the last seen message ID for the session.
func (s *Server) handleSeen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req SeenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	// CAS loop: only update if new value is higher
	for {
		old := sess.lastSeenID.Load()
		if req.LastSeenID <= old {
			break
		}
		if sess.lastSeenID.CompareAndSwap(old, req.LastSeenID) {
			break
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// readLoop receives commands from the WebSocket client and forwards them to Lily.
func (c *wsClient) readLoop(sess *Session) {
	defer close(c.send)
	for {
		var cm WSClientMsg
		if err := wsjson.Read(c.ctx, c.ws, &cm); err != nil {
			return
		}
		if cm.Type != "command" {
			continue
		}
		// Command output goes only to the client that issued it (assigned an ID
		// but not buffered/broadcast, matching the prior behaviour).
		emit := func(msg *WSServerMsg) {
			sess.assignID(msg)
			select {
			case c.send <- msg:
			default:
			}
		}
		if err := sess.dispatchInput(cm.Text, emit); err != nil {
			slog.Debug("lily send error", "err", err)
			return
		}
	}
}

// dispatchInput expands any alias in text and dispatches each resulting command
// line. emit publishes proxy-command output and forwarded client commands. A
// send error to the Lily server is returned so the caller can tear down.
func (sess *Session) dispatchInput(text string, emit func(*WSServerMsg)) error {
	lines := []string{text}
	if expanded, ok := sess.aliases.Expand(text); ok {
		lines = expanded
	}
	for _, line := range lines {
		if err := sess.dispatchLine(line, emit); err != nil {
			return err
		}
	}
	return nil
}

// dispatchLine handles one concrete command line: %alias and registered proxy
// commands are handled here; any other %-command is forwarded to the client to
// run locally; everything else is sent upstream to Lily.
func (sess *Session) dispatchLine(line string, emit func(*WSServerMsg)) error {
	if strings.HasPrefix(line, "%") {
		fields := strings.Fields(line)
		var cmd string
		if len(fields) > 0 {
			cmd = fields[0]
		}
		respond := func(lines []string) {
			emit(&WSServerMsg{Type: "commandresult", Data: CommandResultData{CmdID: 0, Lines: lines}})
		}
		switch {
		case cmd == "%startup":
			return sess.replayStartup(emit, true)
		case cmd == "%alias":
			sess.aliases.HandleCommand(fields[1:], respond)
		case cmd == "%after" || cmd == "%every" || cmd == "%cron":
			sess.cron.HandleCommand(strings.TrimPrefix(cmd, "%"), fields[1:], respond)
		case cmd == "%on":
			// %on needs quote-aware parsing, so pass the raw remainder.
			sess.on.HandleCommand(strings.TrimSpace(strings.TrimPrefix(line, cmd)), sess.conn.State(), respond)
		case commands.IsRegistered(cmd):
			commands.Execute(sess.conn.State(), line, respond)
		default:
			// A client-only command (e.g. %style) — forward for local execution.
			emit(&WSServerMsg{Type: "clientcommand", Data: ClientCommandData{Text: line}})
		}
		return nil
	}
	// Plain text — forward to Lily.
	return sess.conn.Send(line)
}

// writeLoop drains the send channel and writes messages to the WebSocket.
func (c *wsClient) writeLoop() {
	for msg := range c.send {
		if err := wsjson.Write(c.ctx, c.ws, msg); err != nil {
			return
		}
	}
}

// sessionFromRequest extracts the session token from the Authorization header
// ("Bearer <token>") or the "token" query parameter.
func (s *Server) sessionFromRequest(r *http.Request) (*Session, bool) {
	token := r.URL.Query().Get("token")
	if token == "" {
		auth := r.Header.Get("Authorization")
		if len(auth) > 7 && auth[:7] == "Bearer " {
			token = auth[7:]
		}
	}
	if token == "" {
		return nil, false
	}
	v, ok := s.sessions.Load(token)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

// generateToken returns a cryptographically random 32-byte hex token.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// msgToWS converts a parsed SLCP message to a WebSocket server message.
func msgToWS(msg *slcp.Message, sess *Session) *WSServerMsg {
	switch msg.Type {
	case slcp.MsgNotify:
		ev, err := slcp.ParseNotify(msg)
		if err != nil {
			return nil
		}

		// Collect all entity handles referenced in this event
		handles := make(map[string]bool)
		if ev.Source != "" {
			handles[ev.Source] = true
		}
		for _, h := range ev.Recips {
			handles[h] = true
		}
		for _, h := range ev.Targets {
			handles[h] = true
		}

		// Look up entity state for each handle
		st := sess.conn.State()
		entities := make(map[string]EntityJSON)
		for handle := range handles {
			if entity := st.Get(handle); entity != nil {
				j := entityToJSON(entity)
				if entity.Kind == lily.KindDisc {
					j.Member = st.IsDiscMember(entity.Handle)
				}
				entities[handle] = j
			}
		}

		return &WSServerMsg{
			Type: "event",
			Data: EventData{
				Event:    ev.Event,
				Source:   ev.Source,
				Time:     ev.Time,
				Value:    ev.Value,
				Recips:   ev.Recips,
				Targets:  ev.Targets,
				SubEvt:   ev.SubEvt,
				Notify:   ev.Notify,
				Stamp:    ev.Stamp,
				Entities: entities,
				Text:     formatEventText(ev, entities, st.Whoami),
			},
		}
	case slcp.MsgRaw:
		return &WSServerMsg{Type: "text", Data: TextData{Text: msg.Text}}
	case slcp.MsgPrompt:
		return &WSServerMsg{Type: "prompt", Data: msg.Text}
	default:
		return nil
	}
}

// entityToJSON converts a lily.Entity to its wire representation.
func entityToJSON(e *lily.Entity) EntityJSON {
	j := EntityJSON{
		Handle:  e.Handle,
		Name:    e.Name,
		Blurb:   e.Blurb,
		State:   e.State,
		Pronoun: e.Pronoun,
		Title:   e.Title,
		Attrib:  e.Attrib,
		Members: e.Members,
	}
	switch e.Kind {
	case lily.KindUser:
		j.Kind = "user"
	case lily.KindDisc:
		j.Kind = "disc"
		j.Creation = e.Creation
	case lily.KindGroup:
		j.Kind = "group"
	}
	return j
}

// handleExpand returns entities whose names match the given partial string.
// Exact matches (case-insensitive) are returned first; if none, prefix matches
// are returned instead. The TUI applies the reference "unique match wins" rule.
//
// Query parameters:
//
//	q        - the partial name to match
//	valid_dest_only=1 - exclude discussions the current user is not a member of
func (s *Server) handleExpand(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, ExpandResponse{Matches: []EntityJSON{}})
		return
	}
	validDestOnly := r.URL.Query().Get("valid_dest_only") == "1"

	state := sess.conn.State()
	entities := state.AllEntities()
	qLower := strings.ToLower(q)

	// isValid reports whether an entity is a valid send destination.
	// With validDestOnly, discussions the user is not a member of are excluded.
	isValid := func(e *lily.Entity) bool {
		if validDestOnly && e.Kind == lily.KindDisc {
			return state.IsDiscMember(e.Handle)
		}
		return true
	}

	// Exact match first.
	var exact []EntityJSON
	for _, e := range entities {
		if strings.EqualFold(e.Name, q) && isValid(e) {
			exact = append(exact, entityToJSON(e))
		}
	}
	if len(exact) > 0 {
		writeJSON(w, ExpandResponse{Matches: exact})
		return
	}

	// Prefix match.
	var prefix []EntityJSON
	for _, e := range entities {
		if strings.HasPrefix(strings.ToLower(e.Name), qLower) && isValid(e) {
			prefix = append(prefix, entityToJSON(e))
		}
	}
	if prefix == nil {
		prefix = []EntityJSON{}
	}
	writeJSON(w, ExpandResponse{Matches: prefix})
}

// handleFetch fetches the content of an info or memo from the Lily server.
// Query params: type=info|memo, target=<handle or "me">, name=<memo name>.
// The command result is intercepted by fanOut and returned here rather than
// broadcast to WebSocket clients.
func (s *Server) handleFetch(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	contentType := r.URL.Query().Get("type")
	if contentType == "" {
		contentType = "info"
	}
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "me"
	}
	name := r.URL.Query().Get("name")

	var cmd string
	switch contentType {
	case "info":
		cmd = "/info " + target
	case "memo":
		cmd = "/memo " + target + " " + name
	default:
		http.Error(w, "unknown type", http.StatusBadRequest)
		return
	}

	content, err := sess.fetchLines(cmd)
	if err != nil {
		switch {
		case errors.Is(err, errFetchInProgress):
			http.Error(w, "fetch already in progress", http.StatusConflict)
		case errors.Is(err, errFetchTimeout):
			http.Error(w, "fetch timeout", http.StatusGatewayTimeout)
		default:
			http.Error(w, "send failed: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, FetchResponse{Lines: content})
}

var (
	errFetchInProgress = errors.New("fetch already in progress")
	errFetchTimeout    = errors.New("fetch timeout")
)

// fetchLines sends cmd to the Lily server and returns its command output with the
// server's "* " content prefix stripped. fanOut routes the result back here rather
// than broadcasting it. Only one fetch may be in flight per session at a time.
func (sess *Session) fetchLines(cmd string) ([]string, error) {
	resultCh := make(chan []string, 1)
	sess.fetchMu.Lock()
	if sess.fetchResultCh != nil {
		sess.fetchMu.Unlock()
		return nil, errFetchInProgress
	}
	sess.fetchResultCh = resultCh
	sess.fetchCmdID = 0
	sess.fetchMu.Unlock()

	if err := sess.conn.Send(cmd); err != nil {
		sess.fetchMu.Lock()
		sess.fetchResultCh = nil
		sess.fetchMu.Unlock()
		return nil, err
	}

	select {
	case lines := <-resultCh:
		// Content lines are prefixed "* " by the server; strip those two chars.
		content := make([]string, 0, len(lines))
		for _, line := range lines {
			if strings.HasPrefix(line, "* ") {
				content = append(content, line[2:])
			}
		}
		return content, nil
	case <-time.After(10 * time.Second):
		sess.fetchMu.Lock()
		sess.fetchResultCh = nil
		sess.fetchMu.Unlock()
		return nil, errFetchTimeout
	}
}

// handleStore stores new content for an info or memo via the Lily export_file protocol.
func (s *Server) handleStore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req StoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	// Lily uses an empty target string to mean "me".
	target := req.Target
	if target == "me" {
		target = ""
	}

	var exportCmd string
	switch req.Type {
	case "info":
		if target == "" {
			exportCmd = fmt.Sprintf("#$# export_file info %d", len(req.Lines))
		} else {
			exportCmd = fmt.Sprintf("#$# export_file info %d %s", len(req.Lines), target)
		}
	case "memo":
		byteSize := 0
		for _, line := range req.Lines {
			byteSize += len(line)
		}
		if target == "" {
			exportCmd = fmt.Sprintf("#$# export_file memo %d %d %s", byteSize, len(req.Lines), req.Name)
		} else {
			exportCmd = fmt.Sprintf("#$# export_file memo %d %d %s %s", byteSize, len(req.Lines), req.Name, target)
		}
	default:
		http.Error(w, "unknown type", http.StatusBadRequest)
		return
	}

	resultCh := make(chan string, 1)
	sess.storeMu.Lock()
	if sess.storeResultCh != nil {
		sess.storeMu.Unlock()
		http.Error(w, "store already in progress", http.StatusConflict)
		return
	}
	sess.storeResultCh = resultCh
	sess.storeMu.Unlock()

	if err := sess.conn.Send(exportCmd); err != nil {
		sess.storeMu.Lock()
		sess.storeResultCh = nil
		sess.storeMu.Unlock()
		http.Error(w, "send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	select {
	case response := <-resultCh:
		if response != "OKAY" {
			http.Error(w, "server rejected store: "+response, http.StatusBadGateway)
			return
		}
		for _, line := range req.Lines {
			if err := sess.conn.Send(line); err != nil {
				http.Error(w, "send line failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case <-time.After(10 * time.Second):
		sess.storeMu.Lock()
		sess.storeResultCh = nil
		sess.storeMu.Unlock()
		http.Error(w, "store timeout", http.StatusGatewayTimeout)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// runKeepalive sends periodic pings to the Lily server and notifies subscribers
// if a pong response is not received within keepalivePongTimeout.
// Pong arrivals are recorded independently by fanOut via sess.lastPongReceivedAt;
// this goroutine simply checks that field after the timeout elapses.
// It exits when doneCh is closed (i.e. when fanOut returns).
func (s *Server) runKeepalive(sess *Session, done <-chan struct{}) {
	ticker := time.NewTicker(keepalivePingInterval)
	defer ticker.Stop()

	notResponding := false

	broadcast := func(text string) {
		msg := &WSServerMsg{Type: "error", Data: text}
		sess.assignID(msg)
		sess.subsMu.Lock()
		for c := range sess.subscribers {
			select {
			case c.send <- msg:
			default:
			}
		}
		sess.subsMu.Unlock()
	}

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}

		sentAt := time.Now()
		if err := sess.conn.Send("#$# ping"); err != nil {
			return
		}

		// Wait the pong timeout, then check whether a pong arrived since the ping.
		select {
		case <-done:
			return
		case <-time.After(keepalivePongTimeout):
		}

		pongAt := time.Unix(0, sess.lastPongReceivedAt.Load())
		if pongAt.After(sentAt) {
			if notResponding {
				notResponding = false
				broadcast("keepalive: server is responding again")
			}
		} else {
			if !notResponding {
				notResponding = true
				broadcast("keepalive: server not responding")
			}
		}
	}
}
