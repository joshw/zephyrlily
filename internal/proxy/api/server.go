package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/joshw/zephyrlily/internal/lily"
	"github.com/joshw/zephyrlily/internal/proxy/commands"
	"github.com/joshw/zephyrlily/internal/slcp"
)

// Config holds proxy server configuration.
type Config struct {
	ListenAddr string // e.g. ":7888"
	LilyAddr   string // e.g. "rpi.lily.org:7777"
}

// Server is the proxy HTTP/WebSocket server.
type Server struct {
	cfg      Config
	sessions sync.Map // token -> *Session
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
	resp := StateResponse{
		Whoami:   st.Whoami,
		Version:  st.Version,
		Server:   st.Name,
		Entities: make([]EntityJSON, 0, len(entities)),
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
	for msg := range sess.conn.Events {
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
	s.sessions.Delete(sess.token)
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
