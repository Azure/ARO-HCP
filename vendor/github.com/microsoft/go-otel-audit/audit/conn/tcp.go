//go:build linux || darwin

package conn

import (
	"fmt"
	"net"
	"time"

	"github.com/microsoft/go-otel-audit/audit/conn/internal/writer"
)

// Compile check on interface implementation.
var _ Audit = TCPConn{}

// TCPConn represents a connection to a remote audit server via a TCP socket
// This implements conn.Audit interface.
type TCPConn struct {
	*writer.Conn
}

// Type returns the type of the audit connection.
func (TCPConn) Type() Type {
	return TypeTCP
}

func (TCPConn) private() {}

// NewTCPConn creates a new connection to the remote audit server. addr is the host:port of the remote audit server.
// host can be an IP address or a hostname. If host is a hostname, it will be resolved to an IP address.
func NewTCPConn(addr string) (TCPConn, error) {
	l := writer.ClosePool.Len()
	if l >= 10 {
		return TCPConn{}, fmt.Errorf("there are %d TCP connections trying to close, this indicates some system level error", l)
	}

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return TCPConn{}, err
	}
	w, err := writer.New(conn)
	if err != nil {
		_ = conn.Close() // Close the connection if writer creation fails
		return TCPConn{}, err
	}
	return TCPConn{Conn: w}, nil
}
