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
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
)

const deletionTestEndpoint = "https://management.example.com"

func newDeletionTestClient(clock func() time.Time) *azureClient {
	client := NewAzureClient(azureTestCredential{}, clock).(*azureClient)
	client.options.Cloud = cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
		cloud.ResourceManager: {Endpoint: deletionTestEndpoint, Audience: deletionTestEndpoint},
	}}
	return client
}

func TestAKSDeletionResumeAndFailures(t *testing.T) {
	const cluster = "/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/management/providers/Microsoft.ContainerService/managedClusters/cluster"
	const pool = cluster + "/agentPools/pool"
	for _, outcome := range []string{"Succeeded", "Failed"} {
		t.Run(outcome, func(t *testing.T) {
			now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
			posts, gets := 0, 0
			reader := newDeletionTestClient(func() time.Time { return now })
			reader.options.Transport = azureTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Scheme != "https" || request.URL.Host != "management.example.com" {
					t.Fatalf("unexpected test endpoint: %s", request.URL)
				}
				headers := http.Header{"Content-Type": []string{"application/json"}}
				status, body := 200, `{}`
				if request.Method == http.MethodPost {
					posts++
					if request.URL.Path != pool+"/deleteMachines" || request.URL.Query().Get("api-version") != "2025-10-01" {
						t.Fatal(request.URL)
					}
					var payload struct {
						MachineNames []string `json:"machineNames"`
					}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if len(payload.MachineNames) != 1 || payload.MachineNames[0] != "machine" {
						t.Fatalf("wrong deletion target: %+v", payload)
					}
					status = 202
					headers.Set("Azure-AsyncOperation", deletionTestEndpoint+"/operations/deletion")
					headers.Set("Retry-After", "60")
				} else {
					gets++
					if request.Method != http.MethodGet || request.URL.Path != "/operations/deletion" {
						t.Fatal(request.Method, request.URL)
					}
					body = `{"status":"` + outcome + `"}`
					if outcome == "Failed" {
						body = `{"status":"Failed","error":{"code":"DeletionFailed","message":"test operation failed"}}`
					}
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			op, err := reader.DeleteMachine(context.Background(), pool, "machine")
			if err != nil || op.Outcome != "Pending" || op.Token == "" || !op.PollAfter.Equal(now.Add(time.Minute)) {
				t.Fatalf("begin: %+v %v", op, err)
			}
			result, err := reader.PollDeletion(context.Background(), pool, op.Token)
			if err != nil || result.Outcome != outcome || posts != 1 || gets != 1 {
				t.Fatalf("resume: %+v %v posts=%d gets=%d", result, err, posts, gets)
			}
		})
	}
}

func TestAKSPollFailurePreservesRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	reader := newDeletionTestClient(func() time.Time { return now })
	posts, gets := 0, 0
	reader.options.Transport = azureTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "management.example.com" {
			t.Fatalf("unexpected test endpoint: %s", request.URL)
		}
		headers := http.Header{"Content-Type": []string{"application/json"}}
		status, body := http.StatusAccepted, `{}`
		switch request.Method {
		case http.MethodPost:
			posts++
			headers.Set("Azure-AsyncOperation", deletionTestEndpoint+"/operations/deletion")
		case http.MethodGet:
			gets++
			status = http.StatusTooManyRequests
			headers.Set("Retry-After", "120")
			body = `{"error":{"code":"TooManyRequests","message":"slow down"}}`
		default:
			t.Fatal(request.Method)
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	const pool = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mgmt/agentPools/pool"
	op, err := reader.DeleteMachine(context.Background(), pool, "machine")
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.PollDeletion(context.Background(), pool, op.Token)
	if err == nil || !result.PollAfter.Equal(now.Add(2*time.Minute)) || posts != 1 || gets != 1 {
		t.Fatalf("poll failure: %+v %v posts=%d gets=%d", result, err, posts, gets)
	}
}

func TestAKSPostIsNotAutomaticallyRetried(t *testing.T) {
	reader := NewAzureClient(azureTestCredential{}, time.Now).(*azureClient)
	calls := 0
	reader.options.Transport = azureTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 500, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"error":{"code":"InternalError","message":"lost outcome"}}`)), Request: request}, nil
	})
	_, err := reader.DeleteMachine(context.Background(), "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mgmt/agentPools/pool", "machine")
	if err == nil || calls != 1 {
		t.Fatalf("POST retried or failure hidden: %d %v", calls, err)
	}
}

func TestMachineMappingUsesResourceIdentity(t *testing.T) {
	const cluster = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/mgmt"
	const resource = "/subscriptions/sub/resourceGroups/nodes/providers/Microsoft.Compute/virtualMachineScaleSets/vmss/virtualMachines/0"
	reader := NewAzureClient(azureTestCredential{}, time.Now).(*azureClient)
	for _, duplicate := range []bool{false, true} {
		reader.options.Transport = azureTransport(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodGet || request.URL.Path != cluster+"/agentPools/pool/machines" {
				t.Fatal(request.URL)
			}
			machine := `{"name":"machine","id":"` + cluster + `/agentPools/pool/machines/machine","properties":{"resourceId":"` + resource + `"}}`
			value := machine
			if duplicate {
				value += "," + machine
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"value":[` + value + `]}`)), Request: request}, nil
		})
		name, err := reader.Machine(context.Background(), cluster, "pool", "azure://"+resource)
		if duplicate && err == nil {
			t.Fatal("duplicate mapping accepted")
		}
		if !duplicate && (err != nil || name != "machine") {
			t.Fatalf("mapping=%s error=%v", name, err)
		}
	}
}
