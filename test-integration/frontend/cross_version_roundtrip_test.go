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

package frontend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

// TestCrossVersionRoundTrip verifies that GET-then-PUT and PATCH operations
// across API versions preserve all fields, including those unknown to the
// requesting version.
//
// Today (v2024 and v2025 share all fields) these tests PASS as a baseline.
// When v2025-exclusive fields land, the cross-version tests will FAIL unless
// ConvertToInternal preserves unknown fields (the Classic ARO pattern).
func TestCrossVersionRoundTrip(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	integrationutils.WithAndWithoutCosmos(t, testCrossVersionRoundTrip)
}

const (
	v2024 = "2024-06-10-preview"
	v2025 = "2025-12-23-preview"
)

func testCrossVersionRoundTrip(t *testing.T, withMock bool) {
	// Each subtest gets a unique cluster name and resource ID to avoid conflicts.
	tests := []struct {
		name string
		fn   func(t *testing.T, ti *integrationutils.IntegrationTestInfo, subscriptionID string)
	}{
		{
			name: "Cluster/PUT/v2025-create-v2024-put-v2025-verify",
			fn:   testCrossVersionClusterPUT,
		},
		{
			name: "Cluster/PATCH/v2025-create-v2024-patch-v2025-verify",
			fn:   testCrossVersionClusterPATCH,
		},
		{
			name: "Cluster/PUT/v2025-create-v2025-put-v2025-verify",
			fn:   testSameVersionClusterPUT,
		},
		{
			name: "Cluster/PATCH/v2025-create-v2025-patch-v2025-verify",
			fn:   testSameVersionClusterPATCH,
		},
		{
			name: "NodePool/PUT/v2025-create-v2024-put-v2025-verify",
			fn:   testCrossVersionNodePoolPUT,
		},
		{
			name: "NodePool/PATCH/v2025-create-v2024-patch-v2025-verify",
			fn:   testCrossVersionNodePoolPATCH,
		},
		{
			name: "ExternalAuth/PUT/v2025-create-v2024-put-v2025-verify",
			fn:   testCrossVersionExternalAuthPUT,
		},
		{
			name: "ExternalAuth/PATCH/v2025-create-v2024-patch-v2025-verify",
			fn:   testCrossVersionExternalAuthPATCH,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
			logger := utils.LoggerFromContext(ctx)

			testInfo, err := integrationutils.NewIntegrationTestInfoFromEnv(ctx, t, withMock)
			require.NoError(t, err)
			cleanupCtx := context.Background()
			cleanupCtx = utils.ContextWithLogger(cleanupCtx, integrationutils.DefaultLogger(t))
			defer testInfo.Cleanup(cleanupCtx)

			frontendStarted := atomic.Bool{}
			frontendErrCh := make(chan error, 1)
			defer func() {
				if frontendStarted.Load() {
					require.NoError(t, <-frontendErrCh)
				}
			}()
			adminAPIStarted := atomic.Bool{}
			adminAPIErrCh := make(chan error, 1)
			defer func() {
				if adminAPIStarted.Load() {
					require.NoError(t, <-adminAPIErrCh)
				}
			}()
			defer cancel()
			go func() {
				frontendStarted.Store(true)
				frontendErrCh <- testInfo.Frontend.Run(ctx)
			}()
			go func() {
				adminAPIStarted.Store(true)
				adminAPIErrCh <- testInfo.AdminAPI.Run(ctx)
			}()

			err = wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
				for _, url := range []string{testInfo.FrontendURL, testInfo.AdminURL} {
					resp, err := http.Get(url)
					if err != nil {
						return false, nil
					}
					if closeErr := resp.Body.Close(); closeErr != nil {
						logger.Error(closeErr, "failed to close response body")
					}
				}
				return true, nil
			})
			require.NoError(t, err)

			// Register subscription
			subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
			subscriptionResourceID := api.Must(arm.ToSubscriptionResourceID(subscriptionID))
			subscriptionJSON := []byte(`{
				"resourceId": "/subscriptions/6b690bec-0c16-4ecb-8f67-781caf40bba7",
				"state": "Registered",
				"registrationDate": "2025-12-19T19:53:15+00:00",
				"properties": null
			}`)
			accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2025)
			require.NoError(t, accessor.CreateOrUpdate(ctx, subscriptionResourceID.String(), subscriptionJSON))

			tt.fn(t, testInfo, subscriptionID)
		})
	}
}

