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

package dnsreadback

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestWait(testingT *testing.T) {
	timeoutErr := fmt.Errorf("NS lookup: %w", &net.DNSError{Err: "i/o timeout", IsTimeout: true})
	temporaryErr := fmt.Errorf("NS lookup: %w", &net.DNSError{Err: "temporary failure", IsTemporary: true})
	for _, testCase := range []struct {
		name  string
		polls []error
	}{
		{name: "successful polls", polls: []error{nil, nil, nil}},
		{name: "timeouts preserve progress", polls: []error{nil, timeoutErr, nil, timeoutErr, nil}},
		{name: "temporary DNS errors without timeout preserve progress", polls: []error{nil, nil, temporaryErr, nil}},
		{name: "deadlines preserve progress", polls: []error{nil, fmt.Errorf("lookup: %w", context.DeadlineExceeded), nil, nil}},
		{name: "other errors reset progress", polls: []error{nil, nil, errors.New("connection refused"), nil, nil, nil}},
		{name: "wrong answer resets progress", polls: []error{nil, nil, fmt.Errorf("NS mismatch: %w", ErrInconsistent), nil, nil, nil}},
		{name: "missing answer resets progress", polls: []error{nil, nil, &net.DNSError{IsNotFound: true}, nil, nil, nil}},
		{name: "missing presentation resets progress", polls: []error{nil, nil, fmt.Errorf("%w: waiting for a presented DNS-01 challenge", ErrInconsistent), nil, nil, nil}},
		{name: "inconsistency overrides transient error", polls: []error{nil, nil, fmt.Errorf("%w: %w", ErrInconsistent, timeoutErr), nil, nil, nil}},
		{name: "inconsistency overrides temporary DNS error", polls: []error{nil, nil, fmt.Errorf("%w: %w", ErrInconsistent, temporaryErr), nil, nil, nil}},
		{name: "missing answer overrides timeout", polls: []error{nil, nil, &net.DNSError{IsNotFound: true, IsTimeout: true}, nil, nil, nil}},
		{name: "missing answer overrides temporary DNS error", polls: []error{nil, nil, &net.DNSError{IsNotFound: true, IsTemporary: true}, nil, nil, nil}},
	} {
		testingT.Run(testCase.name, func(testingT *testing.T) {
			polls := 0
			err := Wait(context.Background(), time.Microsecond, time.Second, 3, func(context.Context) error {
				if polls >= len(testCase.polls) {
					testingT.Fatal("readback did not converge after expected polls")
				}
				pollErr := testCase.polls[polls]
				polls++
				return pollErr
			})
			if err != nil || polls != len(testCase.polls) {
				testingT.Fatalf("got %d polls and error %v; want %d polls and no error", polls, err, len(testCase.polls))
			}
		})
	}
}

func TestWaitTimeout(testingT *testing.T) {
	err := Wait(context.Background(), time.Millisecond, 10*time.Millisecond, 3, func(context.Context) error {
		return &net.DNSError{Err: "i/o timeout", IsTimeout: true}
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		testingT.Fatalf("expected deadline error for unreachable DNS, got %v", err)
	}
}
