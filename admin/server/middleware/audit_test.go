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

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/microsoft/go-otel-audit/audit/base"
	"github.com/microsoft/go-otel-audit/audit/msgs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/utils"
)

type testAuditClient struct {
	messages []msgs.Msg
}

func (t *testAuditClient) Send(ctx context.Context, msg msgs.Msg, options ...base.SendOption) error {
	t.messages = append(t.messages, msg)
	return nil
}

func TestWithAudit(t *testing.T) {
	testCases := []struct {
		name                   string
		headers                http.Header
		statusCode             int
		expectedResult         msgs.OperationResult
		expectedCallerIdentity string
	}{
		{
			name: "success with client principal",
			headers: http.Header{
				ClientPrincipalNameHeader: []string{"test-user@example.com"},
			},
			statusCode:             http.StatusOK,
			expectedResult:         msgs.Success,
			expectedCallerIdentity: "test-user@example.com",
		},
		{
			name: "failure with client principal",
			headers: http.Header{
				ClientPrincipalNameHeader: []string{"test-user@example.com"},
			},
			statusCode:             http.StatusBadRequest,
			expectedResult:         msgs.Failure,
			expectedCallerIdentity: "test-user@example.com",
		},
		{
			name:           "redirect is success",
			statusCode:     http.StatusTemporaryRedirect,
			expectedResult: msgs.Success,
		},
		{
			name:           "500 is failure",
			statusCode:     http.StatusInternalServerError,
			expectedResult: msgs.Failure,
		},
		{
			name:           "no headers",
			headers:        http.Header{},
			statusCode:     http.StatusOK,
			expectedResult: msgs.Success,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			client := &testAuditClient{messages: []msgs.Msg{}}

			writer := httptest.NewRecorder()
			request, err := http.NewRequest("GET", "/admin/helloworld", nil)
			require.NoError(t, err)
			request.RemoteAddr = "10.1.2.3:18586"
			request.Header = tc.headers
			request = request.WithContext(ctx)

			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			})

			handler := WithAudit(client, next)
			handler.ServeHTTP(writer, request)

			assert.Equal(t, tc.statusCode, writer.Result().StatusCode)
			require.Len(t, client.messages, 1)
			record := client.messages[0].Record
			assert.Equal(t, "10.1.2.3", record.CallerIpAddress.String())
			assert.Equal(t, tc.expectedResult, record.OperationResult)
			assert.Equal(t, "GET /admin/helloworld", record.OperationName)
			assert.Equal(t, operationCategoryDescription, record.OperationCategoryDescription)
			assert.Equal(t, operationAccessLevel, record.OperationAccessLevel)

			if tc.expectedResult == msgs.Failure {
				assert.Equal(t, fmt.Sprintf("Status code: %d", tc.statusCode), record.OperationResultDescription)
			}

			if tc.expectedCallerIdentity != "" {
				require.Contains(t, record.CallerIdentities, msgs.UPN)
				require.Len(t, record.CallerIdentities[msgs.UPN], 1)
				assert.Equal(t, tc.expectedCallerIdentity, record.CallerIdentities[msgs.UPN][0].Identity)
				assert.Equal(t, "client principal name", record.CallerIdentities[msgs.UPN][0].Description)
			} else {
				assert.Empty(t, record.CallerIdentities)
			}
		})
	}
}