func clusterCreatePayload(clusterName, apiVersion string) []byte {
	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"

	switch apiVersion {
	case v2024:
		// v2024 payload — omits optional fields (autoscaling, nodeDrainTimeoutMinutes) to test preservation
		return []byte(fmt.Sprintf(`{
  "identity": {
    "type": "UserAssigned",
    "userAssignedIdentities": {}
  },
  "name": "%s",
  "properties": {
    "api": {
      "visibility": "Public"
    },
    "clusterImageRegistry": {
      "state": "Disabled"
    },
    "etcd": {
      "dataEncryption": {
        "keyManagementMode": "PlatformManaged"
      }
    },
    "network": {
      "hostPrefix": 23,
      "machineCidr": "10.0.0.0/16",
      "networkType": "OVNKubernetes",
      "podCidr": "10.128.0.0/14",
      "serviceCidr": "172.30.0.0/16"
    },
    "platform": {
      "managedResourceGroup": "managed-rg-xvrt",
      "networkSecurityGroupId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/networkSecurityGroups/nsg",
      "outboundType": "LoadBalancer",
      "subnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"
    },
    "version": {
      "channelGroup": "stable",
      "id": "4.20"
    }
  },
  "tags": {
    "env": "test"
  },
  "type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
}`, clusterName, subscriptionID, subscriptionID))

	case v2025:
		// v2025 payload — includes all optional fields (autoscaling, nodeDrainTimeoutMinutes)
		return []byte(fmt.Sprintf(`{
  "identity": {
    "type": "UserAssigned",
    "userAssignedIdentities": {}
  },
  "name": "%s",
  "properties": {
    "api": {
      "visibility": "Public"
    },
    "autoscaling": {
      "maxNodeProvisionTimeSeconds": 1200,
      "maxNodesTotal": 50,
      "maxPodGracePeriodSeconds": 300,
      "podPriorityThreshold": -5
    },
    "clusterImageRegistry": {
      "state": "Disabled"
    },
    "etcd": {
      "dataEncryption": {
        "keyManagementMode": "PlatformManaged"
      }
    },
    "nodeDrainTimeoutMinutes": 15,
    "network": {
      "hostPrefix": 23,
      "machineCidr": "10.0.0.0/16",
      "networkType": "OVNKubernetes",
      "podCidr": "10.128.0.0/14",
      "serviceCidr": "172.30.0.0/16"
    },
    "platform": {
      "managedResourceGroup": "managed-rg-xvrt",
      "networkSecurityGroupId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/networkSecurityGroups/nsg",
      "outboundType": "LoadBalancer",
      "subnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"
    },
    "version": {
      "channelGroup": "stable",
      "id": "4.20"
    }
  },
  "tags": {
    "env": "test"
  },
  "type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters"
}`, clusterName, subscriptionID, subscriptionID))

	default:
		panic(fmt.Sprintf("unsupported apiVersion: %s", apiVersion))
	}
}

func clusterResourceID(clusterName string) string {
	return "/subscriptions/6b690bec-0c16-4ecb-8f67-781caf40bba7/resourceGroups/resourceGroupName/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName
}

// createClusterAndComplete creates a cluster via the given API version
// and marks the creation operation as succeeded.
func createClusterAndComplete(
	t *testing.T,
	ctx context.Context,
	testInfo *integrationutils.IntegrationTestInfo,
	apiVersion, subscriptionID, clusterName string,
) {
	t.Helper()

	resourceID := clusterResourceID(clusterName)
	accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, apiVersion)
	require.NoError(t, accessor.CreateOrUpdate(ctx, resourceID, clusterCreatePayload(clusterName, apiVersion)))

	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))
}

