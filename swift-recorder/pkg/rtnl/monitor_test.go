// Copyright 2026 Microsoft Corporation
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

//go:build linux

package rtnl

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// netlinkMessage builds one netlink message with a body of bodyLen zero bytes.
func netlinkMessage(msgType uint16, bodyLen int) []byte {
	b := make([]byte, unix.NLMSG_HDRLEN+bodyLen)
	binary.NativeEndian.PutUint32(b[0:4], uint32(len(b)))
	binary.NativeEndian.PutUint16(b[4:6], msgType)
	return b
}

func TestParseNotifications(t *testing.T) {
	tests := []struct {
		name       string
		datagram   []byte
		wantErr    error
		wantEvents int
	}{
		{
			name:     "overrun reports overflow",
			datagram: netlinkMessage(unix.NLMSG_OVERRUN, 0),
			wantErr:  ErrOverflow,
		},
		{
			name:     "overrun after a link notification still reports overflow",
			datagram: append(netlinkMessage(unix.RTM_NEWLINK, unix.SizeofIfInfomsg), netlinkMessage(unix.NLMSG_OVERRUN, 0)...),
			wantErr:  ErrOverflow,
		},
		{
			name:       "unrelated type is skipped",
			datagram:   netlinkMessage(unix.RTM_NEWROUTE, unix.SizeofRtMsg),
			wantEvents: 0,
		},
		{
			name:       "link notification is decoded",
			datagram:   netlinkMessage(unix.RTM_NEWLINK, unix.SizeofIfInfomsg),
			wantEvents: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, err := parseNotifications(tc.datagram, time.Unix(0, 0))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if len(events) != tc.wantEvents {
				t.Fatalf("got %d events, want %d", len(events), tc.wantEvents)
			}
		})
	}
}
