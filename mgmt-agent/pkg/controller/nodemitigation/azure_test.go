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

package nodemitigation

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type azureTransport func(*http.Request) (*http.Response, error)

func (f azureTransport) Do(request *http.Request) (*http.Response, error) { return f(request) }

type azureTestCredential struct{}

func (azureTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "unit-test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestAzurePoolObservationClock(t *testing.T) {
	const cluster = "/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/management/providers/Microsoft.ContainerService/managedClusters/cluster"
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			clockCalls := 0
			clock := func() time.Time {
				clockCalls++
				return now
			}
			reader := NewAzureClient(azureTestCredential{}, clock).(*azureClient)
			reader.options.Retry.MaxRetries = -1
			reader.options.Transport = azureTransport(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.Path != cluster+"/agentPools/pool" {
					t.Fatalf("unexpected Azure request: %s %s", request.Method, request.URL.Path)
				}
				now = now.Add(3 * time.Second)
				body := `{"id":"` + cluster + `/agentPools/pool","properties":{"count":10,"provisioningState":"Succeeded","mode":"User"}}`
				if status != http.StatusOK {
					body = `{"error":{"code":"AuthorizationFailed","message":"test failure"}}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			observation, err := reader.Pool(context.Background(), cluster, "pool")
			if status != http.StatusOK {
				if err == nil || !observation.ObservedAt.IsZero() || clockCalls != 0 {
					t.Fatalf("failed read produced an observation: %+v, calls=%d, err=%v", observation, clockCalls, err)
				}
				return
			}
			if err != nil || !observation.ObservedAt.Equal(now) || clockCalls != 1 {
				t.Fatalf("observation did not use the clock after the read: %+v, now=%v, calls=%d, err=%v", observation, now, clockCalls, err)
			}
			cfg := testConfig()
			if _, err := updateBaseline(nil, observation, cfg, "test", clock()); err != nil {
				t.Fatalf("shared clock rejected a fresh observation: %v", err)
			}
			now = now.Add(cfg.ObservationMaxAge.Duration + time.Nanosecond)
			if _, err := updateBaseline(nil, observation, cfg, "test", clock()); err == nil {
				t.Fatal("expired observation was accepted")
			}
		})
	}
}

func TestAzureIdentityAndAbsence(t *testing.T) {
	const subscription = "00000000-1111-2222-3333-444444444444"
	cluster := "/subscriptions/" + subscription + "/resourceGroups/management/providers/Microsoft.ContainerService/managedClusters/cluster"
	provider := "azure:///subscriptions/" + subscription + "/resourceGroups/nodes/providers/Microsoft.Compute/virtualMachineScaleSets/pool-vmss/virtualMachines/0"
	for _, tc := range []struct {
		name, failingResource, errorCode, computerName, nodeName, poolTag, group string
		status                                                                   int
		wantError, wantPresent                                                   bool
	}{
		{name: "matching instance", wantPresent: true},
		{name: "missing creation time", wantPresent: true},
		{name: "verified VM absence", failingResource: "vm", status: 404, errorCode: "ResourceNotFound"},
		{name: "compute VM absence", failingResource: "vm", status: 404, errorCode: "NotFound"},
		{name: "verified parent absence", failingResource: "parent", status: 404, errorCode: "ResourceNotFound"},
		{name: "generic parent not found", failingResource: "parent", status: 404, errorCode: "NotFound", wantError: true},
		{name: "authorization failure", failingResource: "vm", status: 403, errorCode: "AuthorizationFailed", wantError: true},
		{name: "not found code without 404", failingResource: "vm", status: 403, errorCode: "NotFound", wantError: true},
		{name: "unknown 404", failingResource: "vm", status: 404, errorCode: "Unknown", wantError: true},
		{name: "missing cluster", failingResource: "cluster", status: 404, errorCode: "ResourceNotFound", wantError: true},
		{name: "generic cluster not found", failingResource: "cluster", status: 404, errorCode: "NotFound", wantError: true},
		{name: "computer name mismatch", computerName: "different", wantError: true},
		{name: "observation sees reused slot", computerName: "different", nodeName: "-", wantPresent: true},
		{name: "wrong pool", poolTag: "different", wantError: true},
		{name: "wrong node resource group", group: "different", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			computer, node, tag, group := "node", "node", "pool", "nodes"
			if tc.computerName != "" {
				computer = tc.computerName
			}

			if tc.nodeName == "-" {
				node = ""
			}
			if tc.poolTag != "" {
				tag = tc.poolTag
			}
			if tc.group != "" {
				group = tc.group
			}
			reader := azureClient{credential: azureTestCredential{}, options: azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}, Transport: azureTransport(func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodGet {
						t.Fatalf("Azure write attempted: %s", request.Method)
					}
					kind := "parent"
					body := `{"tags":{"aks-managed-poolName":"` + tag + `"}}`
					switch {
					case strings.Contains(request.URL.Path, "/managedClusters/"):
						kind, body = "cluster", `{"properties":{"nodeResourceGroup":"`+group+`"}}`
					case strings.Contains(request.URL.Path, "/virtualMachines/"):
						kind, body = "vm", `{"properties":{"vmId":"INSTANCE-ID","osProfile":{"computerName":"`+computer+`"}}}`
						if tc.name != "missing creation time" {
							body = `{"properties":{"vmId":"INSTANCE-ID","timeCreated":"2000-01-01T00:00:00Z","osProfile":{"computerName":"` + computer + `"}}}`
						}
					}
					status := 200
					if tc.failingResource == kind {
						status, body = tc.status, `{"error":{"code":"`+tc.errorCode+`","message":"test failure"}}`
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})},
			}}
			id, present, err := reader.Instance(context.Background(), cluster, "pool", provider, node)
			if (err != nil) != tc.wantError || present != tc.wantPresent {
				t.Fatalf("present=%v error=%v; want present=%v error=%v", present, err, tc.wantPresent, tc.wantError)
			}
			if present && id.ID != "instance-id" {
				t.Fatalf("immutable identity not preserved: %+v", id)
			}
			if present {
				want := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				if tc.name == "missing creation time" {
					want = time.Time{}
				}
				if !id.CreatedAt.Equal(want) {
					t.Fatalf("VM creation time=%v, want=%v", id.CreatedAt, want)
				}
			}
		})
	}
}
