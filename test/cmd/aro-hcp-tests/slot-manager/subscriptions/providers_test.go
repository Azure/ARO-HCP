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

package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

func TestParseProviderNamespaces(t *testing.T) {
	t.Parallel()
	got := ParseProviderNamespaces(" Microsoft.Compute,,Microsoft.Network , Microsoft.Compute,")
	if !slices.Equal(got, []string{"Microsoft.Compute", "Microsoft.Network"}) {
		t.Fatalf("got %v", got)
	}
}

type providerCredential struct{}

func (providerCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "unit-test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type providerTransport func(*http.Request) (*http.Response, error)

func (transport providerTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type providerExchange struct {
	subscription, method, state string
	status                      int
}

func providerTestOptions(t *testing.T, exchanges []providerExchange) *azcorearm.ClientOptions {
	t.Helper()
	index := 0
	t.Cleanup(func() {
		if index != len(exchanges) {
			t.Errorf("completed %d requests, want %d", index, len(exchanges))
		}
	})
	return &azcorearm.ClientOptions{
		DisableRPRegistration: true,
		ClientOptions: azcore.ClientOptions{
			Retry: policy.RetryOptions{MaxRetries: -1},
			Transport: providerTransport(func(request *http.Request) (*http.Response, error) {
				if index >= len(exchanges) {
					t.Fatalf("unexpected provider request: %s %s", request.Method, request.URL)
				}
				exchange := exchanges[index]
				index++
				path := "/subscriptions/" + exchange.subscription + "/providers/Microsoft.Compute"
				if exchange.method == http.MethodPost {
					path += "/register"
				}
				if request.Method != exchange.method || request.URL.Path != path {
					t.Fatalf("request %d: got %s %s, want %s %s", index, request.Method, request.URL.Path, exchange.method, path)
				}
				status := exchange.status
				if status == 0 {
					status = http.StatusOK
				}
				body := fmt.Sprintf(`{"registrationState":%q}`, exchange.state)
				if status != http.StatusOK {
					body = `{"error":{"code":"AuthorizationFailed","message":"permission denied"}}`
				}
				return &http.Response{
					StatusCode: status,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    request,
				}, nil
			}),
		},
	}
}

func TestRegisterProvidersLifecycle(t *testing.T) {
	t.Parallel()
	const subscription = "22222222-2222-2222-2222-222222222222"
	for _, test := range []struct {
		name       string
		exchanges  []providerExchange
		wantStatus int
		cancel     bool
	}{
		{
			name: "already registered never writes",
			exchanges: []providerExchange{
				{subscription: subscription, method: http.MethodGet, state: "Registered"},
			},
		},
		{
			name: "registration completes immediately",
			exchanges: []providerExchange{
				{subscription: subscription, method: http.MethodGet, state: "NotRegistered"},
				{subscription: subscription, method: http.MethodPost, state: "Registered"},
			},
		},
		{
			name: "waits through registering until registered",
			exchanges: []providerExchange{
				{subscription: subscription, method: http.MethodGet, state: "NotRegistered"},
				{subscription: subscription, method: http.MethodPost, state: "Registering"},
				{subscription: subscription, method: http.MethodGet, state: "Registering"},
				{subscription: subscription, method: http.MethodGet, state: "Registered"},
			},
		},
		{
			name: "initial lookup error",
			exchanges: []providerExchange{
				{subscription: subscription, method: http.MethodGet, status: http.StatusForbidden},
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "registration error",
			exchanges: []providerExchange{
				{subscription: subscription, method: http.MethodGet, state: "NotRegistered"},
				{subscription: subscription, method: http.MethodPost, status: http.StatusForbidden},
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "polling error",
			exchanges: []providerExchange{
				{subscription: subscription, method: http.MethodGet, state: "NotRegistered"},
				{subscription: subscription, method: http.MethodPost, state: "Registering"},
				{subscription: subscription, method: http.MethodGet, status: http.StatusForbidden},
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "cancellation interrupts polling",
			exchanges: []providerExchange{
				{subscription: subscription, method: http.MethodGet, state: "NotRegistered"},
				{subscription: subscription, method: http.MethodPost, state: "Registering"},
			},
			cancel: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if test.cancel {
					timer := time.AfterFunc(time.Second, cancel)
					defer timer.Stop()
				}
				err := RegisterProviders(ctx, providerCredential{}, providerTestOptions(t, test.exchanges), []string{subscription}, []string{"Microsoft.Compute"})
				switch {
				case test.cancel:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("want cancellation, got %v", err)
					}
				case test.wantStatus != 0:
					var responseError *azcore.ResponseError
					if !errors.As(err, &responseError) || responseError.StatusCode != test.wantStatus {
						t.Fatalf("want HTTP %d, got %v", test.wantStatus, err)
					}
					if !strings.Contains(err.Error(), subscription) || !strings.Contains(err.Error(), "Microsoft.Compute") {
						t.Fatalf("error lacks subscription and provider context: %v", err)
					}
				case err != nil:
					t.Fatal(err)
				}
			})
		})
	}
}

func TestRegisterProvidersContinuesAcrossSubscriptionsAndReportsAllFailures(t *testing.T) {
	t.Parallel()
	subscriptions := []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
	}
	options := providerTestOptions(t, []providerExchange{
		{subscription: subscriptions[0], method: http.MethodGet, status: http.StatusForbidden},
		{subscription: subscriptions[1], method: http.MethodGet, state: "NotRegistered"},
		{subscription: subscriptions[1], method: http.MethodPost, status: http.StatusForbidden},
		{subscription: subscriptions[2], method: http.MethodGet, state: "NotRegistered"},
		{subscription: subscriptions[2], method: http.MethodPost, state: "Registered"},
	})
	err := RegisterProviders(t.Context(), providerCredential{}, options, subscriptions, []string{"Microsoft.Compute"})
	if err == nil {
		t.Fatal("want aggregated subscription failures")
	}
	for _, subscription := range subscriptions[:2] {
		if !strings.Contains(err.Error(), subscription) {
			t.Errorf("missing failure for subscription %s: %v", subscription, err)
		}
	}
	if strings.Contains(err.Error(), subscriptions[2]) {
		t.Errorf("successful subscription reported as failed: %v", err)
	}
}
