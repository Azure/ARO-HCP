// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"errors"
	"net"
	"sync"
)

type ConnTracker struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func NewConnTracker() *ConnTracker {
	return &ConnTracker{
		conns: make(map[net.Conn]struct{}),
	}
}

func (t *ConnTracker) wrap(c net.Conn) net.Conn {
	t.mu.Lock()
	t.conns[c] = struct{}{}
	t.mu.Unlock()
	return &trackedConn{Conn: c, tracker: t}
}

func (t *ConnTracker) remove(c net.Conn) {
	t.mu.Lock()
	delete(t.conns, c)
	t.mu.Unlock()
}

func (t *ConnTracker) CloseAll() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var errs []error
	for c := range t.conns {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type trackedConn struct {
	net.Conn
	tracker *ConnTracker
	once    sync.Once
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		c.tracker.remove(c.Conn)
	})
	return err
}
