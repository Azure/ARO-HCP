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
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// mockPipelineResult represents a single response or error from the pipeline.
type mockPipelineResult struct {
	resp *http.Response
	err  error
}

// mockPipeline records calls and returns preconfigured results.
type mockPipeline struct {
	responses []mockPipelineResult
	callCount int
}

func (m *mockPipeline) Next(req *http.Request, middlewareIndex int) (*http.Response, error) {
	idx := m.callCount
	m.callCount++
	if idx < len(m.responses) {
		return m.responses[idx].resp, m.responses[idx].err
	}
	last := m.responses[len(m.responses)-1]
	return last.resp, last.err
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
				responses: []mockPipelineResult{
					{resp: newResponseWithRetryAfter(tt.statusCode, "0")},
					{resp: newResponse(http.StatusOK)},
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
	results := make([]mockPipelineResult, graphRetryMaxRetries+2)
	for i := range results {
		results[i] = mockPipelineResult{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, "0")}
	}

	pipeline := &mockPipeline{responses: results}
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
		responses: []mockPipelineResult{
			{resp: newResponse(http.StatusTooManyRequests)},
			{resp: newResponse(http.StatusOK)},
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
		responses: []mockPipelineResult{
			{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, "0")},
			{resp: newResponse(http.StatusOK)},
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
		responses: []mockPipelineResult{{resp: firstResp}, {resp: newResponse(http.StatusOK)}},
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
		responses: []mockPipelineResult{
			{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, "0")},
			{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, "0")},
			{resp: newResponse(http.StatusOK)},
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
		responses: []mockPipelineResult{
			{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, "1")},
			{resp: newResponse(http.StatusOK)},
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

func TestBodiedRequestRetriability(t *testing.T) {
	data := []byte("data")
	tests := []struct {
		name      string
		setupReq  func() *http.Request
		wantRetry bool
	}{
		{
			"not retriable without content length",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "https://graph.microsoft.com/test", bytes.NewReader(data))
				req.ContentLength = -1
				return req
			},
			false,
		},
		{
			"not retriable without seek or GetBody",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "https://graph.microsoft.com/test", bytes.NewReader(data))
				req.GetBody = nil
				return req
			},
			false,
		},
		{
			"retriable with GetBody",
			func() *http.Request {
				req := httptest.NewRequest(http.MethodPost, "https://graph.microsoft.com/test", nil)
				req.Body = io.NopCloser(bytes.NewReader(data))
				req.ContentLength = int64(len(data))
				req.GetBody = func() (io.ReadCloser, error) {
					return io.NopCloser(bytes.NewReader(data)), nil
				}
				return req
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline := &mockPipeline{
				responses: []mockPipelineResult{
					{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, "0")},
					{resp: newResponse(http.StatusOK)},
				},
			}

			handler := newGraphRetryHandler()
			resp, err := handler.Intercept(pipeline, 0, tt.setupReq())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantRetry {
				if pipeline.callCount != 2 {
					t.Errorf("expected 2 pipeline calls, got %d", pipeline.callCount)
				}
				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
				}
			} else {
				if pipeline.callCount != 1 {
					t.Errorf("expected 1 pipeline call, got %d", pipeline.callCount)
				}
				if resp.StatusCode != http.StatusTooManyRequests {
					t.Errorf("expected status 429, got %d", resp.StatusCode)
				}
			}
		})
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
		responses: []mockPipelineResult{
			{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, futureTime.Format(time.RFC1123))},
			{resp: newResponse(http.StatusOK)},
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
		responses: []mockPipelineResult{{resp: newResponse(http.StatusOK)}},
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

func newNetworkUnreachableError() error {
	return &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: syscall.ENETUNREACH,
	}
}

func newConnectionRefusedError() error {
	return &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: syscall.ECONNREFUSED,
	}
}

func TestNetworkErrorRetry(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []mockPipelineResult{
			{err: newNetworkUnreachableError()},
			{resp: newResponse(http.StatusOK)},
		},
	}

	handler := newGraphRetryHandler()
	req := newRequest(http.MethodDelete)

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("expected successful retry, got error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if pipeline.callCount != 2 {
		t.Errorf("expected 2 pipeline calls (initial + retry), got %d", pipeline.callCount)
	}
}

func TestNetworkErrorThenHTTPErrorRetry(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []mockPipelineResult{
			{err: newNetworkUnreachableError()},
			{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, "0")},
			{resp: newResponse(http.StatusOK)},
		},
	}

	handler := newGraphRetryHandler()
	req := newRequest(http.MethodGet)

	resp, err := handler.Intercept(pipeline, 0, req)
	if err != nil {
		t.Fatalf("expected successful retry, got error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if pipeline.callCount != 3 {
		t.Errorf("expected 3 pipeline calls, got %d", pipeline.callCount)
	}
}

func TestNetworkErrorNotRetriedOnNonNetError(t *testing.T) {
	pipeline := &mockPipeline{
		responses: []mockPipelineResult{
			{err: errors.New("some random error")},
		},
	}

	handler := newGraphRetryHandler()
	req := newRequest(http.MethodGet)

	_, err := handler.Intercept(pipeline, 0, req)
	if err == nil {
		t.Fatal("expected error")
	}
	if pipeline.callCount != 1 {
		t.Errorf("expected 1 pipeline call (no retry for non-net error), got %d", pipeline.callCount)
	}
}

func TestIsTransientNetworkError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{"net.OpError is transient", newNetworkUnreachableError(), true},
		{"connection refused is transient", newConnectionRefusedError(), true},
		{"context.Canceled is not transient", context.Canceled, false},
		{"context.DeadlineExceeded is not transient", context.DeadlineExceeded, false},
		{"random error is not transient", errors.New("random"), false},
		{"wrapped net.OpError is transient", errors.Join(errors.New("wrapper"), newNetworkUnreachableError()), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientNetworkError(tt.err)
			if got != tt.wantRetry {
				t.Errorf("isTransientNetworkError(%v) = %v, want %v", tt.err, got, tt.wantRetry)
			}
		})
	}
}

func TestCumulativeDelayLimit(t *testing.T) {
	// Use Retry-After to force large delays that exceed the cumulative limit.
	// Each retry requests a delay well over the max cumulative, so the second
	// retry attempt should be blocked by the cumulative cap.
	overLimit := strconv.Itoa(int(graphRetryMaxCumulativeDelay.Seconds()) + 1)
	results := make([]mockPipelineResult, 5)
	for i := range results {
		results[i] = mockPipelineResult{resp: newResponseWithRetryAfter(http.StatusTooManyRequests, overLimit)}
	}

	pipeline := &mockPipeline{responses: results}
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
