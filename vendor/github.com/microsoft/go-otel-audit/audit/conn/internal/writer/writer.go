// Package writer implements a writer to a remote audit server of any type that can be connected to
// by a net.Conn object.
package writer

import (
	"net"
	"time"

	"github.com/gostdlib/base/context"
	"github.com/microsoft/go-otel-audit/audit/msgs"
)

// ClosePool is a goroutine pool for closing connections to a remote audit server.
var ClosePool = context.Pool(context.Background()).Sub(context.Background(), "conn.TCPConn.Close")

type now func() time.Time

// Conn is a generic writer to a remote audit server. It can use anything that implements the net.Conn interface.
type Conn struct {
	conn net.Conn
	now  now
}

// New creates a new connection to the remote audit server.
func New(conn net.Conn) (*Conn, error) {
	return &Conn{conn: conn, now: time.Now}, nil
}

// Write writes a message to the remote audit server. Setting a timeout on the context has no effect.
// The write deadline is set to 15 seconds from the current time.
func (c *Conn) Write(msg msgs.Msg) error {
	c.conn.SetWriteDeadline(c.now().Add(15 * time.Second))

	var b []byte
	var err error
	b, err = msgs.MarshalMsgpack(msg)
	if err != nil {
		return err
	}

	_, err = c.conn.Write(b)
	return err
}

// CloseSend closes the send channel to the remote audit server.
func (c *Conn) CloseSend() error {
	ClosePool.Submit(
		context.Background(),
		func() {
			_ = c.conn.Close() // We don't care about an error here.
		},
	)
	return nil
}