// testCrossVersionClusterPUT verifies that a v2024 GET-then-PUT preserves
// all v2025 cluster fields.
func testCrossVersionClusterPUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-put-cross"
	resourceID := clusterResourceID(clusterName)

	// Step 1: Create cluster via v2025 with all fields populated
	createClusterAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName)

	// Step 2: GET via v2025 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 3: GET via v2024 → this drops any v2025-only fields from the response
	v2024Body, _ := getResourceResponse(t, ctx, testInfo, v2024, resourceID)

	// Step 4: PUT via v2024 using the v2024 GET response body
	v2024Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2024)
	require.NoError(t, v2024Accessor.CreateOrUpdate(ctx, resourceID, v2024Body))

	// Complete the update operation
	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))

	// Step 5: GET via v2025 → snapshot after the v2024 round-trip ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 6: Compare — all v2025 fields should be preserved
	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v2025 GET before v2024 PUT):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v2025 GET after v2024 PUT):\n%s", prettyJSON(t, afterMap))
		t.Errorf("cross-version PUT data loss: v2024 GET-then-PUT lost v2025 fields:\n%s", diff)
	}
}

// testCrossVersionClusterPATCH verifies that a v2024 PATCH of an unrelated
// cluster field preserves all v2025 fields.
func testCrossVersionClusterPATCH(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-patch-cross"
	resourceID := clusterResourceID(clusterName)

	// Step 1: Create cluster via v2025 with all fields populated
	createClusterAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName)

	// Step 2: GET via v2025 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 3: PATCH via v2024 — only change tags (unrelated to v2025-only fields)
	patchBody := []byte(`{"tags": {"patched": "true"}}`)
	v2024Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2024)
	require.NoError(t, v2024Accessor.Patch(ctx, resourceID, patchBody))

	// Complete the update operation
	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))

	// Step 4: GET via v2025 → snapshot after the v2024 PATCH ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 5: Tags are not what we're testing — equalize them and compare
	// all other fields (properties, identity, etc.) for data loss.
	afterTags, ok := afterMap["tags"].(map[string]any)
	require.True(t, ok, "PATCH response should have tags")
	require.Contains(t, afterTags, "patched", "PATCH should have added the new tag")

	beforeMap["tags"] = afterMap["tags"]

	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v2025 GET before v2024 PATCH, tags equalized):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v2025 GET after v2024 PATCH):\n%s", prettyJSON(t, afterMap))
		t.Errorf("cross-version PATCH data loss: v2024 PATCH lost v2025 fields:\n%s", diff)
	}
}

// testSameVersionClusterPUT is a baseline test: v2025 GET-then-PUT should
// be a no-op round-trip. This must always pass.
func testSameVersionClusterPUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-put-same"
	resourceID := clusterResourceID(clusterName)

	// Create and snapshot
	createClusterAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName)
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// GET-then-PUT via same version
	v2025Body, _ := getResourceResponse(t, ctx, testInfo, v2025, resourceID)
	v2025Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2025)
	require.NoError(t, v2025Accessor.CreateOrUpdate(ctx, resourceID, v2025Body))

	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))

	// Verify no data loss
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)
	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before:\n%s", prettyJSON(t, beforeMap))
		t.Logf("after:\n%s", prettyJSON(t, afterMap))
		t.Errorf("same-version PUT round-trip lost data:\n%s", diff)
	}
}

