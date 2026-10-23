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
		name      string
		err       error
		wantField string
	}{
		{
			// NXDOMAIN from the resolver: "no such host".
			name:      "not-found",
			err:       &net.DNSError{Err: "no such host", Name: "app.example.com", IsNotFound: true, IsTemporary: false},
			wantField: "notFound",
		},
		{
			// Unresponsive resolver: "dial udp 172.30.0.10:53: i/o timeout".
			name:      "timeout",
			err:       &net.DNSError{Err: "dial udp 172.30.0.10:53: i/o timeout", Name: "app.example.com", IsNotFound: false, IsTemporary: true},
			wantField: "temporary",
		},
		{
			// Resolver answered with a non-not-found, non-temporary failure (e.g. SERVFAIL misconfigured).
			name:      "dns error without classification",
			err:       &net.DNSError{Err: "server misbehaving", Name: "app.example.com"},
			wantField: "other",
		},
		{
			name:      "non-dns error",
			err:       errors.New("context deadline exceeded"),
			wantField: "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tally dnsPollTally
			tally.record(tt.err)
			if tally.polls != 1 {
				t.Errorf("polls = %d, want 1", tally.polls)
			}
			fields := map[string]int{
				"notFound":  tally.notFound,
				"temporary": tally.temporary,
				"other":     tally.other,
			}
			for field, value := range fields {
				want := 0
				if field == tt.wantField {
					want = 1
				}
				if value != want {
					t.Errorf("%s = %d, want %d", field, value, want)
				}
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
	tally.record(errors.New("context deadline exceeded"))

	want := "5 polls: 3 not-found, 1 timeout/transient, 1 other"
	if got := tally.summary(); got != want {
		t.Errorf("summary() = %q, want %q", got, want)
	}
}

// TestWaitForDNSResolutionTimeoutMessage checks that the timeout error carries the
// poll-outcome tally, so a missing record (not-found polls) is distinguishable from
// an unresponsive resolver (timeout polls) without digging through logs.
func TestWaitForDNSResolutionTimeoutMessage(t *testing.T) {
	// A .invalid hostname can never resolve (RFC 2606), so the wait fails; what the
	// lookup errors look like depends on the environment's resolver (not-found when
	// it answers NXDOMAIN, temporary when it is unreachable), so the test only
	// asserts the structural parts of the message.
	err := WaitForDNSResolution(t.Context(), "aro-29937-reserved.invalid", 250*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error for reserved.invalid hostname, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"aro-29937-reserved.invalid",
		"did not resolve within",
		" polls: ",
		"last error:",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("timeout message %q does not contain %q", msg, want)
		}
	}
}
