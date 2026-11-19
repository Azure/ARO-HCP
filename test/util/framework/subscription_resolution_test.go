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
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

func TestGetSubscriptionIDRequiresCompleteUnambiguousInventory(t *testing.T) {
	const selected = "11111111-1111-4111-8111-111111111111"
	for _, tt := range []struct {
		name        string
		first       string
		second      string
		secondFails bool
		wantID      string
		wantError   string
	}{
		{name: "match on later page", first: `{"value":[null,{"displayName":"other"}],"nextLink":"https://management.azure.com/subscriptions?page=2"}`, second: `{"value":[{"displayName":"target","subscriptionId":"` + selected + `"}]}`, wantID: selected},
		{name: "duplicate after early match", first: `{"value":[{"displayName":"target","subscriptionId":"` + selected + `"}],"nextLink":"https://management.azure.com/subscriptions?page=2"}`, second: `{"value":[{"displayName":"target","subscriptionId":"22222222-2222-4222-8222-222222222222"}]}`, wantError: "ambiguous"},
		{name: "incomplete inventory after early match", first: `{"value":[{"displayName":"target","subscriptionId":"` + selected + `"}],"nextLink":"https://management.azure.com/subscriptions?page=2"}`, secondFails: true, wantError: "unavailable page"},
		{name: "matched subscription missing ID", first: `{"value":[{"displayName":"target"}]}`, wantError: "no subscription ID"},
		{name: "no match", first: `{"value":[{"displayName":"other","subscriptionId":"` + selected + `"}]}`, wantError: "not found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, err := armsubscriptions.NewClient(&azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodGet {
						t.Fatalf("subscription resolution attempted a write: %s", request.Method)
					}
					if request.URL.Query().Get("page") == "2" {
						if tt.secondFails {
							return nil, errors.New("unavailable page")
						}
						return jobIDResponse(tt.second)
					}
					return jobIDResponse(tt.first)
				}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			id, err := GetSubscriptionID(context.Background(), client, "target")
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) || id != "" {
					t.Fatalf("got ID %q, error %v; want rejection %q", id, err, tt.wantError)
				}
				return
			}
			if err != nil || id != tt.wantID {
				t.Fatalf("got ID %q, error %v; want ID %q", id, err, tt.wantID)
			}
		})
	}
}
