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

package verifiers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func overridePollInterval(t *testing.T) {
	t.Helper()
	orig := routeReachabilityPollInterval
	routeReachabilityPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { routeReachabilityPollInterval = orig })
}

func okResponse() *http.Response {
	return &http.Response{
		StatusCode:    200,
		Status:        "200 OK",
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader("OK")),
		ContentLength: 2,
	}
}

func dnsOpError(host string) *net.OpError {
	return &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &net.DNSError{
			Err:        "no such host",
			Name:       host,
			IsNotFound: true,
		},
	}
}

func TestWaitForRouteReachability_ImmediateSuccess(t *testing.T) {
	overridePollInterval(t)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return okResponse(), nil
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://example.com", 5*time.Second)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestWaitForRouteReachability_SuccessAfterNon200(t *testing.T) {
	overridePollInterval(t)
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return &http.Response{
					StatusCode:    503,
					Status:        "503 Service Unavailable",
					Proto:         "HTTP/1.1",
					ProtoMajor:    1,
					ProtoMinor:    1,
					Header:        make(http.Header),
					Body:          io.NopCloser(strings.NewReader("unavailable")),
					ContentLength: 11,
				}, nil
			}
			return okResponse(), nil
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://example.com", 5*time.Second)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestWaitForRouteReachability_DNSErrorClassification(t *testing.T) {
	overridePollInterval(t)
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 3 {
				return nil, dnsOpError(req.URL.Hostname())
			}
			return okResponse(), nil
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://test.example.com", 5*time.Second)
	if err != nil {
		t.Fatalf("expected success after DNS retries, got: %v", err)
	}
	if attempts < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestWaitForRouteReachability_DNSErrorThenNonDNSError(t *testing.T) {
	overridePollInterval(t)
	attempts := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			switch {
			case attempts <= 2:
				return nil, dnsOpError(req.URL.Hostname())
			case attempts <= 4:
				return nil, fmt.Errorf("connection reset")
			default:
				return okResponse(), nil
			}
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://test.example.com", 5*time.Second)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if attempts < 5 {
		t.Fatalf("expected at least 5 attempts, got %d", attempts)
	}
}

func TestWaitForRouteReachability_TimeoutReturnsLastError(t *testing.T) {
	overridePollInterval(t)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    503,
				Status:        "503 Service Unavailable",
				Proto:         "HTTP/1.1",
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        make(http.Header),
				Body:          io.NopCloser(strings.NewReader("unavailable")),
				ContentLength: 11,
			}, nil
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://example.com", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "route was never reachable") {
		t.Fatalf("expected 'route was never reachable' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected last status error wrapped, got: %v", err)
	}
}

func TestWaitForRouteReachability_DNSTimeoutReturnsLastError(t *testing.T) {
	overridePollInterval(t)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, dnsOpError(req.URL.Hostname())
		}),
	}

	err := waitForRouteReachability(context.Background(), client, "https://test.example.com", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "route was never reachable") {
		t.Fatalf("expected 'route was never reachable' in error, got: %v", err)
	}
}