func nodePoolCreatePayload(nodePoolName, apiVersion string) []byte {
	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"

	switch apiVersion {
	case v2024:
		// v2024 payload — omits optional fields (osDisk.diskStorageAccountType, nodeDrainTimeoutMinutes) to test preservation
		return []byte(fmt.Sprintf(`{
  "name": "%s",
  "properties": {
    "autoRepair": true,
    "autoScaling": {
      "min": 1,
      "max": 5
    },
    "labels": [
      {
        "key": "env",
        "value": "test"
      }
    ],
    "platform": {
      "vmSize": "Standard_D4s_v3",
      "availabilityZone": "1",
      "subnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"
    },
    "taints": [
      {
        "effect": "NoExecute",
        "key": "dedicated",
        "value": "gpu"
      }
    ],
    "version": {
      "channelGroup": "stable",
      "id": "4.20"
    }
  },
  "type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools"
}`, nodePoolName, subscriptionID))

	case v2025:
		// v2025 payload — includes all optional fields (osDisk.diskStorageAccountType, diskType, nodeDrainTimeoutMinutes)
		return []byte(fmt.Sprintf(`{
  "name": "%s",
  "properties": {
    "autoRepair": true,
    "autoScaling": {
      "min": 1,
      "max": 5
    },
    "labels": [
      {
        "key": "env",
        "value": "test"
      }
    ],
    "nodeDrainTimeoutMinutes": 15,
    "platform": {
      "vmSize": "Standard_D4s_v3",
      "availabilityZone": "1",
      "osDisk": {
        "sizeGiB": 128,
        "diskStorageAccountType": "Premium_LRS",
        "diskType": "Managed"
      },
      "subnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet"
    },
    "taints": [
      {
        "effect": "NoExecute",
        "key": "dedicated",
        "value": "gpu"
      }
    ],
    "version": {
      "channelGroup": "stable",
      "id": "4.20"
    }
  },
  "type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools"
}`, nodePoolName, subscriptionID))

	default:
		panic(fmt.Sprintf("unsupported apiVersion: %s", apiVersion))
	}
}

func nodePoolResourceID(clusterName, nodePoolName string) string {
	return clusterResourceID(clusterName) + "/nodePools/" + nodePoolName
}

// createNodePoolAndComplete creates a nodepool on an existing cluster.
func createNodePoolAndComplete(
	t *testing.T,
	ctx context.Context,
	testInfo *integrationutils.IntegrationTestInfo,
	apiVersion, subscriptionID, clusterName, nodePoolName string,
) {
	t.Helper()

	resourceID := nodePoolResourceID(clusterName, nodePoolName)
	accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, apiVersion)
	require.NoError(t, accessor.CreateOrUpdate(ctx, resourceID, nodePoolCreatePayload(nodePoolName, apiVersion)))

	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))
}

// externalAuthCreatePayload returns the ExternalAuth creation payload.
// v2024 and v2025 are currently identical (no version-specific fields yet).
// When version-specific fields are added, convert to a switch like clusterCreatePayload.
func externalAuthCreatePayload(_ string) []byte {
	return []byte(`{
		"name": "default",
		"properties": {
			"claim": {
				"mappings": {
					"groups": {
						"claim": "groups"
					},
					"username": {
						"claim": "sub",
						"prefix": "prefix-",
						"prefixPolicy": "Prefix"
					}
				}
			},
			"clients": [
				{
					"clientId": "87654321-4321-4321-4321-abcdefghijkl",
					"component": {
						"authClientNamespace": "openshift-console",
						"name": "console"
					},
					"type": "Confidential"
				}
			],
			"issuer": {
				"audiences": [
					"87654321-4321-4321-4321-abcdefghijkl"
				],
				"url": "https://login.microsoftonline.com/12345678-1234-1234-1234-123456789abc/v2.0"
			}
		},
		"type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths"
	}`)
}

func externalAuthResourceID(clusterName, authName string) string {
	return clusterResourceID(clusterName) + "/externalAuths/" + authName
}

// createExternalAuthAndComplete creates an external auth on an existing cluster.
func createExternalAuthAndComplete(
	t *testing.T,
	ctx context.Context,
	testInfo *integrationutils.IntegrationTestInfo,
	apiVersion, subscriptionID, clusterName, authName string,
) {
	t.Helper()

	resourceID := externalAuthResourceID(clusterName, authName)
	accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, apiVersion)
	require.NoError(t, accessor.CreateOrUpdate(ctx, resourceID, externalAuthCreatePayload(apiVersion)))

	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))
}

