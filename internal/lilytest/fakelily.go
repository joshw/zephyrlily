// Package lilytest provides a scriptable fake Lily (SLCP) TCP server for
// integration tests. It speaks just enough of the protocol to drive a real
// lily.Conn (and therefore the proxy and TUI) through a full handshake, sync,
// live events, and command round-trips.
//
// It deliberately does not import internal/lily — it only reads and writes raw
// SLCP lines over a net.Conn.
package lilytest

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Handles and names used by DefaultWorld, exported so tests can reference them.
const (
	HandleMe    = "#100"
	HandleAlice = "#1"
	HandleBob   = "#2"
	HandleCarol = "#3"
	HandleCafe  = "#5"
	HandleLobby = "#6"
	HandleSand  = "#7"
)

// Options configures a fake Lily server.
type Options struct {
	Whoami         string              // connected user's handle (default "#100")
	Setup          []string            // %USER/%DISC/%GROUP/%DATA lines sent inside the sync block
	WhereResponse  string              // e.g. "You are a member of cafe, lobby." (seeds disc membership)
	OmitOptions    []string            // options to drop from the advertised %options line (failure tests)
	RejectLogin    bool                // re-prompt with "login:" instead of %options, simulating bad credentials
	RedirectLogin  bool                // user already logged in elsewhere: redirect banner + blurb prompt instead of "*** Connected ***"
	CommandReplies map[string][]string // client command line -> reply lines (sent between %begin/%end)
}

// DefaultWorld returns Options pre-populated with a realistic, named entity set:
// the connected user "me", three other users, and three discussions (member of
// two of them). It lets tests drive public, private, and emote events with real
// name resolution.
func DefaultWorld() Options {
	return Options{
		Whoami: HandleMe,
		Setup: []string{
			"%USER HANDLE=" + HandleMe + " NAME=me STATE=here",
			"%USER HANDLE=" + HandleAlice + " NAME=alice STATE=here",
			"%USER HANDLE=" + HandleBob + " NAME=bob STATE=away BLURB=4=busy",
			"%USER HANDLE=" + HandleCarol + " NAME=carol STATE=here PRONOUN=they",
			"%DISC HANDLE=" + HandleCafe + " NAME=cafe TITLE=8=The Cafe CREATION=1700000000",
			"%DISC HANDLE=" + HandleLobby + " NAME=lobby TITLE=5=Lobby CREATION=1700000001",
			"%DISC HANDLE=" + HandleSand + " NAME=sandbox TITLE=7=Sandbox CREATION=1700000002",
		},
		WhereResponse: "You are a member of cafe, lobby.",
		CommandReplies: map[string][]string{
			"/who": {"Users here:", "  alice", "  bob", "  carol"},
			// The client fetches this memo on startup (zlilyStartup replay). Model
			// a real server answering it so the proxy's in-flight fetch completes
			// and clears, rather than staying armed and swallowing the next leafed
			// command's output (e.g. /who).
			"/memo me zlilyStartup": {},
		},
	}
}

// NotifyLine builds a %NOTIFY line with a length-prefixed VALUE so it may contain
// spaces. recips may be nil.
func NotifyLine(event, source string, recips []string, value string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%%NOTIFY EVENT=%s SOURCE=%s", event, source)
	if len(recips) > 0 {
		fmt.Fprintf(&b, " RECIPS=%s", strings.Join(recips, ","))
	}
	fmt.Fprintf(&b, " VALUE=%d=%s NOTIFY", len(value), value)
	return b.String()
}

// Server is a running fake Lily server bound to a loopback port.
type Server struct {
	ln  net.Listener
	opt Options

	mu     sync.Mutex // guards conn writes and nextID
	conn   net.Conn
	nextID int

	// Commands receives each client line read after the handshake completes.
	Commands chan string

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// Start binds a fake Lily server on 127.0.0.1:0, accepts a single connection in
// the background, and runs the handshake + sync. It registers cleanup via
// t.Cleanup so the goroutine is always joined.
func Start(t testing.TB, opt Options) *Server {
	t.Helper()
	if opt.Whoami == "" {
		opt.Whoami = HandleMe
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &Server{ln: ln, opt: opt, nextID: 2, Commands: make(chan string, 64)}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.Close)
	return s
}

// Addr returns the host:port to use as a lily address.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Push writes raw SLCP lines to the connected client (e.g. async %NOTIFY events).
func (s *Server) Push(lines ...string) {
	for _, l := range lines {
		s.write(l)
	}
}

// WaitCommand blocks until the client sends a line equal to want, or fails the
// test after a short timeout.
func (s *Server) WaitCommand(t testing.TB, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-s.Commands:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for client command %q", want)
		}
	}
}

