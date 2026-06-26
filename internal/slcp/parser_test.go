package slcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Dispatch(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantType MsgType
		wantText string // empty means not checked / expected empty
	}{
		{name: "login prompt", line: "login:", wantType: MsgLoginPrompt},
		{name: "password prompt", line: "password:", wantType: MsgPassPrompt},
		{name: "connected", line: "%connected #123", wantType: MsgConnected},
		{name: "pong", line: "%pong", wantType: MsgPong},
		{name: "sync begin", line: "%SLCP-SYNC START", wantType: MsgSyncBegin},
		{name: "sync end", line: "%SLCP-SYNC END", wantType: MsgSyncEnd},
		// Delimiter token is matched tolerantly, so the older beginning/ending
		// wording still classifies correctly rather than falling through to raw.
		{name: "sync begin legacy", line: "%SLCP-SYNC beginning", wantType: MsgSyncBegin},
		{name: "sync end legacy", line: "%SLCP-SYNC ending", wantType: MsgSyncEnd},
		{name: "prompt", line: "%prompt -> ", wantType: MsgPrompt, wantText: "-> "},
		{name: "options", line: "%options +prompt +connected", wantType: MsgOptions, wantText: "+prompt +connected"},
		{name: "export_file", line: "%export_file OKAY", wantType: MsgExportFile, wantText: "OKAY"},
		{name: "raw fallback", line: "hello world", wantType: MsgRaw, wantText: "hello world"},
		{name: "empty line is raw", line: "", wantType: MsgRaw, wantText: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := Parse(tt.line)
			require.NoError(t, err)
			require.NotNil(t, msg)
			assert.Equal(t, tt.wantType, msg.Type)
			assert.Equal(t, tt.line, msg.Raw)
			if tt.wantText != "" {
				assert.Equal(t, tt.wantText, msg.Text)
			}
		})
	}
}

func TestParse_RoutesToParseParams(t *testing.T) {
	tests := []struct {
		line     string
		wantType MsgType
	}{
		{"%NOTIFY EVENT=connect", MsgNotify},
		{"%USER HANDLE=#1", MsgUser},
		{"%DISC HANDLE=#2", MsgDisc},
		{"%GROUP NAME=friends", MsgGroup},
		{"%DATA NAME=whoami VALUE=#1", MsgData},
		{"%server version=2.0", MsgServer},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			msg, err := Parse(tt.line)
			require.NoError(t, err)
			assert.Equal(t, tt.wantType, msg.Type)
			assert.NotNil(t, msg.Params)
		})
	}
}

func TestParse_CmdBound(t *testing.T) {
	t.Run("begin with label", func(t *testing.T) {
		msg, err := Parse("%begin [42] /who")
		require.NoError(t, err)
		assert.Equal(t, MsgCmdBegin, msg.Type)
		assert.Equal(t, 42, msg.CmdID)
		assert.Equal(t, "/who", msg.Text)
	})

	t.Run("end without label", func(t *testing.T) {
		msg, err := Parse("%end [42]")
		require.NoError(t, err)
		assert.Equal(t, MsgCmdEnd, msg.Type)
		assert.Equal(t, 42, msg.CmdID)
		assert.Equal(t, "", msg.Text)
	})

	t.Run("non-numeric id is an error", func(t *testing.T) {
		// "%begin [" prefix matches, but the id "x" fails to parse.
		_, err := Parse("%begin [x] foo")
		require.Error(t, err)
	})
}

func TestParseCmdBound_MissingBracket(t *testing.T) {
	// parseCmdBound is reachable directly to exercise the missing-"]" path,
	// which Parse's prefix guard otherwise hides.
	_, err := parseCmdBound("%begin [42 oops", MsgCmdBegin)
	require.Error(t, err)
}

func TestParseParams_SimpleValue(t *testing.T) {
	msg, err := parseParams("HANDLE=#123 NAME=alice", MsgUser)
	require.NoError(t, err)
	assert.Equal(t, "#123", msg.Params["HANDLE"])
	assert.Equal(t, "alice", msg.Params["NAME"])
}

func TestParseParams_LengthPrefixed(t *testing.T) {
	// BLURB has length 11 and its data contains spaces.
	msg, err := parseParams("HANDLE=#1 BLURB=11=hello world NAME=bob", MsgUser)
	require.NoError(t, err)
	assert.Equal(t, "#1", msg.Params["HANDLE"])
	assert.Equal(t, "hello world", msg.Params["BLURB"])
	assert.Equal(t, "bob", msg.Params["NAME"])
}

