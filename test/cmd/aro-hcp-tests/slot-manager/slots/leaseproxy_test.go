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

package slots

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAcquireAndReleaseLease(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lease/acquire":
			if got := r.URL.Query().Get("type"); got != "aro-hcp-dev-westus3-slot" {
				t.Fatalf("unexpected lease type %q", got)
			}
			if got := r.URL.Query().Get("count"); got != "1" {
				t.Fatalf("unexpected lease count %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"names": []string{"aro-hcp-dev-westus3-slot-00"},
			})
		case "/lease/release":
			var requestBody map[string][]string
			if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
				t.Fatalf("expected release request body to decode: %v", err)
			}
			if len(requestBody["names"]) != 1 || requestBody["names"][0] != "aro-hcp-dev-westus3-slot-00" {
				t.Fatalf("unexpected release names %v", requestBody["names"])
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	name, err := AcquireLease(context.Background(), server.URL, "aro-hcp-dev-westus3-slot", DefaultLeaseProxyTimeout)
	if err != nil {
		t.Fatalf("expected lease acquire to succeed: %v", err)
	}
	if name != "aro-hcp-dev-westus3-slot-00" {
		t.Fatalf("unexpected acquired name %q", name)
	}

	if err := ReleaseLease(context.Background(), server.URL, name, DefaultLeaseProxyTimeout); err != nil {
		t.Fatalf("expected lease release to succeed: %v", err)
	}
}

func TestAcquireLeaseRetriesRetryableStatus(t *testing.T) {
	t.Parallel()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"names": []string{"aro-hcp-dev-westus3-slot-00"},
		})
	}))
	defer server.Close()

	name, err := AcquireLease(context.Background(), server.URL, "aro-hcp-dev-westus3-slot", 5*time.Second)
	if err != nil {
		t.Fatalf("expected lease acquire retry to succeed: %v", err)
	}
	if name != "aro-hcp-dev-westus3-slot-00" {
		t.Fatalf("unexpected acquired name %q", name)
	}
	if attempts != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", attempts)
	}
}

func TestAcquireLeaseRejectsUnexpectedStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	if _, err := AcquireLease(context.Background(), server.URL, "missing-type", DefaultLeaseProxyTimeout); err == nil {
		t.Fatalf("expected lease acquire to fail for non-2xx status")
	}
}

func TestAcquireLeaseClassifiesPoolExhaustionWithoutRetry(t *testing.T) {
	t.Parallel()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`Failed to acquire lease "aro-hcp-dev-westus3-slot": resource not found`))
	}))
	defer server.Close()

	_, err := AcquireLease(context.Background(), server.URL, "aro-hcp-dev-westus3-slot", 5*time.Second)
	if err == nil {
		t.Fatal("expected lease acquire to fail for an exhausted pool")
	}
	if !errors.Is(err, ErrLeasePoolExhausted) {
		t.Fatalf("expected exhausted-pool error classification, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 attempt for exhausted pool, got %d", attempts)
	}
}

func TestAcquireLeaseDoesNotClassifyTypeNotFoundAsPoolExhausted(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`Failed to acquire lease "missing-type": resource type not found`))
	}))
	defer server.Close()

	_, err := AcquireLease(context.Background(), server.URL, "missing-type", DefaultLeaseProxyTimeout)
	if err == nil {
		t.Fatal("expected lease acquire to fail for missing resource type")
	}
	if errors.Is(err, ErrLeasePoolExhausted) {
		t.Fatalf("expected missing resource type to remain a hard failure, got %v", err)
	}
}

func TestAcquireLeaseRejectsMultipleReturnedNames(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"names": []string{"aro-hcp-dev-westus3-slot-00", "aro-hcp-dev-westus3-slot-01"},
		})
	}))
	defer server.Close()

	if _, err := AcquireLease(context.Background(), server.URL, "aro-hcp-dev-westus3-slot", DefaultLeaseProxyTimeout); err == nil {
		t.Fatalf("expected lease acquire to fail when the proxy returns multiple names")
	}
}
