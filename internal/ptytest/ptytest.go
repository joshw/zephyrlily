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
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// RunWithTimeout runs an sh pipeline with a hard timeout so a wedged PTY
// session fails one test quickly instead of hanging the whole suite until
// the go test alarm. WaitDelay makes Wait return even while the script
// process still holds the output pipes open.
func RunWithTimeout(t *testing.T, timeout time.Duration, pipeline string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", pipeline)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("pty pipeline timed out after %s; output:\n%s", timeout, out)
	}
	return out, err
}

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
