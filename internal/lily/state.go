// Package lily manages per-user connections to the Lily server.
package lily

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/joshw/zephyrlily/internal/slcp"
)

// State holds the Lily server state for a single connected user.
type State struct {
	mu sync.RWMutex

	// Server metadata
	Whoami  string // our handle, e.g. "#850"
	Version string
	Events  []string // events the server supports
	Name    string   // server name from welcome banner

	// Entity databases
	byHandle map[string]*Entity // keyed by handle "#123"
	byName   map[string]*Entity // keyed by lowercase name

	// Discussions the current user is a member of (keyed by handle).
	// Seeded from %DISC ATTRIB during sync; updated by join/quit events.
	discMembership map[string]bool
}

// EntityKind distinguishes users from discussions.
type EntityKind int

const (
	KindUser EntityKind = iota
	KindDisc
	KindGroup
)

// Entity is a unified record for users, discussions, and groups.
type Entity struct {
	Kind     EntityKind
	Handle   string
	Name     string
	Blurb    string   // user blurb / status
	State    string   // "here" or "away" (users)
	Pronoun  string   // users
	Title    string   // discussions
	Attrib   string   // discussions
	Creation int64    // discussions (unix ts)
	Members  []string // groups (list of handles)
}

// NewState returns an empty State.
func NewState() *State {
	return &State{
		byHandle:       make(map[string]*Entity),
		byName:         make(map[string]*Entity),
		discMembership: make(map[string]bool),
	}
}

// Get retrieves an entity by handle. Returns nil if not found.
func (s *State) Get(handle string) *Entity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byHandle[handle]
}

// ApplyUser upserts a user entity from a parsed UserRecord.
func (s *State) ApplyUser(u *slcp.UserRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.getOrCreate(u.Handle, u.Name, KindUser)
	e.Name = u.Name
	e.Blurb = u.Blurb
	e.State = u.State
	e.Pronoun = u.Pronoun
}

// ApplyDisc upserts a discussion entity from a parsed DiscRecord.
// Disc membership is tracked separately via ApplyWhereResponse and
// the join/quit cases in ApplyNotify.
func (s *State) ApplyDisc(d *slcp.DiscRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.getOrCreate(d.Handle, d.Name, KindDisc)
	e.Name = d.Name
	e.Title = d.Title
	e.Attrib = d.Attrib
	e.Creation = d.Creation
}

// ApplyWhereResponse parses the output lines of a "/where me" command and
// marks every named discussion as a member disc. Expected format:
//
//	You are a member of disc1, disc2, disc3
func (s *State) ApplyWhereResponse(lines []string) {
	const prefix = "You are a member of "
	for _, line := range lines {
		// Strip the "%command [id] " prefix that +leaf-cmd adds to output lines.
		if _, rest, ok := slcp.SplitCommandPrefix(line); ok {
			line = rest
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimSuffix(strings.TrimPrefix(line, prefix), ".")
		slog.Debug("lily: where response", "names", rest)
		for _, name := range strings.Split(rest, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			s.mu.Lock()
			e := s.byName[strings.ToLower(name)]
			if e != nil && e.Kind == KindDisc {
				s.discMembership[e.Handle] = true
				slog.Debug("lily: disc membership set", "name", name, "handle", e.Handle)
			} else {
				slog.Debug("lily: disc membership miss", "name", name, "found", e != nil)
			}
			s.mu.Unlock()
		}
		break
	}
}

// JoinDisc marks the current user as a member of the discussion with the
// given handle. Called when the user's own join event is observed.
func (s *State) JoinDisc(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discMembership[handle] = true
}

// QuitDisc removes the current user's membership for the given discussion.
func (s *State) QuitDisc(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.discMembership, handle)
}

// IsDiscMember reports whether the current user is a member of the discussion
// identified by handle.
func (s *State) IsDiscMember(handle string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.discMembership[handle]
}

// ApplyGroup upserts a group entity from a parsed GroupRecord.
func (s *State) ApplyGroup(g *slcp.GroupRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(g.Name)
	e, ok := s.byName[key]
	if !ok {
		e = &Entity{Kind: KindGroup, Name: g.Name}
		s.byName[key] = e
	}
	e.Members = g.Members
}

// ApplyNotify updates state in response to a real-time event.
func (s *State) ApplyNotify(ev *slcp.NotifyEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch ev.Event {
	case "rename":
		if e := s.byHandle[ev.Source]; e != nil {
			delete(s.byName, strings.ToLower(e.Name))
			e.Name = ev.Value
			s.byName[strings.ToLower(ev.Value)] = e
		}
	case "blurb":
		if e := s.byHandle[ev.Source]; e != nil {
			e.Blurb = ev.Value
		}
	case "here":
		if e := s.byHandle[ev.Source]; e != nil {
			e.State = "here"
		}
	case "away":
		if e := s.byHandle[ev.Source]; e != nil {
			e.State = "away"
		}
	case "disconnect":
		// user logged out — mark absent but keep record
		if e := s.byHandle[ev.Source]; e != nil {
			e.State = "away"
		}
	case "destroy":
		// A discussion was destroyed. ev.Source is the user who destroyed it;
		// the discussion(s) being removed are in ev.Recips. Drop only the
		// discussion entities — deleting the source would wipe the destroyer's
		// handle→name mapping.
		for _, h := range ev.Recips {
			if e := s.byHandle[h]; e != nil && e.Kind == KindDisc {
				delete(s.byName, strings.ToLower(e.Name))
				delete(s.byHandle, e.Handle)
				delete(s.discMembership, e.Handle)
			}
		}
	case "retitle":
		if e := s.byHandle[ev.Source]; e != nil {
			e.Title = ev.Value
		}
	case "join":
		// Track when the current user joins a discussion.
		if ev.Source == s.Whoami {
			for _, h := range ev.Recips {
				s.discMembership[h] = true
			}
		}
	case "quit":
		// Track when the current user leaves a discussion.
		if ev.Source == s.Whoami {
			for _, h := range ev.Recips {
				delete(s.discMembership, h)
			}
		}
	}
}

// SetData stores a %DATA NAME=... VALUE=... field.
func (s *State) SetData(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch strings.ToLower(name) {
	case "whoami":
		s.Whoami = value
	case "version":
		s.Version = value
	case "name":
		s.Name = value
	case "events":
		s.Events = strings.Split(value, ",")
	}
}

// LookupHandle returns the entity with the given handle, or nil.
func (s *State) LookupHandle(handle string) *Entity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byHandle[handle]
}

// LookupName returns the entity with the given name (case-insensitive), or nil.
func (s *State) LookupName(name string) *Entity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byName[strings.ToLower(name)]
}

// AllEntities returns a snapshot of all entities.
func (s *State) AllEntities() []*Entity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Entity, 0, len(s.byHandle))
	for _, e := range s.byHandle {
		cp := *e
		out = append(out, &cp)
	}
	return out
}

// getOrCreate finds or creates an entity by handle, registering it under the name index.
// Caller must hold s.mu.
func (s *State) getOrCreate(handle, name string, kind EntityKind) *Entity {
	e, ok := s.byHandle[handle]
	if !ok {
		e = &Entity{Kind: kind, Handle: handle}
		s.byHandle[handle] = e
	}
	// update name index if name changed
	if e.Name != name {
		if e.Name != "" {
			delete(s.byName, strings.ToLower(e.Name))
		}
		s.byName[strings.ToLower(name)] = e
	}
	return e
}