// getResourceResponse returns the resource GET response as raw JSON bytes and
// as a parsed map for comparison. Works for any resource type.
func getResourceResponse(
	t *testing.T,
	ctx context.Context,
	testInfo *integrationutils.IntegrationTestInfo,
	apiVersion, resourceID string,
) ([]byte, map[string]any) {
	t.Helper()

	accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, apiVersion)
	result, err := accessor.Get(ctx, resourceID)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "GET response should be a map")

	resultBytes, err := json.Marshal(resultMap)
	require.NoError(t, err)

	return resultBytes, resultMap
}

// testCrossVersionNodePoolPUT verifies that a v2024 GET-then-PUT preserves
// all v2025 nodepool fields.
func testCrossVersionNodePoolPUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-np-put-cross"
	nodePoolName := "np01"
	resourceID := nodePoolResourceID(clusterName, nodePoolName)

	// Step 1: Create parent cluster via v2025
	createClusterAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName)

	// Step 2: Create nodepool via v2025 with all fields populated
	createNodePoolAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName, nodePoolName)

	// Step 3: GET via v2025 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 4: GET via v2024 → this drops any v2025-only fields from the response
	v2024Body, _ := getResourceResponse(t, ctx, testInfo, v2024, resourceID)

	// Step 5: PUT via v2024 using the v2024 GET response body
	v2024Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2024)
	require.NoError(t, v2024Accessor.CreateOrUpdate(ctx, resourceID, v2024Body))

	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))

	// Step 6: GET via v2025 → snapshot after the v2024 round-trip ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 7: Compare — all v2025 fields should be preserved
	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v2025 GET before v2024 PUT):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v2025 GET after v2024 PUT):\n%s", prettyJSON(t, afterMap))
		t.Errorf("NodePool cross-version PUT data loss: v2024 GET-then-PUT lost v2025 fields:\n%s", diff)
	}
}

// testCrossVersionNodePoolPATCH verifies that a v2024 PATCH of an unrelated
// nodepool field preserves all v2025 fields.
func testCrossVersionNodePoolPATCH(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-np-patch-cross"
	nodePoolName := "np01"
	resourceID := nodePoolResourceID(clusterName, nodePoolName)

	// Step 1: Create parent cluster and nodepool via v2025
	createClusterAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName)
	createNodePoolAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName, nodePoolName)

	// Step 2: GET via v2025 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 3: PATCH via v2024 — only change tags (unrelated to v2025-only fields)
	patchBody := []byte(`{"tags": {"patched": "true"}}`)
	v2024Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2024)
	require.NoError(t, v2024Accessor.Patch(ctx, resourceID, patchBody))

	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))

	// Step 4: GET via v2025 → snapshot after the v2024 PATCH ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 5: Tags are what we changed — equalize them and compare everything else
	afterTags, ok := afterMap["tags"].(map[string]any)
	require.True(t, ok, "PATCH response should have tags")
	require.Contains(t, afterTags, "patched", "PATCH should have added the new tag")
	beforeMap["tags"] = afterMap["tags"]

	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v2025 GET before v2024 PATCH, tags equalized):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v2025 GET after v2024 PATCH):\n%s", prettyJSON(t, afterMap))
		t.Errorf("NodePool cross-version PATCH data loss: v2024 PATCH lost v2025 fields:\n%s", diff)
	}
}