// Close shuts the server down and joins its goroutine.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		_ = s.ln.Close()
		s.mu.Lock()
		if s.conn != nil {
			_ = s.conn.Close()
		}
		s.mu.Unlock()
	})
	s.wg.Wait()
}

func (s *Server) serve() {
	defer s.wg.Done()

	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	r := bufio.NewReader(conn)
	if err := s.handshake(r); err != nil {
		return
	}

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		select {
		case s.Commands <- line:
		default:
		}
		if reply, ok := s.opt.CommandReplies[line]; ok {
			s.sendCommand(line, reply)
		}
	}
}

// handshake performs the server side of the SLCP login + sync sequence.
func (s *Server) handshake(r *bufio.Reader) error {
	s.write("Welcome to lily at TestServer")
	s.write("login:") // sent with a trailing newline so the client returns immediately

	// Client replies with its "#$# options ..." line and its "user pass" line.
	if _, err := r.ReadString('\n'); err != nil {
		return err
	}
	if _, err := r.ReadString('\n'); err != nil {
		return err
	}

	s.write("%server version=1.0 name=TestServer")

	// %options is the server's reply to the client's "#$# options" negotiation
	// and is sent regardless of whether the credentials are valid.
	s.write("%options " + s.optionsLine())

	if s.opt.RejectLogin {
		// Simulate rejected credentials: a real Lily server prints an error and
		// re-prompts for login (the prompt itself is not newline-terminated).
		s.write("")
		s.write("Login in the wrong.")
		s.write("")
		s.write("login:")
		return nil
	}

	if s.opt.RedirectLogin {
		// The user was already logged in from another client: the server takes
		// over that session, printing a redirect banner in place of "*** Connected
		// ***", then waits at the blurb prompt before syncing (real wire capture,
		// lily 2.6.2). The client must accept the banner as login success or it
		// hangs here: the blurb answer can only come from a user who is stuck in
		// the auth dialog until Connect returns.
		s.write("*** Redirecting old connection to this port ***")
		s.write("%NOTIFY SOURCE=#1660 EVENT=detach TIME=1784247402 NOTIFY VALUE=22=(possibly by accident)")
		s.write("")
		s.write("Please enter a blurb, or hit <enter> for none")
		s.write("%prompt --> ")
		if _, err := r.ReadString('\n'); err != nil { // the blurb answer
			return err
		}
	} else {
		// "*** Connected ***" is printed as soon as login succeeds, before the sync
		// block — the client uses it as the login-success signal.
		s.write("*** Connected ***")
	}

	s.write("%SLCP-SYNC START")
	s.write("%DATA NAME=whoami VALUE=" + s.opt.Whoami)
	for _, l := range s.opt.Setup {
		s.write(l)
	}
	s.write("%SLCP-SYNC END")
	s.write("%connected " + s.opt.Whoami)

	// On %connected the client sends "#$# client zlily <ver>" then "/where me".
	// Read until we see /where, answering it as a leafed command so the client's
	// interceptor seeds membership and closes SyncComplete.
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.Contains(line, "/where") {
			s.write("%begin [1] /where me")
			if s.opt.WhereResponse != "" {
				s.write(s.opt.WhereResponse)
			}
			s.write("%end [1]")
			return nil
		}
	}
}

// optionsLine returns the advertised options minus any in OmitOptions.
func (s *Server) optionsLine() string {
	all := []string{"+version", "+prompt", "+prompt2", "+leaf-notify", "+leaf-cmd", "+connected"}
	omit := make(map[string]bool, len(s.opt.OmitOptions))
	for _, o := range s.opt.OmitOptions {
		omit[o] = true
	}
	var keep []string
	for _, o := range all {
		if !omit[o] {
			keep = append(keep, o)
		}
	}
	return strings.Join(keep, " ")
}

// sendCommand emits a leafed command response: %begin [id] <cmd>, the raw reply
// lines, then %end [id].
func (s *Server) sendCommand(cmd string, lines []string) {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.mu.Unlock()

	s.write(fmt.Sprintf("%%begin [%d] %s", id, cmd))
	for _, l := range lines {
		s.write(l)
	}
	s.write(fmt.Sprintf("%%end [%d]", id))
}

func (s *Server) write(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return
	}
	_, _ = fmt.Fprintf(s.conn, "%s\n", line)
}
