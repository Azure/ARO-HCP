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
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestGuardedNodePoolUsesClusterLock(t *testing.T) {
	manifest := publishedTestManifest(t)
	lock := flock.New(os.Getenv(SharedFixtureManifestEnv) + ".guarded-write.lock")
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Unlock(); err != nil {
			t.Error(err)
		}
	})
	requests := 0
	client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("request escaped fixture lock")
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := client.RejectNodePoolPatch20240610(ctx, map[string]any{}, immutableDNSRejection)
	if !errors.Is(err, context.DeadlineExceeded) || requests != 0 {
		t.Fatalf("node pool guard must use the cluster fixture lock: err=%v, requests=%d", err, requests)
	}
}

func TestProvisionSharedNodePool(t *testing.T) {
	for _, scenario := range []string{"success", "create rejected", "wrong identity", "failed provisioning"} {
		t.Run(scenario, func(t *testing.T) {
			manifest := publishedTestManifest(t)
			creates := 0
			client, err := hcpsdk20240610preview.NewNodePoolsClient(manifest.SubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}, Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					if request.URL.Path != manifest.NodePoolResourceID {
						t.Errorf("wrong node pool path: %s", request.URL.Path)
					}
					if request.Method == http.MethodPut {
						creates++
						var pool hcpsdk20240610preview.NodePool
						if err := json.NewDecoder(request.Body).Decode(&pool); err != nil {
							t.Fatal(err)
						}
						if pool.Properties == nil || pool.Properties.Replicas == nil || *pool.Properties.Replicas != 1 {
							t.Fatalf("shared node pool must request one replica: %+v", pool.Properties)
						}
					} else if request.Method != http.MethodGet {
						t.Fatalf("unexpected operation: %s", request.Method)
					}
					resourceID, state := manifest.NodePoolResourceID, "Succeeded"
					if scenario == "wrong identity" {
						resourceID += "wrong"
					}
					if scenario == "failed provisioning" {
						state = "Failed"
					}
					if scenario == "create rejected" {
						response, err := jobIDResponse(`{"error":{"code":"InvalidRequestContent","message":"rejected"}}`)
						response.StatusCode = http.StatusBadRequest
						return response, err
					}
					response, err := jobIDResponse(fmt.Sprintf(`{"id":%q,"properties":{"provisioningState":%q,"replicas":1}}`, resourceID, state))
					response.Request = request
					return response, err
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			params := NewDefaultNodePoolParams20240610()
			params.NodePoolName, params.VMSize, params.Replicas = manifest.NodePoolName, "Standard_D4s_v3", 1
			err = provisionSharedNodePool20240610(t.Context(), client, manifest, params)
			if (err == nil) != (scenario == "success") || creates != 1 {
				t.Fatalf("provision must create exactly once and validate success: creates=%d, err=%v", creates, err)
			}
		})
	}
}

func TestSharedReadOnlyNodePool(t *testing.T) {
	manifest := publishedTestManifest(t)
	requests := 0
	client, err := hcpsdk20240610preview.NewNodePoolsClient(manifest.SubscriptionID, &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
			requests++
			if request.Method != http.MethodGet || request.URL.Path != manifest.NodePoolResourceID {
				t.Fatalf("read-only borrower accessed unexpected resource: %s %s", request.Method, request.URL)
			}
			return jobIDResponse(fmt.Sprintf(`{"id":%q}`, manifest.NodePoolResourceID))
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := &ReadOnlyClusterClient{manifest: manifest, nodePools: client}
	pool, err := reader.GetNodePool20240610(t.Context())
	if err != nil || pool.ID == nil || *pool.ID != manifest.NodePoolResourceID {
		t.Fatalf("borrowed node pool GET: %+v, %v", pool, err)
	}
	manifest.Ready = false
	if err := WriteSharedClusterManifest(os.Getenv(SharedFixtureManifestEnv), manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetNodePool20240610(t.Context()); err == nil || requests != 1 {
		t.Fatalf("unready fixture must block borrowed GET: %v, requests=%d", err, requests)
	}
}

func TestGuardedNodePoolPatch(t *testing.T) {
	const body = `{"error":{"code":"MultipleErrorsOccurred","message":"2 errors occurred","details":[{"code":"InvalidRequestContent","target":"properties.platform","message":"Forbidden: field is immutable"},{"code":"InvalidRequestContent","target":"properties.platform.osDisk","message":"Forbidden: field is immutable"}]}}`
	for _, scenario := range []string{"unchanged", "accepted", "changed", "wrong status", "wrong root code", "wrong detail code", "wrong target", "wrong reason", "missing details"} {
		t.Run(scenario, func(t *testing.T) {
			manifest := publishedTestManifest(t)
			var methods []string
			client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				if request.URL.Path != manifest.NodePoolResourceID || request.URL.Query().Get("api-version") != "2024-06-10-preview" {
					t.Fatalf("guard must target only the shared node pool: %s", request.URL)
				}
				if request.Method == http.MethodPatch {
					var patch hcpsdk20240610preview.NodePool
					if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
						t.Fatal(err)
					}
					if patch.Properties == nil || patch.Properties.Platform == nil || patch.Properties.Platform.OSDisk == nil || patch.Properties.Platform.OSDisk.SizeGiB == nil || *patch.Properties.Platform.OSDisk.SizeGiB != 128 {
						t.Fatalf("guard did not send the immutable disk-size PATCH: %+v", patch.Properties)
					}
					payload, status := body, http.StatusBadRequest
					switch scenario {
					case "accepted":
						status = http.StatusAccepted
					case "wrong status":
						status = http.StatusConflict
					case "wrong root code":
						payload = strings.ReplaceAll(body, "MultipleErrorsOccurred", "InvalidRequestContent")
					case "wrong detail code":
						payload = strings.ReplaceAll(body, "InvalidRequestContent", "Other")
					case "wrong target":
						payload = strings.ReplaceAll(body, "properties.platform.osDisk", "properties.platform.vmSize")
					case "wrong reason":
						payload = strings.ReplaceAll(body, "field is immutable", "invalid syntax")
					case "missing details":
						payload = `{"error":{"code":"MultipleErrorsOccurred","message":"2 errors occurred"}}`
					}
					response, err := jobIDResponse(payload)
					response.StatusCode = status
					return response, err
				}
				size := 64
				if scenario == "changed" && len(methods) == 3 {
					size = 128
				}
				return jobIDResponse(fmt.Sprintf(`{"id":%q,"properties":{"platform":{"osDisk":{"sizeGiB":%d}}}}`, manifest.NodePoolResourceID, size))
			}})
			err := client.RejectNodePoolPatch20240610(t.Context(), map[string]any{"properties": map[string]any{"platform": map[string]any{"osDisk": map[string]any{"sizeGiB": 128}}}}, ClusterPatchRejection{
				StatusCode: http.StatusBadRequest, Code: "MultipleErrorsOccurred", Message: "2 errors occurred",
				Details: []PatchRejectionDetail{
					{Code: "InvalidRequestContent", Target: "properties.platform", Message: "field is immutable"},
					{Code: "InvalidRequestContent", Target: "properties.platform.osDisk", Message: "field is immutable"},
				},
			})
			if (err == nil) != (scenario == "unchanged") || strings.Join(methods, ",") != "GET,PATCH,GET" {
				t.Fatalf("guarded node pool sequence %v returned %v", methods, err)
			}
			if _, err := ReadSharedClusterManifest(); (err == nil) != (scenario == "unchanged") {
				t.Fatalf("node pool failure must quarantine the entire fixture: %v", err)
			}
		})
	}
}
