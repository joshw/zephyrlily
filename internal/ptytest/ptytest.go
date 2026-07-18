// Package ptytest builds portable script(1) invocations for tests that need
// to run a command under a real PTY. The two script implementations disagree
// on syntax:
//
//	BSD/macOS:  script -q <typescript> <command> [args...]
//	util-linux: script -q -e -c '<command>' <typescript>
//
// Tests embed the returned invocation inside larger sh pipelines (e.g. piping
// keystrokes into script's stdin), so this returns a shell fragment rather
// than an exec.Cmd.
package ptytest

import (
	"runtime"
	"strings"
)

// ScriptInvocation returns a shell fragment that runs inner (a shell command
// line) under a PTY via script(1), capturing terminal output to capture.
func ScriptInvocation(capture, inner string) string {
	if runtime.GOOS == "darwin" {
		return "script -q " + capture + " sh -c " + ShellQuote(inner)
	}
	// util-linux: -c takes the command (run via the shell), -e propagates the
	// child's exit status.
	return "script -q -e -c " + ShellQuote(inner) + " " + capture
}

// ShellQuote single-quotes s for embedding in an sh command line.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
