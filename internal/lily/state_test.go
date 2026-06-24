package lily

import (
	"testing"

	"github.com/joshw/zephyrlily/internal/slcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyUser_UpsertAndIndexes(t *testing.T) {
	s := NewState()
	s.ApplyUser(&slcp.UserRecord{Handle: "#1", Name: "Alice", Blurb: "hi", State: "here", Pronoun: "they"})

	e := s.Get("#1")
	require.NotNil(t, e)
	assert.Equal(t, KindUser, e.Kind)
	assert.Equal(t, "Alice", e.Name)
	assert.Equal(t, "hi", e.Blurb)

	// Lookup by handle and by case-insensitive name reach the same entity.
	assert.Same(t, e, s.LookupHandle("#1"))
	assert.Same(t, e, s.LookupName("alice"))
	assert.Same(t, e, s.LookupName("ALICE"))

	// Re-applying updates in place rather than creating a duplicate.
	s.ApplyUser(&slcp.UserRecord{Handle: "#1", Name: "Alice", Blurb: "back", State: "away"})
	assert.Same(t, e, s.Get("#1"))
	assert.Equal(t, "back", s.Get("#1").Blurb)
	assert.Equal(t, "away", s.Get("#1").State)
}

func TestGetOrCreate_NameReindex(t *testing.T) {
	s := NewState()
	s.ApplyUser(&slcp.UserRecord{Handle: "#1", Name: "Alice"})
	// Apply a new name for the same handle; old name index must be dropped.
	s.ApplyUser(&slcp.UserRecord{Handle: "#1", Name: "Alicia"})

	assert.Nil(t, s.LookupName("alice"))
	require.NotNil(t, s.LookupName("alicia"))
	assert.Equal(t, "#1", s.LookupName("alicia").Handle)
}

func TestApplyDisc(t *testing.T) {
	s := NewState()
	s.ApplyDisc(&slcp.DiscRecord{Handle: "#5", Name: "cafe", Title: "The Cafe", Attrib: "public", Creation: 42})
	e := s.Get("#5")
	require.NotNil(t, e)
	assert.Equal(t, KindDisc, e.Kind)
	assert.Equal(t, "The Cafe", e.Title)
	assert.Equal(t, int64(42), e.Creation)
}

func TestApplyGroup(t *testing.T) {
	s := NewState()
	s.ApplyGroup(&slcp.GroupRecord{Name: "Friends", Members: []string{"#1", "#2"}})
	e := s.LookupName("friends")
	require.NotNil(t, e)
	assert.Equal(t, KindGroup, e.Kind)
	assert.Equal(t, []string{"#1", "#2"}, e.Members)

	// Re-apply replaces the member list.
	s.ApplyGroup(&slcp.GroupRecord{Name: "Friends", Members: []string{"#3"}})
	assert.Equal(t, []string{"#3"}, s.LookupName("friends").Members)
}

func TestApplyNotify_IdentityAndPresence(t *testing.T) {
	s := NewState()
	s.ApplyUser(&slcp.UserRecord{Handle: "#1", Name: "Alice", State: "here"})

	s.ApplyNotify(&slcp.NotifyEvent{Event: "blurb", Source: "#1", Value: "brb"})
	assert.Equal(t, "brb", s.Get("#1").Blurb)

	s.ApplyNotify(&slcp.NotifyEvent{Event: "away", Source: "#1"})
	assert.Equal(t, "away", s.Get("#1").State)

	s.ApplyNotify(&slcp.NotifyEvent{Event: "here", Source: "#1"})
	assert.Equal(t, "here", s.Get("#1").State)

	s.ApplyNotify(&slcp.NotifyEvent{Event: "disconnect", Source: "#1"})
	assert.Equal(t, "away", s.Get("#1").State)

	// rename reindexes the name maps.
	s.ApplyNotify(&slcp.NotifyEvent{Event: "rename", Source: "#1", Value: "Alicia"})
	assert.Equal(t, "Alicia", s.Get("#1").Name)
	assert.Nil(t, s.LookupName("alice"))
	assert.NotNil(t, s.LookupName("alicia"))
}

