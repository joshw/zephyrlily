package api

import (
	"testing"

	"github.com/joshw/zephyrlily/internal/slcp"
	"github.com/stretchr/testify/assert"
)

func TestFormatEventText(t *testing.T) {
	entities := map[string]EntityJSON{
		"#1": {Handle: "#1", Kind: "user", Name: "Alice", Blurb: "busy"},
		"#2": {Handle: "#2", Kind: "user", Name: "Bob"},
		"#5": {Handle: "#5", Kind: "disc", Name: "cafe"},
	}

	tests := []struct {
		name   string
		ev     *slcp.NotifyEvent
		whoami string
		want   string
	}{
		{
			name: "connect includes blurb",
			ev:   &slcp.NotifyEvent{Event: "connect", Source: "#1"},
			want: "*** Alice [busy] has entered lily ***",
		},
		{
			name: "disconnect with reason",
			ev:   &slcp.NotifyEvent{Event: "disconnect", Source: "#2", Value: "timeout"},
			want: "*** Bob has left lily (timeout) ***",
		},
		{
			name:   "rename other user",
			ev:     &slcp.NotifyEvent{Event: "rename", Source: "#1", Value: "Alicia"},
			whoami: "#2",
			want:   "*** Alice is now named Alicia ***",
		},
		{
			name:   "rename self uses parenthetical",
			ev:     &slcp.NotifyEvent{Event: "rename", Source: "#1", Value: "Alicia"},
			whoami: "#1",
			want:   "(you are now named Alicia)",
		},
		{
			name: "blurb cleared by other",
			ev:   &slcp.NotifyEvent{Event: "blurb", Source: "#2", Value: ""},
			want: "*** Bob has turned their blurb off ***",
		},
		{
			name: "public message summary with recip name resolved",
			ev:   &slcp.NotifyEvent{Event: "public", Source: "#1", Recips: []string{"#5"}, Value: "hello"},
			want: "From Alice [busy] to cafe: hello",
		},
		{
			name: "private message summary",
			ev:   &slcp.NotifyEvent{Event: "private", Source: "#2", Value: "psst"},
			want: "Private from Bob: psst",
		},
		{
			name: "emote",
			ev:   &slcp.NotifyEvent{Event: "emote", Source: "#1", Value: " waves"},
			want: "Alice waves",
		},
		{
			name:   "permit grants privileges to me",
			ev:     &slcp.NotifyEvent{Event: "permit", Source: "#1", Recips: []string{"#5"}, Targets: []string{"#2"}, SubEvt: "speaker"},
			whoami: "#2",
			want:   "*** Alice has given you speaker privileges to discussion cafe ***",
		},
		{
			name:   "appoint owner offered to me",
			ev:     &slcp.NotifyEvent{Event: "appoint", Source: "#1", Recips: []string{"#5"}, Targets: []string{"#2"}, SubEvt: "owner"},
			whoami: "#2",
			want:   "*** Alice has offered you ownership of discussion cafe ***",
		},
		{
			name: "unknown event falls through to generic format",
			ev:   &slcp.NotifyEvent{Event: "wibble", Source: "#1", Value: "x"},
			want: "[wibble] #1 x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatEventText(tt.ev, entities, tt.whoami)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatEventText_UnknownHandleFallsBackToHandle(t *testing.T) {
	// With no entity for the source, its handle is used verbatim.
	got := formatEventText(&slcp.NotifyEvent{Event: "connect", Source: "#99"}, map[string]EntityJSON{}, "")
	assert.Equal(t, "*** #99 has entered lily ***", got)
}
