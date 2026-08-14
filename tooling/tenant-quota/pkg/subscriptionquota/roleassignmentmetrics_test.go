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

package subscriptionquota

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type roleAssignmentMetricsTestCredential struct{}

func (roleAssignmentMetricsTestCredential) GetToken(
	context.Context,
	policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type roleAssignmentMetricsTestTransport struct {
	do func(*http.Request) (*http.Response, error)
}

func (t *roleAssignmentMetricsTestTransport) Do(req *http.Request) (*http.Response, error) {
	return t.do(req)
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestRoleAssignmentMetricsClientGet(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader(
		`{"roleAssignmentsCurrentCount":123,"roleAssignmentsLimit":8000,"roleAssignmentsRemainingCount":7877}`,
	)}
	transport := &roleAssignmentMetricsTestTransport{
		do: func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet {
				t.Fatalf("request method = %q, want GET", req.Method)
			}
			wantPath := "/subscriptions/subscription-id/providers/Microsoft.Authorization/roleAssignmentsUsageMetrics"
			if req.URL.Path != wantPath {
				t.Fatalf("request path = %q, want %q", req.URL.Path, wantPath)
			}
			if got := req.URL.Query().Get("api-version"); got != roleAssignmentMetricsAPIVersion {
				t.Fatalf("api-version = %q, want %q", got, roleAssignmentMetricsAPIVersion)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       body,
				Request:    req,
			}, nil
		},
	}
	client, err := newRoleAssignmentMetricsClient(
		"subscription-id",
		roleAssignmentMetricsTestCredential{},
		&azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: -1},
		}},
	)
	if err != nil {
		t.Fatalf("newRoleAssignmentMetricsClient() error = %v", err)
	}

	got, err := client.Get(context.Background())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.currentCount != 123 {
		t.Fatalf("currentCount = %d, want 123", got.currentCount)
	}
	if got.limit != 8000 {
		t.Fatalf("limit = %d, want 8000", got.limit)
	}
	if !body.closed {
		t.Fatal("response body was not closed")
	}
}

func TestRoleAssignmentMetricsClientGetErrors(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		body       string
		transport  error
		wantErrSub string
	}{
		{
			name:       "transport error",
			transport:  fmt.Errorf("connection failed"),
			wantErrSub: "get role assignment metrics: connection failed",
		},
		{
			name:       "http error",
			statusCode: http.StatusForbidden,
			body:       `{"error":{"code":"AuthorizationFailed","message":"forbidden"}}`,
			wantErrSub: "AuthorizationFailed",
		},
		{
			name:       "invalid json",
			statusCode: http.StatusOK,
			body:       `{`,
			wantErrSub: "decode role assignment metrics",
		},
		{
			name:       "missing current count",
			statusCode: http.StatusOK,
			body:       `{"roleAssignmentsLimit":8000}`,
			wantErrSub: "missing roleAssignmentsCurrentCount",
		},
		{
			name:       "negative current count",
			statusCode: http.StatusOK,
			body:       `{"roleAssignmentsCurrentCount":-1,"roleAssignmentsLimit":8000}`,
			wantErrSub: "negative roleAssignmentsCurrentCount",
		},
		{
			name:       "missing limit",
			statusCode: http.StatusOK,
			body:       `{"roleAssignmentsCurrentCount":123}`,
			wantErrSub: "missing roleAssignmentsLimit",
		},
		{
			name:       "zero limit",
			statusCode: http.StatusOK,
			body:       `{"roleAssignmentsCurrentCount":123,"roleAssignmentsLimit":0}`,
			wantErrSub: "non-positive roleAssignmentsLimit",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var body *trackingReadCloser
			transport := &roleAssignmentMetricsTestTransport{
				do: func(req *http.Request) (*http.Response, error) {
					if tc.transport != nil {
						return nil, tc.transport
					}
					body = &trackingReadCloser{Reader: strings.NewReader(tc.body)}
					return &http.Response{
						StatusCode: tc.statusCode,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       body,
						Request:    req,
					}, nil
				},
			}
			client, err := newRoleAssignmentMetricsClient(
				"subscription-id",
				roleAssignmentMetricsTestCredential{},
				&azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
					Transport: transport,
					Retry:     policy.RetryOptions{MaxRetries: -1},
				}},
			)
			if err != nil {
				t.Fatalf("newRoleAssignmentMetricsClient() error = %v", err)
			}

			_, err = client.Get(context.Background())
			if err == nil {
				t.Fatal("Get() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("Get() error = %v, want substring %q", err, tc.wantErrSub)
			}
			if tc.transport == nil && !body.closed {
				t.Fatal("response body was not closed")
			}
		})
	}
}
