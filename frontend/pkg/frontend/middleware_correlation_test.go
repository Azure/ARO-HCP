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

package frontend

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

	type responseTest func(*testing.T, *http.Response)

	headerValueEqual := func(k, expected string) responseTest {
		return func(t *testing.T, r *http.Response) {
			t.Helper()
			if got := r.Header.Get(k); expected != got {
				t.Fatalf("expected header %q to be %q, got %q", k, expected, got)
			}
		}
	}
	headerPresent := func(k string) responseTest {
		return func(t *testing.T, r *http.Response) {
			t.Helper()
			if got := r.Header.Get(k); got == "" {
				t.Fatalf("expected header %q to be present, got none", k)
			}
		}
	}
	headerAbsent := func(k string) responseTest {
		return func(t *testing.T, r *http.Response) {
			t.Helper()
			if got := r.Header.Get(k); got != "" {
				t.Fatalf("expected header %q to be absent, got %q", k, got)
			}
		}
	}

	type testCase struct {
		name string
		r    http.Request

		expectedResponseFns     []responseTest
		expectedCorrelationID   string
		expectedClientRequestID string
	}

	tests := []testCase{
		{
			name: "should set the request ID header",
			r:    http.Request{},
			expectedResponseFns: []responseTest{
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
			expectedResponseFns: []responseTest{
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
			expectedResponseFns: []responseTest{
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
			expectedResponseFns: []responseTest{
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
			for _, testFn := range tt.expectedResponseFns {
				testFn(t, writer.Result())
			}

			// Check that the correlation data was found in the next handler.
			assert.NotNil(t, data)
			assert.NotEmpty(t, data.RequestID.String())
			assert.Equal(t, tt.expectedCorrelationID, data.CorrelationRequestID)
			assert.Equal(t, tt.expectedClientRequestID, data.ClientRequestID)

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