// testCrossVersionExternalAuthPUT verifies that a v2024 GET-then-PUT preserves
// all v2025 external auth fields.
func testCrossVersionExternalAuthPUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-ea-put-cross"
	authName := "default"
	resourceID := externalAuthResourceID(clusterName, authName)

	// Step 1: Create parent cluster via v2025
	createClusterAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName)

	// Step 2: Create external auth via v2025
	createExternalAuthAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName, authName)

	// Step 3: GET via v2025 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 4: GET via v2024 → this drops any v2025-only fields from the response
	v2024Body, _ := getResourceResponse(t, ctx, testInfo, v2024, resourceID)

	// Step 5: PUT via v2024 using the v2024 GET response body
	v2024Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2024)
	require.NoError(t, v2024Accessor.CreateOrUpdate(ctx, resourceID, v2024Body))

	// Complete the update operation
	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))

	// Step 6: GET via v2025 → snapshot after the v2024 round-trip ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 7: Compare — all v2025 fields should be preserved
	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v2025 GET before v2024 PUT):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v2025 GET after v2024 PUT):\n%s", prettyJSON(t, afterMap))
		t.Errorf("ExternalAuth cross-version PUT data loss: v2024 GET-then-PUT lost v2025 fields:\n%s", diff)
	}
}

// testCrossVersionExternalAuthPATCH verifies that a v2024 PATCH of an
// unrelated field preserves all v2025 external auth fields.
func testCrossVersionExternalAuthPATCH(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-ea-patch-cross"
	authName := "default"
	resourceID := externalAuthResourceID(clusterName, authName)

	// Step 1: Create parent cluster via v2025
	createClusterAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName)

	// Step 2: Create external auth via v2025
	createExternalAuthAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName, authName)

	// Step 3: GET via v2025 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 4: PATCH an unrelated field via v2024
	patchBody := []byte(`{"properties": {"issuer": {"url": "https://patched-issuer.example.com"}}}`)
	v2024Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2024)
	require.NoError(t, v2024Accessor.Patch(ctx, resourceID, patchBody))

	// Complete the update operation
	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))

	// Step 5: GET via v2025 → snapshot after the v2024 PATCH ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// Step 6: Equalize the patched field before comparing everything else
	beforeProps, _ := beforeMap["properties"].(map[string]any)
	afterProps, _ := afterMap["properties"].(map[string]any)
	beforeIssuer, _ := beforeProps["issuer"].(map[string]any)
	afterIssuer, _ := afterProps["issuer"].(map[string]any)
	require.Equal(t, "https://patched-issuer.example.com", afterIssuer["url"], "PATCH should have updated the issuer URL")
	beforeIssuer["url"] = afterIssuer["url"]

	// Step 7: Compare — all other v2025 fields should be preserved
	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v2025 GET before v2024 PATCH, ca equalized):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v2025 GET after v2024 PATCH):\n%s", prettyJSON(t, afterMap))
		t.Errorf("ExternalAuth cross-version PATCH data loss: v2024 PATCH lost v2025 fields:\n%s", diff)
	}
}

// testSameVersionClusterPATCH is a baseline test: v2025 PATCH of tags should
// not affect any other fields. This must always pass.
func testSameVersionClusterPATCH(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-patch-same"
	resourceID := clusterResourceID(clusterName)

	// Create and snapshot
	createClusterAndComplete(t, ctx, testInfo, v2025, subscriptionID, clusterName)
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	// PATCH tags via same version
	patchBody := []byte(`{"tags": {"patched": "true"}}`)
	v2025Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2025)
	require.NoError(t, v2025Accessor.Patch(ctx, resourceID, patchBody))

	parsedID := api.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.CosmosClient(), subscriptionID, parsedID.Name))

	// Verify tags were updated and all other fields are unchanged
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2025, resourceID)

	afterTags, ok := afterMap["tags"].(map[string]any)
	require.True(t, ok, "PATCH response should have tags")
	require.Contains(t, afterTags, "patched", "PATCH should have added the new tag")

	// Equalize tags for comparison of everything else
	beforeMap["tags"] = afterMap["tags"]

	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (tags equalized):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after:\n%s", prettyJSON(t, afterMap))
		t.Errorf("same-version PATCH lost non-tag data:\n%s", diff)
	}
}
