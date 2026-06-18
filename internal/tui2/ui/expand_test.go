package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDestination(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"alice;hello there", "alice"},
		{"alice, bob: hey", "alice, bob"},
		{"  cafe ; trimmed  ", "cafe"},
		{"no separator here", ""},
		{"/who", ""},        // command prefix
		{"$review", ""},     // command prefix
		{"?help", ""},       // command prefix
		{"%options", ""},    // command prefix
		{"#123", ""},        // command prefix
		{";leading sep", ""}, // idx <= 0
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			assert.Equal(t, tt.want, parseDestination(tt.line))
		})
	}
}

func TestNameFromEntities(t *testing.T) {
	entities := map[string]interface{}{
		"#1": map[string]interface{}{"name": "Alice"},
		"#2": map[string]interface{}{"name": ""},        // empty name → fall back
		"#3": map[string]interface{}{"other": "field"},  // no name key → fall back
	}

	assert.Equal(t, "Alice", nameFromEntities(entities, "#1"))
	assert.Equal(t, "#2", nameFromEntities(entities, "#2"))
	assert.Equal(t, "#3", nameFromEntities(entities, "#3"))
	assert.Equal(t, "#9", nameFromEntities(entities, "#9"), "unknown handle falls back to itself")
	assert.Equal(t, "#1", nameFromEntities(nil, "#1"), "nil map falls back to handle")
}
