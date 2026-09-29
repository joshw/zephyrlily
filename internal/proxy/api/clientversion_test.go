package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/version"
	"github.com/stretchr/testify/require"
)

func TestClientIdent(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"ui=tui-0.19.1", "ui:tui-0.19.1"},
		{"ui=tui-web-dev%2Babc123.dirty", "ui:tui-web-dev+abc123.dirty"},
		{"bot=weatherbot-1.2", "bot:weatherbot-1.2"},
		{"", "ui:unknown"},
		// Nothing that could close the quote or the line reaches Lily.
		{"ui=evil%22%0A%2Fshout+hi", "ui:evilshouthi"},
		{"ui=%22%22", "ui:unknown"},
	}
	for _, tc := range cases {
		q, err := url.ParseQuery(tc.query)
		require.NoError(t, err)
		require.Equal(t, tc.want, clientIdent(q), "query %q", tc.query)
	}
}

func TestFormatClientVersion(t *testing.T) {
	p := "proxy:" + version.String()
	require.Equal(t, p, formatClientVersion(nil))
	require.Equal(t,
		p+"  ui:tui-0.19.1  ui:tui-web-0.19.1  bot:a-1  bot:b-2",
		formatClientVersion([]string{"bot:b-2", "ui:tui-web-0.19.1", "ui:tui-0.19.1", "bot:a-1", "ui:tui-0.19.1"}))
}

// Lily is told about every client on the session, and told again as they come
// and go.
func TestVersionReportFollowsConnectedClients(t *testing.T) {
	old := clientGoneGrace
	clientGoneGrace = 10 * time.Millisecond
	t.Cleanup(func() { clientGoneGrace = old })

	fake := lilytest.Start(t, lilytest.DefaultWorld())
	addr := startTestProxy(t, fake)
	token := authTestSession(t, addr)

	p := "proxy:" + version.String()
	want := func(v string) string { return `#$# client zlily "` + v + `"` }

	ui := dialTestWS(t, addr, url.Values{"token": {token}, "ui": {"tui-1.0"}})
	fake.WaitCommand(t, want(p+"  ui:tui-1.0"))

	bot := dialTestWS(t, addr, url.Values{"token": {token}, "bot": {"echobot-2.0"}})
	fake.WaitCommand(t, want(p+"  ui:tui-1.0  bot:echobot-2.0"))

	_ = bot.Close(websocket.StatusNormalClosure, "")
	fake.WaitCommand(t, want(p+"  ui:tui-1.0"))

	_ = ui.Close(websocket.StatusNormalClosure, "")
	fake.WaitCommand(t, want(p))
}

func startTestProxy(t *testing.T, fake *lilytest.Server) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := New(Config{LilyAddr: fake.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.RunWithListener(ctx, l); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("proxy did not shut down in time")
		}
	})
	return l.Addr().String()
}

func authTestSession(t *testing.T, addr string) string {
	t.Helper()
	body, _ := json.Marshal(AuthRequest{Username: "alice", Password: "password"})
	resp, err := http.Post("http://"+addr+"/auth", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ar AuthResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ar))
	return ar.Token
}

func dialTestWS(t *testing.T, addr string, q url.Values) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.Dial(context.Background(), "ws://"+addr+"/ws?"+q.Encode(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws
}
