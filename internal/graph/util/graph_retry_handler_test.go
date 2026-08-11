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

package util

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// mockPipeline records calls and returns preconfigured responses.
type mockPipeline struct {
	responses []*http.Response
	callCount int
}

func (m *mockPipeline) Next(req *http.Request, middlewareIndex int) (*http.Response, error) {
	idx := m.callCount
	m.callCount++
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return m.responses[len(m.responses)-1], nil
}

func newResponse(statusCode int) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
}

func newResponseWithRetryAfter(statusCode int, retryAfter string) *http.Response {
	resp := newResponse(statusCode)
	resp.Header.Set("Retry-After", retryAfter)
	return resp
}

func newRequest(method string) *http.Request {
	req := httptest.NewRequest(method, "https://graph.microsoft.com/v1.0/applications/test-id/addPassword", nil)
	return req
}

type trackingCloser struct {
	io.ReadCloser
	closed *bool
}

func (tc *trackingCloser) Close() error {
	*tc.closed = true
	return tc.ReadCloser.Close()
}

type readSeekCloser struct {
	*bytes.Reader
}

func (readSeekCloser) Close() error { return nil }

func newPostRequestWithBody() *http.Request {
	body := &readSeekCloser{bytes.NewReader([]byte(`{"test":"data"}`))}
	req := httptest.NewRequest(http.MethodPost, "https://graph.microsoft.com/v1.0/applications/test-id/addPassword", nil)
	req.Body = body
	req.ContentLength = int64(body.Len())
	return req
}

func TestRetriableStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		statusCode int
		url        string
		wantRetry  bool
	}{
		{"429 on GET retries", http.MethodGet, http.StatusTooManyRequests, "", true},
		{"429 on POST retries", http.MethodPost, http.StatusTooManyRequests, "", true},
		{"503 retries", http.MethodGet, http.StatusServiceUnavailable, "", true},
		{"504 retries", http.MethodGet, http.StatusGatewayTimeout, "", true},
		{"404 on POST retries", http.MethodPost, http.StatusNotFound, "", true},
		{"404 on GET does not retry", http.MethodGet, http.StatusNotFound, "", false},
		{"404 on DELETE does not retry", http.MethodDelete, http.StatusNotFound, "", false},
		{"400 on POST /servicePrincipals retries (eventual consistency)", http.MethodPost, http.StatusBadRequest, "https://graph.microsoft.com/v1.0/servicePrincipals", true},
		{"400 on POST /applications does not retry", http.MethodPost, http.StatusBadRequest, "https://graph.microsoft.com/v1.0/applications", false},
		{"400 on POST addPassword does not retry", http.MethodPost, http.StatusBadRequest, "", false},
		{"400 on GET /servicePrincipals does not retry", http.MethodGet, http.StatusBadRequest, "https://graph.microsoft.com/v1.0/servicePrincipals", false},
		{"400 on DELETE does not retry", http.MethodDelete, http.StatusBadRequest, "", false},
		{"403 on POST /servicePrincipals retries (eventual consistency)", http.MethodPost, http.StatusForbidden, "https://graph.microsoft.com/v1.0/servicePrincipals", true},
		{"403 on POST /applications does not retry", http.MethodPost, http.StatusForbidden, "https://graph.microsoft.com/v1.0/applications", false},
		{"403 on POST addPassword retries (eventual consistency)", http.MethodPost, http.StatusForbidden, "", true},
		{"403 on GET /servicePrincipals does not retry", http.MethodGet, http.StatusForbidden, "https://graph.microsoft.com/v1.0/servicePrincipals", false},
		{"403 on DELETE does not retry", http.MethodDelete, http.StatusForbidden, "", false},
		{"500 does not retry", http.MethodPost, http.StatusInternalServerError, "", false},
		{"200 does not retry", http.MethodPost, http.StatusOK, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &mockPipeline{
				responses: []*http.Response{
					newResponseWithRetryAfter(tt.statusCode, "0"),
					newResponse(http.StatusOK),
				},
			}

			handler := newGraphRetryHandler()
			var req *http.Request
			if tt.url != "" {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			} else {
				req = newRequest(tt.method)
			}
			resp, err := handler.Intercept(pipeline, 0, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantRetry {
				if pipeline.callCount != 2 {
					t.Errorf("expected 2 pipeline calls (initial + retry), got %d", pipeline.callCount)
				}
				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected final status 200, got %d", resp.StatusCode)
				}
			} else {
				if pipeline.callCount != 1 {
					t.Errorf("expected 1 pipeline call (no retry), got %d", pipeline.callCount)
				}
				if resp.StatusCode != tt.statusCode {
					t.Errorf("expected status %d, got %d", tt.statusCode, resp.StatusCode)
				}
			}
		})
	}
}

