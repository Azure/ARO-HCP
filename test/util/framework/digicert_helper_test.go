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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDigiCertClient(t *testing.T, handler http.HandlerFunc) *DigiCertClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-api-key", r.Header.Get("X-DC-DEVKEY"))
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := NewDigiCertClient("test-api-key")
	require.NoError(t, err)
	client.baseURL = server.URL
	client.pollInterval = time.Millisecond
	return client
}

func TestDigiCertDomainLifecycle(t *testing.T) {
	var adds, submits, deletes atomic.Int32
	client := newTestDigiCertClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /domain":
			assert.Equal(t, "apps.example.com", r.URL.Query().Get("filters[search]"))
			assert.Equal(t, "exact", r.URL.Query().Get("filters[search_criteria]"))
			_, _ = w.Write([]byte(`{"domains":[],"page":{"total":0}}`))
		case "POST /domain", "POST /domain/42/validation":
			var parameters map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&parameters))
			assert.Equal(t, "dns-txt-token", parameters["dcv_method"])
			assert.Equal(t, []any{map[string]any{"type": "ov"}}, parameters["validations"])
			if r.URL.Path == "/domain" {
				adds.Add(1)
				assert.Equal(t, "apps.example.com", parameters["name"])
				assert.Equal(t, map[string]any{"id": float64(123)}, parameters["organization"])
			} else {
				submits.Add(1)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":42,"dcv_token":{"token":"validation-token","expiration_date":"2099-01-01T00:00:00Z"}}`))
		case "DELETE /domain/42":
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	domain, created, err := client.EnsureDomain(t.Context(), "apps.example.com", 123)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, int64(42), domain.ID)
	assert.Equal(t, "validation-token", domain.DCVToken.Token)
	assert.Equal(t, "apps.example.com", domain.Name)
	assert.False(t, domain.OVValidated())
	resubmitted, err := client.SubmitDNSValidation(t.Context(), domain.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(42), resubmitted.ID)
	assert.Equal(t, "validation-token", resubmitted.DCVToken.Token)
	require.NoError(t, client.DeleteDomain(t.Context(), domain.ID))
	assert.Equal(t, int32(1), adds.Load())
	assert.Equal(t, int32(1), submits.Load())
	assert.Equal(t, int32(1), deletes.Load())
}

func TestDigiCertEnsureDomainPreservesExistingEntries(t *testing.T) {
	for _, sameOrganization := range []bool{true, false} {
		t.Run(fmt.Sprintf("same organization=%t", sameOrganization), func(t *testing.T) {
			organizationID := 123
			if !sameOrganization {
				organizationID = 456
			}
			client := newTestDigiCertClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method, "existing domains must not be modified during lookup")
				if r.URL.Path == "/domain" {
					if r.URL.Query().Get("offset") == "0" {
						_, _ = w.Write([]byte(`{"domains":[{"id":1,"name":"other.example.com","organization":{"id":123}}],"page":{"total":2}}`))
					} else {
						_, _ = fmt.Fprintf(w, `{"domains":[{"id":42,"name":"apps.example.com","organization":{"id":%d}}],"page":{"total":2}}`, organizationID)
					}
					return
				}
				assert.Equal(t, "/domain/42", r.URL.Path)
				assert.Equal(t, "true", r.URL.Query().Get("include_validation"))
				assert.Equal(t, "true", r.URL.Query().Get("include_dcv"))
				_, _ = w.Write([]byte(`{"id":42,"name":"apps.example.com","organization":{"id":123},"dcv_expiration_datetime":"2099-01-01T00:00:00Z","validations":[{"type":"ov","status":"active","dcv_status":"complete","validated_until":"2099-01-01T00:00:00Z"}]}`))
			})
			domain, created, err := client.EnsureDomain(t.Context(), "apps.example.com", 123)
			assert.False(t, created, "preexisting domains must not be registered for deletion")
			if !sameOrganization {
				require.ErrorContains(t, err, "refusing to change its organization")
				assert.Nil(t, domain)
				return
			}
			require.NoError(t, err)
			assert.True(t, domain.OVValidated())
		})
	}
}

