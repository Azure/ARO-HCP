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
		{name: "verified VM absence", failingResource: "vm", status: 404, errorCode: "ResourceNotFound"},
		{name: "verified parent absence", failingResource: "parent", status: 404, errorCode: "ResourceNotFound"},
		{name: "authorization failure", failingResource: "vm", status: 403, errorCode: "AuthorizationFailed", wantError: true},
		{name: "unknown 404", failingResource: "vm", status: 404, errorCode: "Unknown", wantError: true},
		{name: "missing cluster", failingResource: "cluster", status: 404, errorCode: "ResourceNotFound", wantError: true},
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
			reader := azureReader{credential: azureTestCredential{}, options: azcorearm.ClientOptions{
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
			if present && id != "instance-id" {
				t.Fatalf("immutable identity not preserved: %q", id)
			}
		})
	}
}
