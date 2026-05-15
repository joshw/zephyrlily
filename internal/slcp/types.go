// Package slcp implements parsing and encoding for the Simple Lily Client Protocol.
package slcp

// MsgType identifies which kind of SLCP line was received.
type MsgType int

const (
	MsgRaw         MsgType = iota // plain text line (not SLCP-prefixed)
	MsgOptions                    // %options ...
	MsgConnected                  // %connected
	MsgPrompt                     // %prompt <text>
	MsgNotify                     // %NOTIFY EVENT=...
	MsgUser                       // %USER HANDLE=... NAME=...
	MsgDisc                       // %DISC HANDLE=... NAME=...
	MsgGroup                      // %GROUP NAME=... MEMBERS=...
	MsgData                       // %DATA NAME=... VALUE=...
	MsgServer                     // %server version=...
	MsgSyncBegin                  // %SLCP-SYNC beginning
	MsgSyncEnd                    // %SLCP-SYNC ending
	MsgCmdBegin                   // %begin [id] <cmd>
	MsgCmdEnd                     // %end [id]
	MsgLoginPrompt                // "login: " prompt
	MsgPassPrompt                 // "password: " prompt
	MsgExportFile                 // %export_file OKAY|ERROR
	MsgPong                       // %pong
)

// Message is a fully-parsed SLCP line.
type Message struct {
	Type   MsgType
	Raw    string            // original line text
	Params map[string]string // parsed key=value pairs
	// Fields populated for specific message types:
	CmdID int    // MsgCmdBegin / MsgCmdEnd
	Text  string // MsgRaw, MsgPrompt, MsgCmdBegin label
}

// UserRecord holds state for a connected user.
type UserRecord struct {
	Handle  string // e.g. "#123"
	Name    string // display name
	Blurb   string
	State   string // "here" or "away"
	Pronoun string
}

// DiscRecord holds state for a discussion.
type DiscRecord struct {
	Handle   string
	Name     string // lowercase internal name
	Title    string
	Attrib   string
	Creation int64 // unix timestamp
}

// GroupRecord holds a named group and its member handles.
type GroupRecord struct {
	Name    string
	Members []string // list of handles
}

// NotifyEvent is a parsed %NOTIFY message.
type NotifyEvent struct {
	Event   string
	Source  string // handle of source user
	Time    int64
	Value   string
	Recips  []string // recipient handles
	Targets []string // target handles (for permission ops)
	SubEvt  string
	Empty   bool // VALUE field was explicitly empty
	Notify  bool // NOTIFY=1 was present — event should be displayed to the user
	Stamp   bool // STAMP=1 was present — event timestamp should be displayed
}
