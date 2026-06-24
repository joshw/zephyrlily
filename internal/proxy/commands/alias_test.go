package commands

import (
	"reflect"
	"testing"
)

func TestExpand(t *testing.T) {
	a := NewAliasTable()
	a.HandleCommand([]string{"inbeener", "/who", "beener", "$*"}, func([]string) {})
	a.HandleCommand([]string{"greet", "hi $1, you are arg $2"}, func([]string) {})
	a.HandleCommand([]string{"named", "alias=$0"}, func([]string) {})
	a.HandleCommand([]string{"multi", `bob;one\njim;two`}, func([]string) {})

	tests := []struct {
		name string
		line string
		want []string
		ok   bool
	}{
		{"all-args", "%inbeener foo bar", []string{"/who beener foo bar"}, true},
		{"all-args-empty", "%inbeener", []string{"/who beener "}, true},
		{"positional", "%greet alice bob", []string{"hi alice, you are arg bob"}, true},
		{"positional-missing", "%greet alice", []string{"hi alice, you are arg "}, true},
		{"alias-name", "%named x", []string{"alias=named"}, true},
		{"multi-command", "%multi", []string{"bob;one", "jim;two"}, true},
		{"not-an-alias", "%nope arg", nil, false},
		{"alias-self-guard", "%alias foo bar", nil, false},
		{"plain-text", "bob;hello", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := a.Expand(tt.line)
			if ok != tt.ok {
				t.Fatalf("Expand(%q) ok = %v, want %v", tt.line, ok, tt.ok)
			}
			if ok && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Expand(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestHandleCommand(t *testing.T) {
	collect := func(out *[]string) func([]string) {
		return func(lines []string) { *out = lines }
	}

	t.Run("define-and-confirm", func(t *testing.T) {
		a := NewAliasTable()
		var out []string
		a.HandleCommand([]string{"foo", "/who", "foo"}, collect(&out))
		want := []string{"(%foo is now aliased to '/who foo')"}
		if !reflect.DeepEqual(out, want) {
			t.Fatalf("define = %q, want %q", out, want)
		}
	})

	t.Run("list-all-empty", func(t *testing.T) {
		a := NewAliasTable()
		var out []string
		a.HandleCommand(nil, collect(&out))
		if len(out) != 1 || out[0] != "(no aliases are currently defined)" {
			t.Fatalf("empty list = %q", out)
		}
	})

	t.Run("list-all", func(t *testing.T) {
		a := NewAliasTable()
		a.HandleCommand([]string{"b", "second"}, func([]string) {})
		a.HandleCommand([]string{"a", "first"}, func([]string) {})
		var out []string
		a.HandleCommand([]string{"list"}, collect(&out))
		want := []string{"The following aliases are defined:", "a: first", "b: second"}
		if !reflect.DeepEqual(out, want) {
			t.Fatalf("list = %q, want %q", out, want)
		}
	})

	t.Run("list-one", func(t *testing.T) {
		a := NewAliasTable()
		a.HandleCommand([]string{"foo", "bar baz"}, func([]string) {})
		var out []string
		a.HandleCommand([]string{"list", "foo"}, collect(&out))
		if !reflect.DeepEqual(out, []string{"foo: bar baz"}) {
			t.Fatalf("list-one = %q", out)
		}
	})

	t.Run("not-aliased", func(t *testing.T) {
		a := NewAliasTable()
		var out []string
		a.HandleCommand([]string{"list", "ghost"}, collect(&out))
		if !reflect.DeepEqual(out, []string{"(ghost is not aliased)"}) {
			t.Fatalf("not-aliased = %q", out)
		}
	})

	t.Run("clear", func(t *testing.T) {
		a := NewAliasTable()
		a.HandleCommand([]string{"foo", "bar"}, func([]string) {})
		var out []string
		a.HandleCommand([]string{"clear", "foo"}, collect(&out))
		if !reflect.DeepEqual(out, []string{"(%foo is now unaliased.)"}) {
			t.Fatalf("clear = %q", out)
		}
		if _, ok := a.Expand("%foo"); ok {
			t.Fatal("alias still defined after clear")
		}
	})

	t.Run("clear-usage", func(t *testing.T) {
		a := NewAliasTable()
		var out []string
		a.HandleCommand([]string{"clear"}, collect(&out))
		if len(out) != 1 || out[0] != "(Usage: %alias clear <alias>)" {
			t.Fatalf("clear-usage = %q", out)
		}
	})

	t.Run("bad-name", func(t *testing.T) {
		a := NewAliasTable()
		var out []string
		a.HandleCommand([]string{"bad-name", "x"}, collect(&out))
		if len(out) != 1 || out[0] != "(First argument to %alias must be in set [A-Za-z0-9_])" {
			t.Fatalf("bad-name = %q", out)
		}
	})
}