func TestParseParams_BooleanFlag(t *testing.T) {
	msg, err := parseParams("EVENT=public EMPTY NOTIFY", MsgNotify)
	require.NoError(t, err)
	assert.Equal(t, "public", msg.Params["EVENT"])
	assert.Equal(t, "1", msg.Params["EMPTY"])
	assert.Equal(t, "1", msg.Params["NOTIFY"])
}

func TestParseParams_Errors(t *testing.T) {
	t.Run("truncated length-prefixed data", func(t *testing.T) {
		_, err := parseParams("BLURB=20=too short", MsgUser)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "truncated")
	})
}

func TestParseNotify(t *testing.T) {
	t.Run("flags and lowercasing", func(t *testing.T) {
		m, err := parseParams("EVENT=CONNECT SOURCE=#1 STAMP TIME=1700000000", MsgNotify)
		require.NoError(t, err)
		ev, err := ParseNotify(m)
		require.NoError(t, err)
		assert.Equal(t, "connect", ev.Event)
		assert.Equal(t, "#1", ev.Source)
		assert.True(t, ev.Stamp)
		assert.Equal(t, int64(1700000000), ev.Time)
	})

	t.Run("message events force notify on", func(t *testing.T) {
		m, err := parseParams("EVENT=emote SOURCE=#1 VALUE=5=waves", MsgNotify)
		require.NoError(t, err)
		ev, err := ParseNotify(m)
		require.NoError(t, err)
		assert.True(t, ev.Notify, "emote should force Notify on even without NOTIFY flag")
		assert.Equal(t, "waves", ev.Value)
	})

	t.Run("recips and targets split on comma", func(t *testing.T) {
		m, err := parseParams("EVENT=permit SOURCE=#1 RECIPS=#2,#3 TARGETS=#4", MsgNotify)
		require.NoError(t, err)
		ev, err := ParseNotify(m)
		require.NoError(t, err)
		assert.Equal(t, []string{"#2", "#3"}, ev.Recips)
		assert.Equal(t, []string{"#4"}, ev.Targets)
	})

	t.Run("empty recips yield nil", func(t *testing.T) {
		m, err := parseParams("EVENT=connect SOURCE=#1", MsgNotify)
		require.NoError(t, err)
		ev, err := ParseNotify(m)
		require.NoError(t, err)
		assert.Nil(t, ev.Recips)
		assert.Nil(t, ev.Targets)
	})

	t.Run("wrong type guard", func(t *testing.T) {
		_, err := ParseNotify(&Message{Type: MsgUser})
		require.Error(t, err)
	})
}

func TestParseUser(t *testing.T) {
	m, err := parseParams("HANDLE=#1 NAME=alice BLURB=2=hi STATE=here PRONOUN=they", MsgUser)
	require.NoError(t, err)
	u, err := ParseUser(m)
	require.NoError(t, err)
	assert.Equal(t, "#1", u.Handle)
	assert.Equal(t, "alice", u.Name)
	assert.Equal(t, "hi", u.Blurb)
	assert.Equal(t, "here", u.State)
	assert.Equal(t, "they", u.Pronoun)

	_, err = ParseUser(&Message{Type: MsgDisc})
	require.Error(t, err)
}

func TestParseDisc(t *testing.T) {
	m, err := parseParams("HANDLE=#9 NAME=cafe TITLE=5=Hello CREATION=1700000000", MsgDisc)
	require.NoError(t, err)
	d, err := ParseDisc(m)
	require.NoError(t, err)
	assert.Equal(t, "#9", d.Handle)
	assert.Equal(t, "cafe", d.Name)
	assert.Equal(t, "Hello", d.Title)
	assert.Equal(t, int64(1700000000), d.Creation)

	_, err = ParseDisc(&Message{Type: MsgUser})
	require.Error(t, err)
}

func TestParseGroup(t *testing.T) {
	m, err := parseParams("NAME=friends MEMBERS=#1,#2,#3", MsgGroup)
	require.NoError(t, err)
	g, err := ParseGroup(m)
	require.NoError(t, err)
	assert.Equal(t, "friends", g.Name)
	assert.Equal(t, []string{"#1", "#2", "#3"}, g.Members)

	_, err = ParseGroup(&Message{Type: MsgUser})
	require.Error(t, err)
}