func TestDigiCertValidationPolling(t *testing.T) {
	for _, scenario := range []string{"complete", "rejected", "timeout", "forbidden", "transient error", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var checks, polls atomic.Int32
			client := newTestDigiCertClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					checks.Add(1)
					assert.Equal(t, "/domain/42/dcv/validate-token", r.URL.Path)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				count := polls.Add(1)
				switch {
				case scenario == "forbidden":
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"errors":[{"code":"access_denied"}]}`))
				case scenario == "rejected":
					_, _ = w.Write([]byte(`{"name":"apps.example.com","validations":[{"type":"ov","status":"rejected","dcv_status":"failed"}]}`))
				case scenario == "canceled":
					cancel()
				case scenario == "transient error" && count == 1:
					w.WriteHeader(http.StatusServiceUnavailable)
				case scenario != "timeout" && count > 1:
					_, _ = w.Write([]byte(`{"name":"apps.example.com","dcv_expiration_datetime":"2099-01-01T00:00:00Z","validations":[{"type":"ov","status":"active","dcv_status":"complete","validated_until":"2099-01-01T00:00:00Z"}]}`))
				default:
					_, _ = w.Write([]byte(`{"name":"apps.example.com","validations":[{"type":"ov","status":"pending","dcv_status":"pending"}]}`))
				}
			})
			err := client.CheckAndWaitForDNSValidation(ctx, 42, 100*time.Millisecond)
			assert.Equal(t, int32(1), checks.Load(), "status polling must not restart validation")
			switch scenario {
			case "complete", "transient error":
				require.NoError(t, err)
				assert.GreaterOrEqual(t, polls.Load(), int32(2))
			case "timeout":
				require.ErrorIs(t, err, context.DeadlineExceeded)
				assert.ErrorContains(t, err, "pending")
			case "canceled":
				require.ErrorIs(t, err, context.Canceled)
			default:
				require.Error(t, err)
				assert.Equal(t, int32(1), polls.Load(), "terminal errors should stop polling")
			}
		})
	}
}

func TestDigiCertValidationExpiration(t *testing.T) {
	for _, test := range []struct {
		name, status, dcvStatus, until, dcvUntil string
		want                                     bool
	}{
		{"current", "active", "complete", "2099-01-01T00:00:00Z", "2099-01-01T00:00:00Z", true},
		{"expired validation", "active", "complete", "2020-01-01T00:00:00Z", "2099-01-01T00:00:00Z", false},
		{"expired DCV", "active", "complete", "2099-01-01T00:00:00Z", "2020-01-01T00:00:00Z", false},
		{"token active but validation pending", "pending", "pending", "2099-01-01T00:00:00Z", "2099-01-01T00:00:00Z", false},
		{"missing expiration", "active", "complete", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			domain := &DigiCertDomain{DCVExpiration: test.dcvUntil, Validations: []digiCertValidation{{Type: "ov", Status: test.status, DCVStatus: test.dcvStatus, ValidatedUntil: test.until}}}
			domain.DCVToken.Token = "a-token-is-not-proof-of-validation"
			assert.Equal(t, test.want, domain.OVValidated())
		})
	}
}

func TestDigiCertErrorsAndCleanup(t *testing.T) {
	client := newTestDigiCertClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid key: test-api-key"}`))
	})
	_, _, err := client.EnsureDomain(t.Context(), "apps.example.com", 123)
	require.ErrorContains(t, err, "HTTP 401")
	assert.NotContains(t, err.Error(), "test-api-key", "API credentials must not appear in diagnostics")
	require.NoError(t, client.DeleteDomain(t.Context(), 42), "already absent test domains should not fail cleanup")
}