func TestMaxRetries(t *testing.T) {
	responses := make([]*http.Response, graphRetryMaxRetries+2)
	for i := range responses {
		responses[i] = newResponseWithRetryAfter(http.StatusTooManyRequests, "0")
	}

	pipeline := &mockPipeline{responses: responses}
	handler := newGraphRetryHandler()
	req := newRequest(http.MethodGet)

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1 initial + maxRetries
	expectedCalls := graphRetryMaxRetries + 1
	if pipeline.callCount != expectedCalls {
		t.Errorf("expected %d pipeline calls, got %d", expectedCalls, pipeline.callCount)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429 after exhausting retries, got %d", resp.StatusCode)
	}
}

func TestContextCancellation(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []*http.Response{
			newResponse(http.StatusTooManyRequests),
			newResponse(http.StatusOK),
		},
	}

	handler := newGraphRetryHandler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "https://graph.microsoft.com/test", nil)
	req = req.WithContext(ctx)

	_, err := handler.Intercept(pipeline, 0, req)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if pipeline.callCount != 1 {
		t.Errorf("expected 1 pipeline call before cancellation, got %d", pipeline.callCount)
	}
}

func TestBodySeekedOnRetry(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []*http.Response{
			newResponseWithRetryAfter(http.StatusTooManyRequests, "0"),
			newResponse(http.StatusOK),
		},
	}

	handler := newGraphRetryHandler()
	req := newPostRequestWithBody()

	// Partially consume the body to verify the middleware seeks it back
	buf := make([]byte, 5)
	_, err := req.Body.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error reading body: %v", err)
	}

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if pipeline.callCount != 2 {
		t.Errorf("expected 2 pipeline calls, got %d", pipeline.callCount)
	}

	// Verify body was seeked back — should be readable from start
	if _, err = req.Body.(io.Seeker).Seek(0, io.SeekStart); err != nil {
		t.Fatalf("unexpected error seeking body: %v", err)
	}
	content, _ := io.ReadAll(req.Body)
	if string(content) != `{"test":"data"}` {
		t.Errorf("expected body content to be preserved, got %q", string(content))
	}
}

func TestResponseBodyClosedOnRetry(t *testing.T) {
	closed := false
	firstResp := newResponseWithRetryAfter(http.StatusTooManyRequests, "0")
	firstResp.Body = &trackingCloser{ReadCloser: firstResp.Body, closed: &closed}

	pipeline := &mockPipeline{
		responses: []*http.Response{firstResp, newResponse(http.StatusOK)},
	}

	handler := newGraphRetryHandler()
	req := newRequest(http.MethodGet)

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if !closed {
		t.Error("previous response body was not closed before retry")
	}
}

func TestRetryAttemptHeader(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []*http.Response{
			newResponseWithRetryAfter(http.StatusTooManyRequests, "0"),
			newResponseWithRetryAfter(http.StatusTooManyRequests, "0"),
			newResponse(http.StatusOK),
		},
	}

	handler := newGraphRetryHandler()
	req := newRequest(http.MethodGet)

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	retryAttempt := req.Header.Get("Retry-Attempt")
	if retryAttempt != "2" {
		t.Errorf("expected Retry-Attempt header to be '2', got %q", retryAttempt)
	}
}

func TestRetryAfterHeaderSeconds(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []*http.Response{
			newResponseWithRetryAfter(http.StatusTooManyRequests, "1"),
			newResponse(http.StatusOK),
		},
	}

	handler := newGraphRetryHandler()
	req := newRequest(http.MethodGet)

	start := time.Now()
	resp, err := handler.Intercept(pipeline, 0, req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Should have waited ~1 second (Retry-After: 1), not the 5s base delay
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected delay of ~1s from Retry-After, got %v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("delay too long (%v), Retry-After should have been 1s", elapsed)
	}
}

