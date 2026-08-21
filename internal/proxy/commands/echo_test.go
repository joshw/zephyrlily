package commands

import (
	"reflect"
	"testing"
)

func TestEcho(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"hello there", []string{"hello there"}},
		// Interior spacing survives: this is why %echo takes the raw remainder.
		{"a    b", []string{"a    b"}},
		// Case is significant, and quotes are not stripped — %echo is verbatim.
		{`Say "Hi"`, []string{`Say "Hi"`}},
		// No argument prints a blank line.
		{"", []string{""}},
	} {
		var got []string
		Echo(tc.text, collect(&got))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Echo(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestEchoHasHelp(t *testing.T) {
	if GetHelp("echo") == nil {
		t.Error("no help topic registered for the echo command")
	}
}
