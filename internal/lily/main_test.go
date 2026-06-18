package lily

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak after the package's tests to catch goroutines left behind
// by the connection read loop or the fake server harness.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
