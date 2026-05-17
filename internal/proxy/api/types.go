// Package api implements the HTTP and WebSocket interface served by zlily-proxy.
package api

// AuthRequest is the body for POST /auth.
type AuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResponse is returned on successful authentication.
type AuthResponse struct {
	Token string `json:"token"`
}

// StateResponse is returned by GET /state.
type StateResponse struct {
	Whoami      string       `json:"whoami"`
	Version     string       `json:"version"`
	Server      string       `json:"server"`
	Entities    []EntityJSON `json:"entities"`
	LastSeenID  int64        `json:"last_seen_id"`
	EventBufSize int         `json:"event_buf_size"`
}

// EventsResponse is returned by GET /events.
type EventsResponse struct {
	Events []WSServerMsg `json:"events"`
	More   bool          `json:"more"`
}

// SeenRequest is the body for POST /seen.
type SeenRequest struct {
	LastSeenID int64 `json:"last_seen_id"`
}

// EntityJSON is the wire representation of a user, discussion, or group.
type EntityJSON struct {
	Handle   string   `json:"handle"`
	Kind     string   `json:"kind"` // "user", "disc", "group"
	Name     string   `json:"name"`
	Blurb    string   `json:"blurb,omitempty"`
	State    string   `json:"state,omitempty"`
	Pronoun  string   `json:"pronoun,omitempty"`
	Title    string   `json:"title,omitempty"`
	Attrib   string   `json:"attrib,omitempty"`
	Creation int64    `json:"creation,omitempty"`
	Members  []string `json:"members,omitempty"`
}

// WSClientMsg is a message sent from a thin client to the proxy over WebSocket.
type WSClientMsg struct {
	Type string `json:"type"` // "command"
	Text string `json:"text"` // raw command text to forward to Lily
}

// WSServerMsg is a message pushed from the proxy to a thin client over WebSocket.
type WSServerMsg struct {
	ID   int64       `json:"id"`   // unique message ID, increments per session
	Type string      `json:"type"` // "event", "text", "commandresult", "prompt", "error"
	Data interface{} `json:"data"`
}

// EventData carries a structured event notification.
type EventData struct {
	Event    string                `json:"event"`
	Source   string                `json:"source"`
	Time     int64                 `json:"time"`
	Value    string                `json:"value,omitempty"`
	Recips   []string              `json:"recips,omitempty"`
	Targets  []string              `json:"targets,omitempty"`
	SubEvt   string                `json:"sub_evt,omitempty"`
	Notify   bool                  `json:"notify"`
	Stamp    bool                  `json:"stamp,omitempty"`
	Entities map[string]EntityJSON `json:"entities,omitempty"`
}

// TextData carries a single line of unformatted text from the server.
type TextData struct {
	Text string `json:"text"`
}

// CommandResultData carries the complete output from a command.
type CommandResultData struct {
	CmdID int      `json:"cmd_id"`
	Lines []string `json:"lines"`
}

// ExpandResponse is returned by GET /expand.
// Matches contains exact matches first; if none, prefix matches.
type ExpandResponse struct {
	Matches []EntityJSON `json:"matches"`
}
