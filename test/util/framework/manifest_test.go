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
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

type manifestRoundTripper func(*http.Request) (*http.Response, error)

func (f manifestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type manifestBody struct {
	io.Reader
	closed bool
}

func (b *manifestBody) Close() error {
	b.closed = true
	return nil
}

type brokenManifestReader struct{}

func (brokenManifestReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestDownloadManifest(t *testing.T) {
	for _, tt := range []struct {
		name         string
		status       int
		networkError bool
		readError    bool
		persistent   bool
		wantAttempts int
		wantError    string
	}{
		{name: "success", status: http.StatusOK, wantAttempts: 1},
		{name: "GitHub HTTP 500 then success", status: http.StatusInternalServerError, wantAttempts: 2},
		{name: "rate limited then success", status: http.StatusTooManyRequests, wantAttempts: 2},
		{name: "network error then success", networkError: true, wantAttempts: 2},
		{name: "truncated response then success", status: http.StatusOK, readError: true, wantAttempts: 2},
		{name: "not found fails immediately", status: http.StatusNotFound, wantAttempts: 1, wantError: "HTTP 404"},
		{name: "forbidden fails immediately", status: http.StatusForbidden, wantAttempts: 1, wantError: "HTTP 403"},
		{name: "retry budget exhausted", status: http.StatusServiceUnavailable, persistent: true, wantAttempts: 3, wantError: "HTTP 503"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			var bodies []*manifestBody
			client := &http.Client{Transport: manifestRoundTripper(func(req *http.Request) (*http.Response, error) {
				for _, body := range bodies {
					if !body.closed {
						t.Error("previous response body was not closed before retry")
					}
				}
				attempts++
				status := http.StatusOK
				var reader io.Reader = strings.NewReader("kind: Namespace")
				if attempts == 1 || tt.persistent {
					if tt.networkError {
						return nil, io.ErrUnexpectedEOF
					}
					status = tt.status
					if tt.readError {
						reader = brokenManifestReader{}
					}
				}
				body := &manifestBody{Reader: reader}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: status, Body: body, Header: make(http.Header), Request: req}, nil
			})}
			body, err := downloadManifest(t.Context(), client, "https://example.com/manifest.yaml", wait.Backoff{Steps: 3})
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) || !strings.Contains(err.Error(), "https://example.com/manifest.yaml") {
					t.Fatalf("expected error containing URL and %q, got %v", tt.wantError, err)
				}
				if body != nil {
					t.Errorf("failed download returned partial body: %q", body)
				}
			} else if err != nil || string(body) != "kind: Namespace" {
				t.Fatalf("expected complete manifest, got %q, error %v", body, err)
			}
			if attempts != tt.wantAttempts {
				t.Errorf("expected %d attempts, got %d", tt.wantAttempts, attempts)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("response body was not closed")
				}
			}
		})
	}
}

func TestDownloadManifestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	requested := make(chan struct{})
	attempts := 0
	client := &http.Client{Transport: manifestRoundTripper(func(req *http.Request) (*http.Response, error) {
		attempts++
		close(requested)
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
	})}
	result := make(chan error, 1)
	go func() {
		_, err := downloadManifest(ctx, client, "https://example.com/manifest.yaml", wait.Backoff{Steps: 5, Duration: time.Hour})
		result <- err
	}()
	<-requested
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected one attempt before cancellation, got %d", attempts)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download retry did not stop after cancellation")
	}
}
