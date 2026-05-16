package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	ListenAddr string // e.g. ":7888"
	LilyAddr   string // e.g. "rpi.lily.org:7777"
}

const maxEventBuf = 5000

// savedState holds the event buffer and last-seen ID across session lifecycle events
// (e.g. Lily TCP disconnect followed by TUI reconnect).
type savedState struct {
	eventBuf   []*WSServerMsg
	lastSeenID int64
}

// Server is the proxy HTTP/WebSocket server.
type Server struct {
	cfg         Config
	sessions    sync.Map // token -> *Session
	savedStates sync.Map // token -> savedState (persists across session deletions)
}

// Session represents an authenticated proxy session for one Lily user.
type Session struct {
	token string
	conn  *lily.Conn

	subsMu      sync.Mutex
	subscribers map[*wsClient]struct{}

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

// New creates a Server.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", s.handleAuth)
	mux.HandleFunc("/state", s.handleState)
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/seen", s.handleSeen)

	// Wrap with logging middleware
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("incoming: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		mux.ServeHTTP(w, r)
	})

	srv := &http.Server{Addr: s.cfg.ListenAddr, Handler: handler}

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	log.Printf("zlily-proxy listening on %s", s.cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleAuth authenticates a user against the Lily server and returns a token.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	log.Printf("handleAuth: %s %s", r.Method, r.URL.Path)
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

	// Return existing session if already connected.
	token := sessionToken(req.Username)
	if _, ok := s.sessions.Load(token); ok {
		writeJSON(w, AuthResponse{Token: token})
		return
	}

	conn := lily.NewConn(s.cfg.LilyAddr, req.Username, req.Password)
	if err := conn.Connect(); err != nil {
		http.Error(w, "lily connect failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	sess := &Session{
		token:       token,
		conn:        conn,
		subscribers: make(map[*wsClient]struct{}),
		cmdBuffer:   make(map[int][]string),
	}

	// Restore event buffer and last-seen position from the previous session if present.
	if v, ok := s.savedStates.LoadAndDelete(token); ok {
		saved := v.(savedState)
		sess.eventBuf = saved.eventBuf
		sess.lastSeenID.Store(saved.lastSeenID)
	}

	s.sessions.Store(token, sess)

	go s.fanOut(sess)

	writeJSON(w, AuthResponse{Token: token})
}

// handleState returns the current Lily state snapshot for the authenticated session.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
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
		resp.Entities = append(resp.Entities, entityToJSON(e))
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
		log.Printf("ws accept: %v", err)
		return
	}
	defer ws.CloseNow()

	ctx := r.Context()
	client := &wsClient{
		ws:   ws,
		ctx:  ctx,
		send: make(chan *WSServerMsg, 64),
	}

	sess.subsMu.Lock()
	sess.subscribers[client] = struct{}{}
	sess.subsMu.Unlock()

	go client.writeLoop()
	client.readLoop(sess) // blocks until client disconnects

	sess.subsMu.Lock()
	delete(sess.subscribers, client)
	sess.subsMu.Unlock()

	ws.Close(websocket.StatusNormalClosure, "")
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

		// Handle command leafing: buffer output between %begin and %end
		sess.cmdMu.Lock()
		switch msg.Type {
		case slcp.MsgCmdBegin:
			// Start buffering for this command
			id := msg.CmdID
			sess.cmdBuffer[id] = []string{}
			sess.cmdMu.Unlock()
			continue // don't send individual begin messages

		case slcp.MsgCmdEnd:
			// Send the complete buffered output
			id := msg.CmdID
			if lines, ok := sess.cmdBuffer[id]; ok {
				wsMsg := &WSServerMsg{
					Type: "commandresult",
					Data: CommandResultData{
						CmdID: id,
						Lines: lines,
					},
				}
				sess.assignID(wsMsg)
				delete(sess.cmdBuffer, id)
				sess.cmdMu.Unlock()

				if wsMsg.Type == "event" || wsMsg.Type == "text" || wsMsg.Type == "commandresult" {
					sess.eventBufMu.Lock()
					sess.eventBuf = append(sess.eventBuf, wsMsg)
					if len(sess.eventBuf) > maxEventBuf {
						sess.eventBuf = sess.eventBuf[len(sess.eventBuf)-maxEventBuf:]
					}
					sess.eventBufMu.Unlock()
				}

				// Broadcast to subscribers
				sess.subsMu.Lock()
				for client := range sess.subscribers {
					select {
					case client.send <- wsMsg:
					default:
						// slow client — drop rather than block
					}
				}
				sess.subsMu.Unlock()
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
				if strings.HasPrefix(text, prefix) {
					text = text[len(prefix):]
				}
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
		sess.assignID(wsMsg)

		if wsMsg.Type == "event" || wsMsg.Type == "text" || wsMsg.Type == "commandresult" {
			sess.eventBufMu.Lock()
			sess.eventBuf = append(sess.eventBuf, wsMsg)
			if len(sess.eventBuf) > maxEventBuf {
				sess.eventBuf = sess.eventBuf[len(sess.eventBuf)-maxEventBuf:]
			}
			sess.eventBufMu.Unlock()
		}

		sess.subsMu.Lock()
		for client := range sess.subscribers {
			select {
			case client.send <- wsMsg:
			default:
				// slow client — drop rather than block
			}
		}
		sess.subsMu.Unlock()
	}

	// Lily connection closed — notify subscribers.
	errMsg := &WSServerMsg{Type: "error", Data: "lily connection closed"}
	sess.assignID(errMsg)
	sess.subsMu.Lock()
	for client := range sess.subscribers {
		select {
		case client.send <- errMsg:
		default:
		}
	}
	sess.subsMu.Unlock()
	// Persist event buffer and last-seen ID so the next session for this user
	// can restore history even after a Lily TCP disconnect.
	sess.eventBufMu.RLock()
	bufCopy := make([]*WSServerMsg, len(sess.eventBuf))
	copy(bufCopy, sess.eventBuf)
	sess.eventBufMu.RUnlock()
	s.savedStates.Store(sess.token, savedState{
		eventBuf:   bufCopy,
		lastSeenID: sess.lastSeenID.Load(),
	})
	s.sessions.Delete(sess.token)
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
	var collected []WSServerMsg
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
	defer r.Body.Close()

	// CAS loop: only update if new value is higher
	for {
		old := sess.lastSeenID.Load()
		if req.LastSeenID <= old {
			break
		}
		if sess.lastSeenID.CompareAndSwap(old, req.LastSeenID) {
			// Mirror into savedStates so the position survives a session deletion.
			sess.eventBufMu.RLock()
			bufCopy := make([]*WSServerMsg, len(sess.eventBuf))
			copy(bufCopy, sess.eventBuf)
			sess.eventBufMu.RUnlock()
			s.savedStates.Store(sess.token, savedState{
				eventBuf:   bufCopy,
				lastSeenID: req.LastSeenID,
			})
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
		if cm.Type == "command" && cm.Text != "" {
			// Check for client commands (starting with %)
			if strings.HasPrefix(cm.Text, "%") {
				// Execute client command
				commands.Execute(sess.conn.State(), cm.Text, func(lines []string) {
					msg := &WSServerMsg{
						Type: "commandresult",
						Data: CommandResultData{
							CmdID: 0, // Client commands use ID 0
							Lines: lines,
						},
					}
					sess.assignID(msg)
					select {
					case c.send <- msg:
					default:
					}
				})
				continue
			}

			// Forward to Lily server
			if err := sess.conn.Send(cm.Text); err != nil {
				log.Printf("lily send: %v", err)
				return
			}
		}
	}
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

// sessionToken derives a stable token from a username.
// TODO: replace with a cryptographically random token stored in a map.
func sessionToken(username string) string {
	return "zlily-" + username
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
		entities := make(map[string]EntityJSON)
		for handle := range handles {
			if entity := sess.conn.State().Get(handle); entity != nil {
				entities[handle] = entityToJSON(entity)
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

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
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
