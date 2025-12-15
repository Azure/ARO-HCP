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
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/microsoft/go-otel-audit/audit/base"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type testClient struct {
	messages []msgs.Msg
}

func (t *testClient) Send(ctx context.Context, msg msgs.Msg, options ...base.SendOption) error {
	t.messages = append(t.messages, msg)
	return nil
}

func TestMiddlewareAudit(t *testing.T) {
	testCases := []struct {
		name           string
		statusCode     int
		expectedResult msgs.OperationResult
	}{
		{
			name:           "success",
			statusCode:     http.StatusOK,
			expectedResult: msgs.Success,
		},
		{
			name:           "failure",
			statusCode:     http.StatusBadRequest,
			expectedResult: msgs.Failure,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			testClient := testClient{messages: []msgs.Msg{}}

			ctx = utils.ContextWithLogger(ctx, slog.Default())

			writer := httptest.NewRecorder()
			request, err := http.NewRequest("GET", "", bytes.NewReader([]byte{}))
			request.RemoteAddr = "10.1.2.3:18586"
			require.NoError(t, err)
			request = request.WithContext(ctx)

			next := func(w http.ResponseWriter, r *http.Request) {
				request = r // capture modified request
				w.WriteHeader(tc.statusCode)
			}

			newMiddlewareAudit(&testClient).handleRequest(writer, request, next)
			assert.Equal(t, tc.statusCode, writer.Result().StatusCode)
			assert.Len(t, testClient.messages, 1)
			assert.Equal(t, testClient.messages[0].Record.CallerIpAddress.String(), "10.1.2.3")
			assert.Equal(t, testClient.messages[0].Record.OperationResult, tc.expectedResult)
		})
	}
}
