package conn

import (
	"testing"

	"github.com/microsoft/go-otel-audit/audit/msgs"
)

// TestHangConn is a test connection that hangs on CloseSend to simulate a hanging bug.
type TestHangConn struct {
	hangOnCloseSend bool
}

// NewTestHangConn creates a new test connection that hangs on CloseSend.
func NewTestHangConn() TestHangConn {
	if !testing.Testing() {
		panic("NewTestHangConn should only be used in tests")
	}
	return TestHangConn{hangOnCloseSend: true}
}

// Type returns the connection type for TestHangConn.
func (t TestHangConn) Type() Type {
	return TypeTCP
}

// Write writes a message to the connection. This is a no-op for TestHangConn.
func (t TestHangConn) Write(msg msgs.Msg) error {
	return nil
}

// CloseSend closes the send side of the connection. This will hang indefinitely
// to simulate a bug where bufio.Writer.Flush() hangs when the network is slow or broken.
func (t TestHangConn) CloseSend() error {
	if t.hangOnCloseSend {
		select {} // This blocks forever, just like a real Flush() hang would
	}
	return nil
}

func (t TestHangConn) private() {}