func TestBodiedRequestNotRetriableWithoutContentLength(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []*http.Response{
			newResponse(http.StatusTooManyRequests),
			newResponse(http.StatusOK),
		},
	}

	handler := newGraphRetryHandler()
	req := httptest.NewRequest(http.MethodPost, "https://graph.microsoft.com/test", bytes.NewReader([]byte("data")))
	req.ContentLength = -1

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pipeline.callCount != 1 {
		t.Errorf("expected 1 pipeline call (no retry for unknown content length), got %d", pipeline.callCount)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", resp.StatusCode)
	}
}

func TestBodiedRequestNotRetriableWithoutSeekOrGetBody(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []*http.Response{
			newResponse(http.StatusTooManyRequests),
			newResponse(http.StatusOK),
		},
	}

	handler := newGraphRetryHandler()
	req := httptest.NewRequest(http.MethodPost, "https://graph.microsoft.com/test", bytes.NewReader([]byte("data")))
	req.GetBody = nil

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pipeline.callCount != 1 {
		t.Errorf("expected 1 pipeline call (body not rewindable), got %d", pipeline.callCount)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", resp.StatusCode)
	}
}

func TestBodiedRequestRetriableWithGetBody(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []*http.Response{
			newResponseWithRetryAfter(http.StatusTooManyRequests, "0"),
			newResponse(http.StatusOK),
		},
	}

	handler := newGraphRetryHandler()
	data := []byte("data")
	req := httptest.NewRequest(http.MethodPost, "https://graph.microsoft.com/test", nil)
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pipeline.callCount != 2 {
		t.Errorf("expected 2 pipeline calls (retry via GetBody), got %d", pipeline.callCount)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestExponentialBackoffDelay(t *testing.T) {
	handler := newGraphRetryHandler()
	resp := newResponse(http.StatusTooManyRequests)

	for i := 1; i <= 3; i++ {
		delay := handler.getRetryDelay(resp, i)
		expectedBase := time.Duration(graphRetryBaseDelaySeconds*(1<<(i-1))) * time.Second
		// Delay should be at least the base (jitter only adds)
		if delay < expectedBase {
			t.Errorf("attempt %d: delay %v less than expected base %v", i, delay, expectedBase)
		}
		// Delay should be at most base + 1s (max jitter)
		if delay > expectedBase+time.Second {
			t.Errorf("attempt %d: delay %v exceeds expected max %v", i, delay, expectedBase+time.Second)
		}
	}
}

func TestRetryAfterHeaderRFC1123(t *testing.T) {
	futureTime := time.Now().Add(2 * time.Second).UTC()
	pipeline := &mockPipeline{
		responses: []*http.Response{
			newResponseWithRetryAfter(http.StatusTooManyRequests, futureTime.Format(time.RFC1123)),
			newResponse(http.StatusOK),
		},
	}

	handler := newGraphRetryHandler()
	req := newRequest(http.MethodGet)

	start := time.Now()
	resp, err := handler.Intercept(pipeline, 0, req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if elapsed < 1*time.Second {
		t.Errorf("expected delay of ~2s from RFC1123 Retry-After, got %v", elapsed)
	}
}

func TestSuccessfulRequestNoRetry(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []*http.Response{newResponse(http.StatusOK)},
	}

	handler := newGraphRetryHandler()
	req := newRequest(http.MethodPost)

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pipeline.callCount != 1 {
		t.Errorf("expected 1 pipeline call for success, got %d", pipeline.callCount)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestCumulativeDelayLimit(t *testing.T) {
	// Use Retry-After to force large delays that exceed the cumulative limit.
	// Each retry requests a delay well over the max cumulative, so the second
	// retry attempt should be blocked by the cumulative cap.
	overLimit := strconv.Itoa(int(graphRetryMaxCumulativeDelay.Seconds()) + 1)
	responses := make([]*http.Response, 5)
	for i := range responses {
		responses[i] = newResponseWithRetryAfter(http.StatusTooManyRequests, overLimit)
	}

	pipeline := &mockPipeline{responses: responses}
	handler := newGraphRetryHandler()
	req := newRequest(http.MethodGet)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First retry delay (121s) exceeds cumulative limit (120s), so should stop
	if pipeline.callCount != 1 {
		t.Errorf("expected 1 pipeline call (cumulative delay exceeded), got %d", pipeline.callCount)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", resp.StatusCode)
	}
}
