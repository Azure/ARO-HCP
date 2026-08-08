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

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

func TestWithSessionProxyClaimHeaderAuthorization(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		owner      sessiongatev1alpha1.Principal
		headers    map[string]string
		wantStatus int
	}{
		{
			name: "azure user missing header",
			owner: sessiongatev1alpha1.Principal{
				Type: sessiongatev1alpha1.PrincipalTypeAzureUser,
				Name: "alice@example.com",
			},
			headers:    map[string]string{},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "azure user empty header",
			owner: sessiongatev1alpha1.Principal{
				Type: sessiongatev1alpha1.PrincipalTypeAzureUser,
				Name: "alice@example.com",
			},
			headers:    map[string]string{"X-JWT-Claim-Upn": ""},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "azure user wrong value",
			owner: sessiongatev1alpha1.Principal{
				Type: sessiongatev1alpha1.PrincipalTypeAzureUser,
				Name: "alice@example.com",
			},
			headers:    map[string]string{"X-JWT-Claim-Upn": "mallory@example.com"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "azure user correct value",
			owner: sessiongatev1alpha1.Principal{
				Type: sessiongatev1alpha1.PrincipalTypeAzureUser,
				Name: "alice@example.com",
			},
			headers:    map[string]string{"X-JWT-Claim-Upn": "alice@example.com"},
			wantStatus: http.StatusOK,
		},
		{
			name: "service principal correct value",
			owner: sessiongatev1alpha1.Principal{
				Type: sessiongatev1alpha1.PrincipalTypeAzureServicePrincipal,
				Name: "00000000-0000-0000-0000-000000000001",
			},
			headers:    map[string]string{"X-JWT-Claim-Oid": "00000000-0000-0000-0000-000000000001"},
			wantStatus: http.StatusOK,
		},
		{
			name: "service principal wrong header name",
			owner: sessiongatev1alpha1.Principal{
				Type: sessiongatev1alpha1.PrincipalTypeAzureServicePrincipal,
				Name: "00000000-0000-0000-0000-000000000001",
			},
			headers:    map[string]string{"X-JWT-Claim-Upn": "00000000-0000-0000-0000-000000000001"},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "unexpected owner type",
			owner: sessiongatev1alpha1.Principal{
				Type: "unknownType",
				Name: "alice@example.com",
			},
			headers:    map[string]string{"X-JWT-Claim-Upn": "alice@example.com"},
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := WithSessionProxyClaimHeaderAuthorization(tt.owner, okHandler)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("got status %d, want %d", rec.Code, tt.wantStatus)
			}

			if tt.name == "azure user missing header" || tt.name == "azure user empty header" {
				const wantBody = "unauthorized: missing claim header\n"
				if gotBody := rec.Body.String(); gotBody != wantBody {
					t.Fatalf("got body %q, want %q", gotBody, wantBody)
				}
			}
		})
	}
}