func TestApplyNotify_DiscEvents(t *testing.T) {
	s := NewState()
	s.ApplyDisc(&slcp.DiscRecord{Handle: "#5", Name: "cafe", Title: "Old"})

	s.ApplyNotify(&slcp.NotifyEvent{Event: "retitle", Source: "#5", Value: "New"})
	assert.Equal(t, "New", s.Get("#5").Title)

	// destroy: Source is the user who destroyed it, the disc is in Recips.
	// The destroyer's own mapping must survive.
	s.ApplyUser(&slcp.UserRecord{Handle: "#142", Name: "Garance"})
	s.JoinDisc("#5")
	s.ApplyNotify(&slcp.NotifyEvent{Event: "destroy", Source: "#142", Recips: []string{"#5"}})
	assert.Nil(t, s.Get("#5"))
	assert.Nil(t, s.LookupName("cafe"))
	assert.False(t, s.IsDiscMember("#5"))
	// The destroyer is untouched — handle→name mapping intact.
	assert.Equal(t, "Garance", s.Get("#142").Name)
	assert.Equal(t, "#142", s.LookupName("Garance").Handle)
}

func TestApplyNotify_MembershipOnlyForSelf(t *testing.T) {
	s := NewState()
	s.Whoami = "#1"

	// Self join marks membership.
	s.ApplyNotify(&slcp.NotifyEvent{Event: "join", Source: "#1", Recips: []string{"#5"}})
	assert.True(t, s.IsDiscMember("#5"))

	// Another user's join must not affect our membership.
	s.ApplyNotify(&slcp.NotifyEvent{Event: "join", Source: "#2", Recips: []string{"#9"}})
	assert.False(t, s.IsDiscMember("#9"))

	// Self quit clears membership.
	s.ApplyNotify(&slcp.NotifyEvent{Event: "quit", Source: "#1", Recips: []string{"#5"}})
	assert.False(t, s.IsDiscMember("#5"))
}

func TestJoinQuitDiscRoundTrip(t *testing.T) {
	s := NewState()
	assert.False(t, s.IsDiscMember("#5"))
	s.JoinDisc("#5")
	assert.True(t, s.IsDiscMember("#5"))
	s.QuitDisc("#5")
	assert.False(t, s.IsDiscMember("#5"))
}

func TestApplyWhereResponse(t *testing.T) {
	s := NewState()
	s.ApplyDisc(&slcp.DiscRecord{Handle: "#5", Name: "cafe"})
	s.ApplyDisc(&slcp.DiscRecord{Handle: "#6", Name: "lobby"})
	s.ApplyUser(&slcp.UserRecord{Handle: "#1", Name: "alice"})

	lines := []string{
		"%command [3] You are a member of cafe, lobby, ghost.",
	}
	s.ApplyWhereResponse(lines)

	assert.True(t, s.IsDiscMember("#5"))
	assert.True(t, s.IsDiscMember("#6"))
	// "ghost" is not a known disc, "alice" is a user — neither becomes a member.
	assert.False(t, s.IsDiscMember("#1"))
}

func TestApplyWhereResponse_StopsAtFirstMatch(t *testing.T) {
	s := NewState()
	s.ApplyDisc(&slcp.DiscRecord{Handle: "#5", Name: "cafe"})
	s.ApplyDisc(&slcp.DiscRecord{Handle: "#6", Name: "lobby"})

	lines := []string{
		"You are a member of cafe.",
		"You are a member of lobby.", // should be ignored after the first match
	}
	s.ApplyWhereResponse(lines)
	assert.True(t, s.IsDiscMember("#5"))
	assert.False(t, s.IsDiscMember("#6"))
}

func TestSetData(t *testing.T) {
	s := NewState()
	s.SetData("whoami", "#1")
	s.SetData("version", "2.0")
	s.SetData("name", "TestServer")
	s.SetData("events", "connect,disconnect,public")

	assert.Equal(t, "#1", s.Whoami)
	assert.Equal(t, "2.0", s.Version)
	assert.Equal(t, "TestServer", s.Name)
	assert.Equal(t, []string{"connect", "disconnect", "public"}, s.Events)
}

func TestAllEntities_ReturnsCopies(t *testing.T) {
	s := NewState()
	s.ApplyUser(&slcp.UserRecord{Handle: "#1", Name: "Alice", Blurb: "hi"})

	all := s.AllEntities()
	require.Len(t, all, 1)

	// Mutating the snapshot must not affect stored state.
	all[0].Blurb = "tampered"
	assert.Equal(t, "hi", s.Get("#1").Blurb)
}
