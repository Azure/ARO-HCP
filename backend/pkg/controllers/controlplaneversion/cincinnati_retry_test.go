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

package controlplaneversion

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

// fastTestBackoff keeps the retry test deterministic and fast: three steps
// with 1ms delays, well within any test timeout.
var fastTestBackoff = wait.Backoff{
	Duration: time.Millisecond,
	Factor:   1,
	Steps:    3,
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// transientDNSErr simulates the DNS lookup failure that motivated this
// retry logic (dial tcp: lookup api.openshift.com: i/o timeout).
var transientDNSErr = &net.DNSError{Err: "i/o timeout", Name: "api.openshift.com", IsTimeout: true}

func TestDoWithRetry(t *testing.T) {
	t.Run("retries a transient transport error and succeeds once it clears", func(t *testing.T) {
		attempts := 0
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 2 {
				return nil, transientDNSErr
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
		})}

		req, err := http.NewRequest("GET", "https://api.openshift.com/api/upgrades_info/graph", nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}

		body, err := doWithRetry(context.Background(), client, req, fastTestBackoff)
		if err != nil {
			t.Fatalf("expected success after retry, got error: %v", err)
		}
		if string(body) != "ok" {
			t.Errorf("expected body %q, got %q", "ok", body)
		}
		if attempts != 2 {
			t.Errorf("expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("retries a 5xx response", func(t *testing.T) {
		attempts := 0
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 2 {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
		})}

		req, err := http.NewRequest("GET", "https://api.openshift.com/api/upgrades_info/graph", nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}

		_, err = doWithRetry(context.Background(), client, req, fastTestBackoff)
		if err != nil {
			t.Fatalf("expected success after retry, got error: %v", err)
		}
		if attempts != 2 {
			t.Errorf("expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("does not retry a 4xx response", func(t *testing.T) {
		attempts := 0
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader(""))}, nil
		})}

		req, err := http.NewRequest("GET", "https://api.openshift.com/api/upgrades_info/graph", nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}

		_, err = doWithRetry(context.Background(), client, req, fastTestBackoff)
		if err == nil {
			t.Fatal("expected an error for a 4xx response")
		}
		if attempts != 1 {
			t.Errorf("expected exactly 1 attempt (no retry) for a 4xx response, got %d", attempts)
		}
	})

	t.Run("does not retry a non-transient transport error", func(t *testing.T) {
		attempts := 0
		nonTransient := errors.New(`unsupported protocol scheme ""`)
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return nil, nonTransient
		})}

		req, err := http.NewRequest("GET", "https://api.openshift.com/api/upgrades_info/graph", nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}

		_, err = doWithRetry(context.Background(), client, req, fastTestBackoff)
		if !errors.Is(err, nonTransient) {
			t.Errorf("expected the non-transient error to be returned as-is, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected exactly 1 attempt (no retry) for a non-transient error, got %d", attempts)
		}
	})

	t.Run("does not retry a permanent DNS lookup failure", func(t *testing.T) {
		attempts := 0
		nxdomain := &net.DNSError{Err: "no such host", Name: "api.openshift.com", IsNotFound: true}
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return nil, nxdomain
		})}

		req, err := http.NewRequest("GET", "https://api.openshift.com/api/upgrades_info/graph", nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}

		_, err = doWithRetry(context.Background(), client, req, fastTestBackoff)
		if !errors.Is(err, nxdomain) {
			t.Errorf("expected the NXDOMAIN error to be returned as-is, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected exactly 1 attempt (no retry) for a permanent DNS failure, got %d", attempts)
		}
	})

	t.Run("gives up after exhausting the backoff and preserves the last transport error", func(t *testing.T) {
		attempts := 0
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return nil, transientDNSErr
		})}

		req, err := http.NewRequest("GET", "https://api.openshift.com/api/upgrades_info/graph", nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}

		_, err = doWithRetry(context.Background(), client, req, fastTestBackoff)
		if err == nil {
			t.Fatal("expected an error once the backoff is exhausted")
		}
		if !errors.Is(err, transientDNSErr) {
			t.Errorf("expected the returned error to wrap the last transport error, got %v", err)
		}
		if attempts != fastTestBackoff.Steps {
			t.Errorf("expected %d attempts, got %d", fastTestBackoff.Steps, attempts)
		}
	})

	t.Run("stops immediately on context cancellation instead of retrying", func(t *testing.T) {
		attempts := 0
		ctx, cancel := context.WithCancel(context.Background())
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			cancel()
			return nil, req.Context().Err()
		})}

		req, err := http.NewRequest("GET", "https://api.openshift.com/api/upgrades_info/graph", nil)
		if err != nil {
			t.Fatalf("failed to build request: %v", err)
		}

		_, err = doWithRetry(ctx, client, req, fastTestBackoff)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected a context.Canceled error, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected exactly 1 attempt (no retry) after context cancellation, got %d", attempts)
		}
	})
}
