package api

import (
	"testing"

	"github.com/joshw/zephyrlily/internal/lily"
	"github.com/stretchr/testify/assert"
)

func TestEntityToJSON(t *testing.T) {
	t.Run("user", func(t *testing.T) {
		j := entityToJSON(&lily.Entity{
			Kind: lily.KindUser, Handle: "#1", Name: "Alice",
			Blurb: "hi", State: "here", Pronoun: "they",
			Creation: 99, // ignored for users
		})
		assert.Equal(t, "user", j.Kind)
		assert.Equal(t, "#1", j.Handle)
		assert.Equal(t, "Alice", j.Name)
		assert.Equal(t, "here", j.State)
		assert.Equal(t, "they", j.Pronoun)
		assert.Equal(t, int64(0), j.Creation, "Creation is only carried for discs")
	})

	t.Run("disc carries creation", func(t *testing.T) {
		j := entityToJSON(&lily.Entity{
			Kind: lily.KindDisc, Handle: "#5", Name: "cafe",
			Title: "The Cafe", Attrib: "public", Creation: 1700000000,
		})
		assert.Equal(t, "disc", j.Kind)
		assert.Equal(t, "The Cafe", j.Title)
		assert.Equal(t, "public", j.Attrib)
		assert.Equal(t, int64(1700000000), j.Creation)
	})

	t.Run("group carries members", func(t *testing.T) {
		j := entityToJSON(&lily.Entity{
			Kind: lily.KindGroup, Name: "friends", Members: []string{"#1", "#2"},
		})
		assert.Equal(t, "group", j.Kind)
		assert.Equal(t, []string{"#1", "#2"}, j.Members)
	})
}
