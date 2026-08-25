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

package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type fakeCredential struct{}

func (fakeCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestQueryRange(t *testing.T) {
	start := time.Now().Add(-5 * time.Minute)
	end := time.Now()

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer fake-token" {
				t.Errorf("expected bearer token header, got %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status": "success",
				"data": {
					"resultType": "matrix",
					"result": [
						{"metric": {"foo": "bar"}, "values": [[1700000000, "1"]]}
					]
				}
			}`))
		}))
		defer server.Close()

		resp, err := QueryRange(context.Background(), server.Client(), fakeCredential{}, server.URL, `up`, start, end, "60s")
		if err != nil {
			t.Fatalf("QueryRange returned unexpected error: %v", err)
		}
		if resp.Status != "success" {
			t.Errorf("expected status success, got %q", resp.Status)
		}
		if len(resp.Data.Result) != 1 {
			t.Fatalf("expected 1 result, got %d", len(resp.Data.Result))
		}
		if resp.Data.Result[0].Metric["foo"] != "bar" {
			t.Errorf("expected metric label foo=bar, got %v", resp.Data.Result[0].Metric)
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error detail"))
		}))
		defer server.Close()

		_, err := QueryRange(context.Background(), server.Client(), fakeCredential{}, server.URL, `up`, start, end, "60s")
		if err == nil {
			t.Fatal("expected an error for non-200 response, got nil")
		}
		if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "internal error detail") {
			t.Errorf("expected error to include status code and body, got: %v", err)
		}
	})

	t.Run("malformed JSON body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{not valid json`))
		}))
		defer server.Close()

		_, err := QueryRange(context.Background(), server.Client(), fakeCredential{}, server.URL, `up`, start, end, "60s")
		if err == nil {
			t.Fatal("expected an error for malformed JSON, got nil")
		}
		if !strings.Contains(err.Error(), "failed to parse Prometheus response") {
			t.Errorf("expected parse error, got: %v", err)
		}
	})

	t.Run("prometheus error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status": "error", "errorType": "bad_data", "error": "invalid query"}`))
		}))
		defer server.Close()

		_, err := QueryRange(context.Background(), server.Client(), fakeCredential{}, server.URL, `up`, start, end, "60s")
		if err == nil {
			t.Fatal("expected an error for a prometheus-level error response, got nil")
		}
		if !strings.Contains(err.Error(), "bad_data") || !strings.Contains(err.Error(), "invalid query") {
			t.Errorf("expected error to include errorType and error message, got: %v", err)
		}
	})
}
