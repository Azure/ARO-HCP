package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestMiddlewareCorrelation(t *testing.T) {
	const (
		testClientRequestID      = "22222222-2222-2222-2222-222222222222"
		testCorrelationRequestID = "33333333-3333-3333-3333-333333333333"
	)

	type headerTest func(*testing.T, http.Header)

	headerValueEqual := func(k, expected string) headerTest {
		return func(t *testing.T, h http.Header) {
			t.Helper()
			if got := h.Get(k); expected != got {
				t.Fatalf("expected header %q to be %q, got %q", k, expected, got)
			}
		}
	}
	headerPresent := func(k string) headerTest {
		return func(t *testing.T, h http.Header) {
			t.Helper()
			if got := h.Get(k); got == "" {
				t.Fatalf("expected header %q to be present, got none", k)
			}
		}
	}
	headerAbsent := func(k string) headerTest {
		return func(t *testing.T, h http.Header) {
			t.Helper()
			if got := h.Get(k); got != "" {
				t.Fatalf("expected header %q to be absent, got %q", k, got)
			}
		}
	}

	type testCase struct {
		name string
		r    http.Request

		expectedHeaders         []headerTest
		expectedCorrelationID   string
		expectedClientRequestID string
	}

	tests := []testCase{
		{
			name: "should set the request ID header",
			r:    http.Request{},
			expectedHeaders: []headerTest{
				headerPresent(arm.HeaderNameRequestID),
				headerAbsent(arm.HeaderNameClientRequestID),
			},
		},
		{
			name: "should set the clientRequestId header when the 'should return client request id' header is true",
			r: http.Request{
				Header: http.Header{
					arm.HeaderNameClientRequestID:       []string{testClientRequestID},
					arm.HeaderNameCorrelationRequestID:  []string{testCorrelationRequestID},
					arm.HeaderNameReturnClientRequestID: []string{"true"},
				},
			},
			expectedHeaders: []headerTest{
				headerPresent(arm.HeaderNameRequestID),
				headerValueEqual(arm.HeaderNameClientRequestID, testClientRequestID),
			},
			expectedCorrelationID:   testCorrelationRequestID,
			expectedClientRequestID: testClientRequestID,
		},
		{
			name: "should not set the clientRequestId header when the 'should return client request id' header is false",
			r: http.Request{
				Header: http.Header{
					arm.HeaderNameClientRequestID:       []string{testClientRequestID},
					arm.HeaderNameCorrelationRequestID:  []string{testCorrelationRequestID},
					arm.HeaderNameReturnClientRequestID: []string{"false"},
				},
			},
			expectedHeaders: []headerTest{
				headerPresent(arm.HeaderNameRequestID),
				headerAbsent(arm.HeaderNameClientRequestID),
			},
			expectedCorrelationID:   testCorrelationRequestID,
			expectedClientRequestID: testClientRequestID,
		},
		{
			name: "should not set the clientRequestId header when the 'should return client request id' header is missing",
			r: http.Request{
				Header: http.Header{
					arm.HeaderNameClientRequestID:      []string{testClientRequestID},
					arm.HeaderNameCorrelationRequestID: []string{testCorrelationRequestID},
				},
			},
			expectedHeaders: []headerTest{
				headerPresent(arm.HeaderNameRequestID),
				headerAbsent(arm.HeaderNameClientRequestID),
			},
			expectedCorrelationID:   testCorrelationRequestID,
			expectedClientRequestID: testClientRequestID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				writer = httptest.NewRecorder()
				req    = &tt.r
				buf    bytes.Buffer
				logger = slog.New(slog.NewTextHandler(&buf, nil))
				data   *arm.CorrelationData
			)
			req = req.WithContext(ContextWithLogger(req.Context(), logger))

			next := func(w http.ResponseWriter, r *http.Request) {
				var err error
				// Capture the correlation data from the context.
				if data, err = CorrelationDataFromContext(r.Context()); err != nil {
					t.Logf("err: %s", err)
				}

				logger := LoggerFromContext(r.Context())
				// Emit a log message to check that it includes the correlation attributes.
				logger.Info("test")
				w.WriteHeader(http.StatusOK)
			}

			MiddlewareCorrelationData(writer, req, next)

			// Check that the expected headers are sent.
			for _, headerTest := range tt.expectedHeaders {
				headerTest(t, writer.Header())
			}

			// Check that the correlation data was found in the next handler.
			assert.NotNil(t, data)
			if data.RequestID.String() == "" {
				t.Fatalf("got empty request ID in the context")
			}

			if data.CorrelationRequestID != tt.expectedCorrelationID {
				t.Fatalf("expected correlation ID %q, got %q", tt.expectedCorrelationID, data.CorrelationRequestID)
			}

			if data.ClientRequestID != tt.expectedClientRequestID {
				t.Fatalf("expected client request ID %q, got %q", tt.expectedClientRequestID, data.ClientRequestID)
			}

			// Check that the contextual logger had the expected attributes.
			lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
			assert.Equal(t, 1, len(lines))

			line := string(lines[0])
			assert.Contains(t, line, " request_id=")

			if tt.expectedCorrelationID != "" {
				assert.Contains(t, line, tt.expectedCorrelationID)
			} else {
				assert.NotContains(t, line, " correlation_request_id=")
			}

			if tt.expectedClientRequestID != "" {
				assert.Contains(t, line, tt.expectedClientRequestID)
			} else {
				assert.NotContains(t, line, " client_request_id=")
			}
		})
	}
}
