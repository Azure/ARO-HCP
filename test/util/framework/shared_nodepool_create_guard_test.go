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
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gofrs/flock"

	"k8s.io/apimachinery/pkg/api/operation"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/validation"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

const negativeReplicasResponse = `{"error":{"code":"InvalidRequestContent","target":"properties.replicas","message":"Invalid value: -1: must be greater than or equal to 0"}}`

func negativeReplicasTestNodePool() hcpsdk20240610preview.NodePool {
	params := NodePoolParams20240610{
		OpenshiftVersionId:     "4.20.8",
		VMSize:                 "Standard_D4s_v3",
		ChannelGroup:           "stable",
		Replicas:               -1,
		OSDiskSizeGiB:          64,
		DiskStorageAccountType: DefaultDiskStorageAccountType,
	}
	return BuildNodePoolFromParams20240610(params, "eastus")
}

func TestNegativeReplicasRPValidation(t *testing.T) {
	encoded, err := json.Marshal(negativeReplicasTestNodePool())
	if err != nil {
		t.Fatal(err)
	}
	external := v20240610preview.NewVersion().NewNodePool(nil)
	if err := json.Unmarshal(encoded, &external); err != nil {
		t.Fatal(err)
	}
	external.ClearReadOnlyFields()
	nodePool, err := external.ConvertToInternal(nil)
	if err != nil {
		t.Fatal(err)
	}
	resourceID, err := azcorearm.ParseResourceID(coreapitesting.TestNodePoolResourceID)
	if err != nil {
		t.Fatal(err)
	}
	nodePool.EnsureDefaults()
	nodePool.TrackedResource = coreapi.NewTrackedResource(resourceID, "eastus")
	nodePool.SetResourceID(resourceID)
	nodePool.SystemData = &coreapi.SystemData{
		CreatedAt:     to.Ptr(time.Now().UTC()),
		CreatedBy:     "test",
		CreatedByType: coreapi.CreatedByTypeUser,
	}
	validationErrors := validation.ValidateNodePool(t.Context(), operation.Operation{Type: operation.Create}, nodePool, nil)
	var cloudError *coreapi.CloudError
	if !errors.As(coreapi.CloudErrorFromFieldErrors(validationErrors), &cloudError) || cloudError.StatusCode != http.StatusBadRequest || cloudError.Code != "InvalidRequestContent" || cloudError.Target != "properties.replicas" || !strings.Contains(cloudError.Message, "must be greater than or equal to 0") || len(validationErrors) != 1 {
		t.Fatalf("negative replicas must produce exactly the expected RP rejection, got %v", validationErrors)
	}
}

