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

package framework

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHostnameFromURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		expectErr bool
	}{
		{
			name:  "full HTTPS URL",
			input: "https://console-openshift-console.apps.example.com",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "HTTPS URL with port",
			input: "https://console-openshift-console.apps.example.com:443",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "HTTPS URL with path",
			input: "https://console-openshift-console.apps.example.com/path",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "HTTP URL",
			input: "http://example.com:8080",
			want:  "example.com",
		},
		{
			name:  "bare hostname",
			input: "console-openshift-console.apps.example.com",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "bare hostname with port",
			input: "console-openshift-console.apps.example.com:443",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "bare hostname with path",
			input: "console-openshift-console.apps.example.com/some/path",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:  "HTTPS URL with path and port",
			input: "https://console-openshift-console.apps.example.com:8080/api/v1",
			want:  "console-openshift-console.apps.example.com",
		},
		{
			name:      "empty string",
			input:     "",
			expectErr: true,
		},
		{
			name:      "scheme only",
			input:     "https://",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HostnameFromURL(tt.input)
			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error, got hostname %q", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDNSPollTallyRecord(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want dnsPollTally
	}{
		{
			// NXDOMAIN from the resolver: "no such host".
			name: "not-found",
			err:  &net.DNSError{Err: "no such host", Name: "app.example.com", IsNotFound: true, IsTemporary: false},
			want: dnsPollTally{notFound: 1},
		},
		{
			// Unresponsive resolver: "dial udp 172.30.0.10:53: i/o timeout".
			name: "timeout",
			err:  &net.DNSError{Err: "dial udp 172.30.0.10:53: i/o timeout", Name: "app.example.com", IsNotFound: false, IsTemporary: true},
			want: dnsPollTally{temporary: 1},
		},
		{
			// Deadline expired mid-lookup: timeout-only, without IsTemporary -
			// the shape dnsreadback's poll already treats as progress-preserving.
			name: "timeout without temporary",
			err:  &net.DNSError{Err: "i/o timeout", Name: "app.example.com", IsTimeout: true, IsTemporary: false},
			want: dnsPollTally{temporary: 1},
		},
		{
			// Resolver answered with a non-not-found, non-temporary failure (e.g. SERVFAIL misconfigured).
			name: "dns error without classification",
			err:  &net.DNSError{Err: "server misbehaving", Name: "app.example.com"},
			want: dnsPollTally{other: 1},
		},
		{
			name: "non-dns error",
			err:  errors.New("context deadline exceeded"),
			want: dnsPollTally{other: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tally dnsPollTally
			tally.record(tt.err)
			if tally != tt.want {
				t.Errorf("tally = %+v, want %+v", tally, tt.want)
			}
		})
	}
}

func TestDNSPollTallySummary(t *testing.T) {
	var tally dnsPollTally
	for i := 0; i < 3; i++ {
		tally.record(&net.DNSError{Err: "no such host", Name: "app.example.com", IsNotFound: true})
	}
	tally.record(&net.DNSError{Err: "dial udp 172.30.0.10:53: i/o timeout", Name: "app.example.com", IsTemporary: true})
	tally.record(&net.DNSError{Err: "i/o timeout", Name: "app.example.com", IsTimeout: true})
	tally.record(errors.New("context deadline exceeded"))

	want := "6 polls: 3 not-found, 2 timeout/transient, 1 other"
	if got := tally.summary(); got != want {
		t.Errorf("summary() = %q, want %q", got, want)
	}
}

// func checks that the timeout error carries the poll-outcome tally, so a missing record is
// distinguishable from an unresponsive resolver (timeout polls) without digging through logs.
func TestWaitForDNSResolutionTimeoutMessage(t *testing.T) {
	// A .invalid hostname can never resolve (RFC 2606), so the wait fails; what the
	// lookup errors look like depends on the environment's resolver so the test only
	// asserts the structural parts of the message.
	err := WaitForDNSResolution(t.Context(), "aro-29937-reserved.invalid.", 250*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error for reserved.invalid hostname, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"aro-29937-reserved.invalid.",
		"did not resolve within",
		" polls: ",
		"last error:",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("timeout message %q does not contain %q", msg, want)
		}
	}
	// The tally must be present and well-formed; its exact rendering is pinned by
	// TestDNSPollTallySummary, and record() classifies every poll into exactly one bucket.
	m := tallyPattern.FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("timeout message %q does not contain a parseable poll tally", msg)
	}
	// At least one poll must have been classified: a "0 polls: 0 not-found, ..."
	// tally means record() got disconnected from the lookup loop, and the message
	// no longer distinguishes a missing record from an unresponsive resolver.
	var total int
	for _, count := range m[1:] {
		n, err := strconv.Atoi(count)
		if err != nil {
			t.Fatalf("tally count %q in message %q does not parse: %v", count, msg, err)
		}
		total += n
	}
	if total == 0 {
		t.Errorf("timeout message %q reports zero classified polls; the tally is disconnected from the lookup loop", msg)
	}
}

// tallyPattern matches the poll-outcome tally rendered by dnsPollTally.summary():
// the total poll count followed by the three classification buckets.
var tallyPattern = regexp.MustCompile(`(\d+) polls: (\d+) not-found, (\d+) timeout/transient, (\d+) other`)
