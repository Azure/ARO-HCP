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

package publicip

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseIPv4Boundaries(t *testing.T) {
	for _, value := range []string{"0.0.0.0", "192.0.2.42", "255.255.255.255"} {
		t.Run(value, func(t *testing.T) {
			address, err := ParseIPv4(value)
			if err != nil || address.String() != value {
				t.Fatalf("ParseIPv4(%q) = %v, %v", value, address, err)
			}
		})
	}
	if address, err := ParseIPv4(""); err == nil || address.IsValid() {
		t.Fatalf("ParseIPv4(empty) = %v, %v; want invalid address and error", address, err)
	}
}

func TestDiscoverIPv4Fallback(t *testing.T) {
	var providers []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		providers = append(providers, request.URL.String())
		switch request.URL.Host {
		case "ifconfig.me":
			return echoResponse(http.StatusOK, "2001:db8::1"), nil
		case "icanhazip.com":
			return nil, errors.New("connection failed")
		default:
			return echoResponse(http.StatusOK, "\r\n 192.0.2.42\t\n"), nil
		}
	})}
	address, err := discoverIPv4(context.Background(), client)
	if err != nil || address.String() != "192.0.2.42" {
		t.Fatalf("discoverIPv4 = %v, %v", address, err)
	}
	want := []string{"https://ifconfig.me", "https://icanhazip.com", "https://ifconfig.co"}
	if !reflect.DeepEqual(providers, want) {
		t.Fatalf("providers = %v, want %v", providers, want)
	}
}

func TestDiscoverIPv4StopsAfterSuccess(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		return echoResponse(http.StatusOK, "192.0.2.42\n"), nil
	})}
	address, err := discoverIPv4(context.Background(), client)
	if err != nil || address.String() != "192.0.2.42" || calls != 1 {
		t.Fatalf("discoverIPv4 = %v, %v after %d calls", address, err, calls)
	}
}

func TestDiscoverIPv4TotalFailure(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		return echoResponse(http.StatusServiceUnavailable, "192.0.2.42"), nil
	})}
	address, err := discoverIPv4(context.Background(), client)
	if err == nil || address.IsValid() || calls != 3 {
		t.Fatalf("discoverIPv4 = %v, %v after %d calls; want failure after all providers", address, err, calls)
	}
	for _, provider := range []string{"https://ifconfig.me", "https://icanhazip.com", "https://ifconfig.co"} {
		if !strings.Contains(err.Error(), provider) {
			t.Errorf("error %q omits provider %s", err, provider)
		}
	}
}

func TestQueryIPv4RejectsInvalidResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "bad status", status: http.StatusForbidden, body: "192.0.2.42"},
		{name: "empty", status: http.StatusOK},
		{name: "HTML", status: http.StatusOK, body: "<html>192.0.2.42</html>"},
		{name: "IPv6", status: http.StatusOK, body: "2001:db8::1"},
		{name: "mapped IPv6", status: http.StatusOK, body: "::ffff:192.0.2.42"},
		{name: "CIDR", status: http.StatusOK, body: "192.0.2.42/32"},
		{name: "multiple addresses", status: http.StatusOK, body: "192.0.2.42\n192.0.2.43"},
		{name: "leading zero", status: http.StatusOK, body: "192.000.2.42"},
		{name: "oversized", status: http.StatusOK, body: "192.0.2.42" + strings.Repeat(" ", maxResponseBytes)},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return echoResponse(test.status, test.body), nil
			})}
			address, err := queryIPv4(context.Background(), client, "https://ifconfig.me")
			if err == nil || address.IsValid() {
				t.Fatalf("queryIPv4 = %v, %v; want invalid address and error", address, err)
			}
		})
	}
}

func TestQueryIPv4BoundsAndClosesResponse(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader(strings.Repeat("x", maxResponseBytes*4))}
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}
	if _, err := queryIPv4(context.Background(), client, "https://ifconfig.me"); err == nil {
		t.Fatal("oversized response succeeded")
	}
	if body.bytesRead != maxResponseBytes+1 || !body.closed {
		t.Fatalf("response read %d bytes, closed=%t; want %d and true", body.bytesRead, body.closed, maxResponseBytes+1)
	}
}

func TestQueryIPv4ReadFailure(t *testing.T) {
	readErr := errors.New("response interrupted")
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(errorReader{err: readErr})}, nil
	})}
	address, err := queryIPv4(context.Background(), client, "https://ifconfig.me")
	if !errors.Is(err, readErr) || address.IsValid() {
		t.Fatalf("queryIPv4 = %v, %v; want read failure", address, err)
	}
}

func TestDiscoverIPv4Cancellation(t *testing.T) {
	for _, canceledBeforeRequest := range []bool{true, false} {
		t.Run(map[bool]string{true: "before request", false: "during request"}[canceledBeforeRequest], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceledBeforeRequest {
				cancel()
			}
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				cancel()
				<-request.Context().Done()
				return nil, request.Context().Err()
			})}
			address, err := discoverIPv4(ctx, client)
			if !errors.Is(err, context.Canceled) || address.IsValid() {
				t.Fatalf("discoverIPv4 = %v, %v; want cancellation", address, err)
			}
			wantCalls := 1
			if canceledBeforeRequest {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("requests = %d, want %d", calls, wantCalls)
			}
		})
	}
}

func TestDiscoveryClientRejectsRedirect(t *testing.T) {
	client := newDiscoveryClient()
	var providers []string
	client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		providers = append(providers, request.URL.String())
		if len(providers) == 1 {
			response := echoResponse(http.StatusFound, "192.0.2.99")
			response.Header = http.Header{"Location": []string{"http://insecure.example"}}
			return response, nil
		}
		return echoResponse(http.StatusOK, "192.0.2.42"), nil
	})
	address, err := discoverIPv4(context.Background(), client)
	if err != nil || address.String() != "192.0.2.42" {
		t.Fatalf("discoverIPv4 = %v, %v", address, err)
	}
	want := []string{"https://ifconfig.me", "https://icanhazip.com"}
	if !reflect.DeepEqual(providers, want) {
		t.Fatalf("providers = %v, want %v", providers, want)
	}
}

func TestDiscoveryClientBoundsEachRequest(t *testing.T) {
	client := newDiscoveryClient()
	calls := 0
	client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > requestTimeout {
			t.Fatalf("request deadline = %v, present=%t; want finite per-request deadline", deadline, ok)
		}
		return nil, context.DeadlineExceeded
	})
	address, err := discoverIPv4(context.Background(), client)
	if !errors.Is(err, context.DeadlineExceeded) || address.IsValid() || calls != 3 {
		t.Fatalf("discoverIPv4 = %v, %v after %d calls; want timeout fallback", address, err, calls)
	}
}

func TestDiscoveryClientForcesIPv4(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client := newDiscoveryClient()
	defer client.CloseIdleConnections()
	transport := client.Transport.(*http.Transport)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// The transport must override the requested family, as curl -4 does.
	connection, err := transport.DialContext(ctx, "tcp6", listener.Addr().String())
	if err != nil {
		t.Fatalf("IPv4 dial with requested tcp6 network failed: %v", err)
	}
	connection.Close()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func echoResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

type trackingBody struct {
	io.Reader
	bytesRead int
	closed    bool
}

func (b *trackingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.bytesRead += n
	return n, err
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

type errorReader struct {
	err error
}

func (r errorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}