func TestGuardedNodePoolCreateContract(t *testing.T) {
	for _, scenario := range []string{"rejected", "wrong target", "accepted", "partial creation", "transport failure", "delayed creation", "delayed accepted", "delayed success", "untyped absence", "owned shared pool", "parent changed"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manifest := publishedTestManifest(t)
				name := "invalid-test"
				present := scenario == "owned shared pool"
				if present {
					name = manifest.NodePoolName
				}
				childID := manifest.ClusterResourceID + "/nodePools/" + name
				writes, deletes, parentReads, childReads := 0, 0, 0, 0
				var putStarted, lastPresent time.Time
				client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					if request.URL.Query().Get("api-version") != "2024-06-10-preview" {
						t.Fatalf("wrong API version: %s", request.URL)
					}
					if request.Method == http.MethodGet && request.URL.Path == manifest.ClusterResourceID {
						parentReads++
						state := "Succeeded"
						if scenario == "parent changed" && parentReads > 1 {
							state = "Updating"
						}
						return jobIDResponse(fmt.Sprintf(`{"id":%q,"properties":{"provisioningState":%q}}`, manifest.ClusterResourceID, state))
					}
					if request.URL.Path != childID {
						t.Fatalf("request escaped unique child: %s %s", request.Method, request.URL)
					}
					switch request.Method {
					case http.MethodPut:
						writes++
						putStarted = time.Now()
						var body map[string]any
						if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						properties := body["properties"].(map[string]any)
						if properties["replicas"] != float64(-1) || properties["platform"].(map[string]any)["vmSize"] != "Standard_D4s_v3" || body["id"] != nil || body["name"] != nil {
							t.Fatalf("incorrect create payload: %v", body)
						}
						switch scenario {
						case "wrong target":
							return externalAuthTestResponse(http.StatusBadRequest, strings.ReplaceAll(negativeReplicasResponse, "properties.replicas", "name"))
						case "accepted":
							present = true
							return externalAuthTestResponse(http.StatusCreated, `{}`)
						case "partial creation":
							present = true
						case "transport failure", "delayed creation":
							return nil, errors.New("connection interrupted")
						case "delayed accepted":
							return externalAuthTestResponse(http.StatusAccepted, `{}`)
						case "delayed success":
							return externalAuthTestResponse(http.StatusOK, `{}`)
						}
						return externalAuthTestResponse(http.StatusBadRequest, negativeReplicasResponse)
					case http.MethodGet:
						childReads++
						if writes > 0 && deletes == 0 && time.Since(putStarted) >= NodePoolCreationTimeout-guardedChildCleanupPollInterval && strings.HasPrefix(scenario, "delayed ") {
							present = true
						}
						if present {
							return jobIDResponse(fmt.Sprintf(`{"id":%q}`, childID))
						}
						if scenario == "untyped absence" && childReads == 2 {
							return nil, errors.New("404 ResourceNotFound")
						}
						return externalAuthTestResponse(http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`)
					case http.MethodDelete:
						deletes++
						lastPresent = time.Now()
						present = false
						return externalAuthTestResponse(http.StatusAccepted, `{}`)
					default:
						t.Fatalf("unexpected method: %s", request.Method)
						return nil, errors.New("unexpected method")
					}
				}})
				err := client.RejectNegativeNodePoolReplicas20240610(t.Context(), name, negativeReplicasTestNodePool())
				if scenario == "owned shared pool" {
					if err == nil || writes != 0 || deletes != 0 || !present {
						t.Fatalf("shared node pool must never be overwritten/deleted: err=%v writes=%d deletes=%d present=%v", err, writes, deletes, present)
					}
					return
				}
				if (err != nil) != (scenario != "rejected") || writes != 1 || parentReads != 2 || childReads < 2 || present {
					t.Fatalf("contract violated: err=%v writes=%d parentReads=%d childReads=%d present=%v", err, writes, parentReads, childReads, present)
				}
				if (scenario == "accepted" || scenario == "partial creation" || strings.HasPrefix(scenario, "delayed ")) != (deletes == 1) {
					t.Fatalf("unexpected cleanup count: %d", deletes)
				}
				if scenario == "rejected" && childReads != 2 {
					t.Fatalf("typed rejection must retain immediate before/after checks: %d reads", childReads)
				}
				if scenario == "transport failure" && (childReads < 3 || time.Since(putStarted) < NodePoolCreationTimeout) {
					t.Fatalf("uncertain PUT must observe the full creation window: %d reads in %v", childReads, time.Since(putStarted))
				}
				if strings.HasPrefix(scenario, "delayed ") && time.Since(lastPresent) < NodePoolCreationTimeout {
					t.Fatalf("delayed child deletion must be followed by stable absence: %v", time.Since(lastPresent))
				}
				if _, err := ReadSharedClusterManifest(); (err != nil) != (scenario != "rejected") {
					t.Fatalf("unexpected quarantine: %v", err)
				}
			})
		})
	}
}

func TestGuardedNodePoolCreateAbsenceWindow(t *testing.T) {
	for _, scenario := range []string{"interrupted absence", "untyped absence", "wrong absence code", "never stable"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manifest := publishedTestManifest(t)
				reads := 0
				client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodGet {
						t.Fatalf("absence checks must not write: %s", request.Method)
					}
					reads++
					if reads == 3 || (scenario == "never stable" && reads%2 == 0) {
						switch scenario {
						case "untyped absence":
							return nil, errors.New("404 ResourceNotFound")
						case "wrong absence code":
							return externalAuthTestResponse(http.StatusNotFound, `{"error":{"code":"SubscriptionNotFound"}}`)
						default:
							return nil, errors.New("transient failure")
						}
					}
					return externalAuthTestResponse(http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`)
				}})
				ctx, cancel := context.WithTimeout(t.Context(), 2*NodePoolCreationTimeout+NodePoolDeletionTimeout)
				defer cancel()
				started := time.Now()
				err := client.cleanupGuardedChild20240610(ctx, manifest.ClusterResourceID+"/nodePools/invalid-test", NodePoolCreationTimeout)
				if scenario == "never stable" {
					if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "last observation") {
						t.Fatalf("intermittent absence must time out with diagnostics: %v", err)
					}
				} else if err != nil || time.Since(started) < NodePoolCreationTimeout+3*guardedChildCleanupPollInterval {
					t.Fatalf("non-typed-absence observations must restart the window: elapsed=%v err=%v", time.Since(started), err)
				}
			})
		})
	}
}

func TestGuardedNodePoolCreateLockAndInput(t *testing.T) {
	manifest := publishedTestManifest(t)
	requests := 0
	client := testGuardedClusterClient(t, manifest, &fakeTransport{do: func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("request escaped guard")
	}})
	for _, replicas := range []*int32{nil, to.Ptr[int32](0), to.Ptr[int32](1)} {
		nodePool := negativeReplicasTestNodePool()
		nodePool.Properties.Replicas = replicas
		if err := client.RejectNegativeNodePoolReplicas20240610(t.Context(), "invalid-test", nodePool); err == nil {
			t.Fatal("guard accepted missing or non-negative replicas")
		}
	}
	lock := flock.New(client.manifestPath + ".guarded-write.lock")
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.Unlock(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := client.RejectNegativeNodePoolReplicas20240610(ctx, "invalid-test", negativeReplicasTestNodePool())
	if !errors.Is(err, context.DeadlineExceeded) || requests != 0 {
		t.Fatalf("CREATE guard must use the fixture lock and reject unsafe inputs: err=%v requests=%d", err, requests)
	}
}
