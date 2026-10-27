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

package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

func TestPickSoleEnabledSubscription(t *testing.T) {
	t.Parallel()

	t.Run("exactly one", func(t *testing.T) {
		t.Parallel()
		got, err := pickSoleEnabledSubscription([]string{"sub-a"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "sub-a" {
			t.Fatalf("got %q, want sub-a", got)
		}
	})

	t.Run("none", func(t *testing.T) {
		t.Parallel()
		_, err := pickSoleEnabledSubscription(nil)
		if err == nil || !strings.Contains(err.Error(), "no Enabled") {
			t.Fatalf("expected empty-list error, got %v", err)
		}
	})

	t.Run("multiple", func(t *testing.T) {
		t.Parallel()
		_, err := pickSoleEnabledSubscription([]string{"a", "b"})
		if err == nil || !strings.Contains(err.Error(), "2 Enabled") {
			t.Fatalf("expected multi-sub error, got %v", err)
		}
	})
}

func TestResolveSubscriptionID(t *testing.T) {
	t.Parallel()

	t.Run("ignores disabled and returns sole enabled", func(t *testing.T) {
		t.Parallel()
		got, err := resolveWithTransport(t, func(request *http.Request) (*http.Response, error) {
			return response(request, 200, `{
				"value": [
					{"subscriptionId":"disabled-sub","state":"Disabled"},
					{"subscriptionId":"enabled-sub","state":"Enabled"},
					{"subscriptionId":"deleted-sub","state":"Deleted"}
				]
			}`), nil
		})
		if err != nil || got != "enabled-sub" {
			t.Fatalf("got %q, %v; want enabled-sub", got, err)
		}
	})

	t.Run("rejects multiple enabled across pages", func(t *testing.T) {
		t.Parallel()
		got, err := resolveWithTransport(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Query().Get("page") == "2" {
				return response(request, 200, `{"value":[{"subscriptionId":"enabled-b","state":"Enabled"}]}`), nil
			}
			next := *request.URL
			query := next.Query()
			query.Set("page", "2")
			next.RawQuery = query.Encode()
			return response(request, 200, fmt.Sprintf(`{
				"value":[{"subscriptionId":"enabled-a","state":"Enabled"},{"subscriptionId":"disabled-sub","state":"Disabled"}],
				"nextLink":%q
			}`, next.String())), nil
		})
		if got != "" || err == nil || !strings.Contains(err.Error(), "2 Enabled") {
			t.Fatalf("expected multi-enabled failure with empty ID: got %q, %v", got, err)
		}
	})

	t.Run("returns no ID when a later page fails", func(t *testing.T) {
		t.Parallel()
		got, err := resolveWithTransport(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Query().Get("page") == "2" {
				return response(request, 500, `{"error":{"code":"InternalServerError","message":"boom"}}`), nil
			}
			next := *request.URL
			query := next.Query()
			query.Set("page", "2")
			next.RawQuery = query.Encode()
			return response(request, 200, fmt.Sprintf(`{
				"value":[{"subscriptionId":"enabled-a","state":"Enabled"}],
				"nextLink":%q
			}`, next.String())), nil
		})
		if got != "" || err == nil || !strings.Contains(err.Error(), "listing subscriptions") {
			t.Fatalf("later-page failure must return no ID: got %q, %v", got, err)
		}
	})
}

func resolveWithTransport(t *testing.T, do transport) (string, error) {
	t.Helper()
	options := azureDiscoveryOptions()
	options.Transport = do
	options.Retry = policy.RetryOptions{MaxRetries: 1, RetryDelay: time.Millisecond, MaxRetryDelay: time.Millisecond}
	return resolveSubscriptionID(context.Background(), credential{}, options)
}
