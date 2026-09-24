package ui

import (
	"errors"
	"testing"

	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// editorModel builds a model sitting in edit mode, as it is when a save result
// arrives.
func editorModel() Model {
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false
	m.editMode = true
	return m
}

// A successful store prints nothing: Lily announces the save itself, so a
// local confirmation showed the user two messages for one edit.
func TestSaveSuccessLeavesTheAcknowledgementToLily(t *testing.T) {
	for _, meta := range []editMeta{
		{contentType: "memo", target: "me", name: "silent"},
		{contentType: "info", target: "me"},
	} {
		m := editorModel()
		before := len(m.output)

		updated, _ := m.Update(editorSaveResultMsg{meta: meta})
		m = updated.(Model)

		assert.False(t, m.editMode, "%s save should leave edit mode", meta.contentType)
		assert.Len(t, m.output, before, "%s save should add no output line", meta.contentType)
	}
}

func TestSaveFailureIsStillReported(t *testing.T) {
	m := editorModel()
	before := len(m.output)

	updated, _ := m.Update(editorSaveResultMsg{
		meta: editMeta{contentType: "memo", target: "me", name: "silent"},
		err:  errors.New("server rejected store: NOPE"),
	})
	m = updated.(Model)

	require.Len(t, m.output, before+1)
	last := m.output[len(m.output)-1]
	assert.Equal(t, "error", last.Type)
	assert.Contains(t, last.Data, "NOPE")
}
