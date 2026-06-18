package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsWordChar(t *testing.T) {
	for _, b := range []byte("aZ0_") {
		assert.True(t, isWordChar(b), "expected %q to be a word char", string(b))
	}
	for _, b := range []byte(" -.;:/") {
		assert.False(t, isWordChar(b), "expected %q to not be a word char", string(b))
	}
}

func TestWordStartBefore(t *testing.T) {
	tests := []struct {
		s    string
		pos  int
		want int
	}{
		{"hello world", 11, 6}, // from end → start of "world"
		{"hello world", 6, 0},  // from start of "world" (skips space) → start of "hello"
		{"hello", 0, 0},        // already at start
		{"  hi", 4, 2},         // skip trailing word, leading spaces remain
		{"a.b", 3, 2},          // punctuation boundary
	}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			assert.Equal(t, tt.want, wordStartBefore(tt.s, tt.pos))
		})
	}
}

func TestWordEndAfter(t *testing.T) {
	tests := []struct {
		s    string
		pos  int
		want int
	}{
		{"hello world", 0, 5},  // end of "hello"
		{"hello world", 5, 11}, // skip space → end of "world"
		{"hello", 5, 5},        // already at end
		{"a.b", 0, 1},          // punctuation boundary
		{"  hi", 0, 4},         // skip leading spaces → end of "hi"
	}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			assert.Equal(t, tt.want, wordEndAfter(tt.s, tt.pos))
		})
	}
}
