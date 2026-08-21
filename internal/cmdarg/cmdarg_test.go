package cmdarg

import "testing"

func TestFold(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"on", "on"},
		{"ON", "on"},
		{"Like", "like"},
		{"", ""},
	} {
		if got := Fold(tc.in); got != tc.want {
			t.Errorf("Fold(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIs(t *testing.T) {
	for _, tc := range []struct {
		tok, want string
		ok        bool
	}{
		{"list", "list", true},
		{"LIST", "list", true},
		{"List", "list", true},
		{"lists", "list", false},
		{"", "list", false},
	} {
		if got := Is(tc.tok, tc.want); got != tc.ok {
			t.Errorf("Is(%q, %q) = %v, want %v", tc.tok, tc.want, got, tc.ok)
		}
	}
}

func TestAny(t *testing.T) {
	for _, tc := range []struct {
		tok  string
		want []string
		ok   bool
	}{
		{"every", []string{"a", "every"}, true},
		{"EVERY", []string{"a", "every"}, true},
		{"A", []string{"a", "every"}, true},
		{"each", []string{"a", "every"}, false},
		{"a", nil, false},
	} {
		if got := Any(tc.tok, tc.want...); got != tc.ok {
			t.Errorf("Any(%q, %v) = %v, want %v", tc.tok, tc.want, got, tc.ok)
		}
	}
}

func TestOnOff(t *testing.T) {
	for _, tc := range []struct {
		in     string
		on, ok bool
	}{
		{"on", true, true},
		{"ON", true, true},
		{"On", true, true},
		{"off", false, true},
		{"OFF", false, true},
		{"Off", false, true},
		// Deliberately not synonyms: the accepted vocabulary is only what the
		// %help text documents. Callers fall through to their usage line.
		{"true", false, false},
		{"yes", false, false},
		{"1", false, false},
		{"", false, false},
	} {
		on, ok := OnOff(tc.in)
		if on != tc.on || ok != tc.ok {
			t.Errorf("OnOff(%q) = (%v, %v), want (%v, %v)", tc.in, on, ok, tc.on, tc.ok)
		}
	}
}
