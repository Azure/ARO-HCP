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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

// TestCrossVersionRoundTrip verifies that GET-then-PUT and PATCH operations
// across API versions preserve all fields, including those unknown to the
// requesting version.
//
// Today v20240610 and v20251223 share all fields; newer versions (v20260630/v20260901) add fields (e.g. ingress).
// Cross-version tests will FAIL when a newer version introduces fields that an older version drops unless
// ConvertToInternal preserves unknown fields (the Classic ARO pattern).
func TestCrossVersionRoundTrip(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	integrationutils.WithAndWithoutCosmos(t, testCrossVersionRoundTrip)
}

const (
	v20240610 = "2024-06-10-preview"
	v20251223 = "2025-12-23-preview"
	v20260630 = "2026-06-30-preview"
	v20260901 = "2026-09-01-preview"
	v20261003 = "2026-10-03-preview"
)

// crossVersionTestEntry pairs a subtest name with its runner function.
// Each subtest gets its own frontend server and unique resource names to avoid conflicts.
type crossVersionTestEntry struct {
	name string
	fn   func(*testing.T, *integrationutils.IntegrationTestInfo, string)
}

// clusterVersionTC specifies a single cluster round-trip scenario.
// createVersion is both the version used to create the cluster and the version
// used for the final GET that verifies field preservation.
// updateVersion is the older version used for the GET-then-PUT or PATCH.
type clusterVersionTC struct {
	name          string
	createVersion string
	updateVersion string
	clusterName   string
}

// clusterPUTRoundTripTests returns tests that verify a GET-then-PUT via an older API
// version preserves all fields introduced by the newer createVersion. Each row creates
// a cluster at createVersion (populating its exclusive fields), GETs and PUTs via
// updateVersion (which strips those fields from the response), then re-GETs at
// createVersion to confirm no data was lost.
func clusterPUTRoundTripTests() []crossVersionTestEntry {
	// name | createVersion (create+verify) | updateVersion (PUT) | clusterName
	tcs := []clusterVersionTC{
		{"Cluster/PUT/v20251223-create-v20251223-put-v20251223-verify", v20251223, v20251223, "xvrt-put-v20251223same"},
		{"Cluster/PUT/v20251223-create-v20240610-put-v20251223-verify", v20251223, v20240610, "xvrt-put-v20251223v20240610"},
		{"Cluster/PUT/v20260630-create-v20260630-put-v20260630-verify", v20260630, v20260630, "xvrt-put-v20260630same"},
		{"Cluster/PUT/v20260630-create-v20240610-put-v20260630-verify", v20260630, v20240610, "xvrt-put-v20260630v20240610"},
		{"Cluster/PUT/v20260630-create-v20251223-put-v20260630-verify", v20260630, v20251223, "xvrt-put-v20260630v20251223"},
		{"Cluster/PUT/v20260901-create-v20260901-put-v20260901-verify", v20260901, v20260901, "xvrt-put-v20260901same"},
		{"Cluster/PUT/v20260901-create-v20240610-put-v20260901-verify", v20260901, v20240610, "xvrt-put-v20260901v20240610"},
		{"Cluster/PUT/v20260901-create-v20251223-put-v20260901-verify", v20260901, v20251223, "xvrt-put-v20260901v20251223"},
		{"Cluster/PUT/v20260901-create-v20260630-put-v20260901-verify", v20260901, v20260630, "xvrt-put-v20260901v20260630"},
		{"Cluster/PUT/v20261003-create-v20261003-put-v20261003-verify", v20261003, v20261003, "xvrt-put-v20261003same"},
		{"Cluster/PUT/v20261003-create-v20240610-put-v20261003-verify", v20261003, v20240610, "xvrt-put-v20261003v20240610"},
		{"Cluster/PUT/v20261003-create-v20251223-put-v20261003-verify", v20261003, v20251223, "xvrt-put-v20261003v20251223"},
		{"Cluster/PUT/v20261003-create-v20260630-put-v20261003-verify", v20261003, v20260630, "xvrt-put-v20261003v20260630"},
		{"Cluster/PUT/v20261003-create-v20260901-put-v20261003-verify", v20261003, v20260901, "xvrt-put-v20261003v20260901"},
	}
	var tests []crossVersionTestEntry
	for _, tc := range tcs {
		tc := tc
		tests = append(tests, crossVersionTestEntry{tc.name, func(t *testing.T, ti *integrationutils.IntegrationTestInfo, sub string) {
			runClusterCrossVersionPUT(t, ti, sub, tc.createVersion, tc.updateVersion, tc.clusterName)
		}})
	}
	return tests
}

// clusterPATCHRoundTripTests returns tests that verify a tag PATCH via an older API
// version preserves all fields introduced by the newer createVersion. Each row creates
// a cluster at createVersion, patches only the tags field via updateVersion, then
// re-GETs at createVersion to confirm no data was lost beyond the changed tags.
func clusterPATCHRoundTripTests() []crossVersionTestEntry {
	// name | createVersion (create+verify) | updateVersion (PATCH) | clusterName
	tcs := []clusterVersionTC{
		{"Cluster/PATCH/v20251223-create-v20251223-patch-v20251223-verify", v20251223, v20251223, "xvrt-patch-v20251223same"},
		{"Cluster/PATCH/v20251223-create-v20240610-patch-v20251223-verify", v20251223, v20240610, "xvrt-patch-v20251223v20240610"},
		{"Cluster/PATCH/v20260630-create-v20260630-patch-v20260630-verify", v20260630, v20260630, "xvrt-patch-v20260630same"},
		{"Cluster/PATCH/v20260630-create-v20240610-patch-v20260630-verify", v20260630, v20240610, "xvrt-patch-v20260630v20240610"},
		{"Cluster/PATCH/v20260630-create-v20251223-patch-v20260630-verify", v20260630, v20251223, "xvrt-patch-v20260630v20251223"},
		{"Cluster/PATCH/v20260901-create-v20260901-patch-v20260901-verify", v20260901, v20260901, "xvrt-patch-v20260901same"},
		{"Cluster/PATCH/v20260901-create-v20240610-patch-v20260901-verify", v20260901, v20240610, "xvrt-patch-v20260901v20240610"},
		{"Cluster/PATCH/v20260901-create-v20251223-patch-v20260901-verify", v20260901, v20251223, "xvrt-patch-v20260901v20251223"},
		{"Cluster/PATCH/v20260901-create-v20260630-patch-v20260901-verify", v20260901, v20260630, "xvrt-patch-v20260901v20260630"},
		{"Cluster/PATCH/v20261003-create-v20261003-patch-v20261003-verify", v20261003, v20261003, "xvrt-patch-v20261003same"},
		{"Cluster/PATCH/v20261003-create-v20240610-patch-v20261003-verify", v20261003, v20240610, "xvrt-patch-v20261003v20240610"},
		{"Cluster/PATCH/v20261003-create-v20251223-patch-v20261003-verify", v20261003, v20251223, "xvrt-patch-v20261003v20251223"},
		{"Cluster/PATCH/v20261003-create-v20260630-patch-v20261003-verify", v20261003, v20260630, "xvrt-patch-v20261003v20260630"},
		{"Cluster/PATCH/v20261003-create-v20260901-patch-v20261003-verify", v20261003, v20260901, "xvrt-patch-v20261003v20260901"},
	}
	var tests []crossVersionTestEntry
	for _, tc := range tcs {
		tc := tc
		tests = append(tests, crossVersionTestEntry{tc.name, func(t *testing.T, ti *integrationutils.IntegrationTestInfo, sub string) {
			runClusterCrossVersionPATCH(t, ti, sub, tc.createVersion, tc.updateVersion, tc.clusterName)
		}})
	}
	return tests
}

// nodepoolExternalAuthRoundTripTests returns tests that verify GET-then-PUT and tag
// PATCH operations via v20240610 preserve all v20251223-exclusive fields on NodePool
// and ExternalAuth resources. These resource types share the same version coverage as
// the cluster tests but require additional parent-cluster and child-resource setup that
// does not fit the generic cluster helpers.
func nodepoolExternalAuthRoundTripTests() []crossVersionTestEntry {
	return []crossVersionTestEntry{
		{
			name: "NodePool/PUT/v20251223-create-v20240610-put-v20251223-verify",
			fn:   testCrossVersionNodePoolPUT,
		},
		{
			name: "NodePool/PATCH/v20251223-create-v20240610-patch-v20251223-verify",
			fn:   testCrossVersionNodePoolPATCH,
		},
		{
			name: "ExternalAuth/PUT/v20251223-create-v20240610-put-v20251223-verify",
			fn:   testCrossVersionExternalAuthPUT,
		},
		{
			name: "ExternalAuth/PATCH/v20251223-create-v20240610-patch-v20251223-verify",
			fn:   testCrossVersionExternalAuthPATCH,
		},
	}
}

func testCrossVersionRoundTrip(t *testing.T, withMock bool) {
	tests := append(append(
		clusterPUTRoundTripTests(),
		clusterPATCHRoundTripTests()...,
	), nodepoolExternalAuthRoundTripTests()...)

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
			subscriptionResourceID := metadataapi.Must(coreapi.ToSubscriptionResourceID(subscriptionID))
			subscriptionJSON := []byte(`{
				"resourceId": "/subscriptions/6b690bec-0c16-4ecb-8f67-781caf40bba7",
				"state": "Registered",
				"registrationDate": "2025-12-19T19:53:15+00:00",
				"properties": null
			}`)
			accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v20251223)
			require.NoError(t, accessor.CreateOrUpdate(ctx, subscriptionResourceID.String(), subscriptionJSON))

			tt.fn(t, testInfo, subscriptionID)
		})
	}
}

func clusterCreatePayload(clusterName, apiVersion string) []byte {
	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"

	switch apiVersion {
	case v20240610:
		// v20240610 payload — omits optional fields (autoscaling, nodeDrainTimeoutMinutes) to test preservation
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
        "customerManaged": {
          "encryptionType": "KMS",
          "kms": {
            "activeKey": {
              "name": "vc-encryption-key",
              "vaultName": "vc-key-vault",
              "version": "2024-12-01-preview"
            }
          }
        },
        "keyManagementMode": "CustomerManaged"
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

	case v20251223:
		// v20251223 payload — includes all optional fields (autoscaling, nodeDrainTimeoutMinutes)
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
        "customerManaged": {
          "encryptionType": "KMS",
          "kms": {
            "activeKey": {
              "name": "vc-encryption-key",
              "version": "2024-12-01-preview"
            },
            "vaultName": "vc-key-vault",
            "visibility": "Public"
          }
        },
        "keyManagementMode": "CustomerManaged"
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
      "subnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
      "vnetIntegrationSubnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/swift-subnet"
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
}`, clusterName, subscriptionID, subscriptionID, subscriptionID))

	case v20260630:
		// v20260630 payload — includes all optional fields (autoscaling, nodeDrainTimeoutMinutes, ingress)
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
        "customerManaged": {
          "encryptionType": "KMS",
          "kms": {
            "activeKey": {
              "name": "vc-encryption-key",
              "version": "2024-12-01-preview"
            },
            "vaultName": "vc-key-vault",
            "visibility": "Public"
          }
        },
        "keyManagementMode": "CustomerManaged"
      }
    },
    "ingress": {
      "type": "Private"
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
      "subnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
      "vnetIntegrationSubnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/swift-subnet"
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
}`, clusterName, subscriptionID, subscriptionID, subscriptionID))

	case v20260901:
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
        "customerManaged": {
          "encryptionType": "KMS",
          "kms": {
            "visibility": "Public",
            "activeKey": {
              "name": "vc-encryption-key",
              "version": "2024-12-01-preview"
            },
            "vaultName": "vc-key-vault"
          }
        },
        "keyManagementMode": "CustomerManaged"
      }
    },
    "ingress": {
      "type": "Private"
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
      "subnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
      "vnetIntegrationSubnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/swift-subnet"
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
}`, clusterName, subscriptionID, subscriptionID, subscriptionID))

	case v20261003:
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
        "customerManaged": {
          "encryptionType": "KMS",
          "kms": {
            "visibility": "Public",
            "keyEncryptionKeyUrl": "https://vc-key-vault.vault.azure.net/keys/vc-encryption-key/2024-12-01-preview"
          }
        },
        "keyManagementMode": "CustomerManaged"
      }
    },
    "ingress": {
      "type": "Private"
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
      "subnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/subnet",
      "vnetIntegrationSubnetId": "/subscriptions/%s/resourceGroups/bar/providers/Microsoft.Network/virtualNetworks/vnet/subnets/swift-subnet"
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
}`, clusterName, subscriptionID, subscriptionID, subscriptionID))

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

	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	createServiceProviderClusterForTesting(t, ctx, testInfo, clusterName, "4.20.8")

	// Deliberately not stamping a ClusterServiceID on the cluster here (unlike
	// createNodePoolAndComplete): cluster updates still synchronously call out to
	// Cluster Service (GetCluster/UpdateCluster) when ServiceProviderProperties.ClusterServiceID
	// is set, and most callers of this helper immediately PUT/PATCH the cluster afterward via
	// a different API version. Child resource helpers stamp the parent cluster's CS ID explicitly
	// before creating node pools or external auths.
}

// createServiceProviderClusterForTesting inserts a serviceProviderCluster document
// to simulate the backend controller populating active_versions.
// Document id must be coreapi.ResourceIDStringToCosmosID(resourceID)
func createServiceProviderClusterForTesting(
	t *testing.T,
	ctx context.Context,
	testInfo *integrationutils.IntegrationTestInfo,
	clusterName string,
	controlPlaneVersion string,
) {
	t.Helper()

	parsedID := metadataapi.Must(azcorearm.ParseResourceID(clusterResourceID(clusterName)))
	subscriptionID := parsedID.SubscriptionID
	resourceGroupName := parsedID.ResourceGroupName

	spcResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s/serviceProviderClusters/default",
		subscriptionID, resourceGroupName, clusterName)

	cosmosID, err := coreapi.ResourceIDStringToCosmosID(spcResourceID)
	require.NoError(t, err)

	spcDoc := map[string]interface{}{
		"id":           cosmosID,
		"partitionKey": subscriptionID,
		"resourceID":   spcResourceID,
		"resourceType": "microsoft.redhatopenshift/hcpopenshiftclusters/serviceproviderclusters",
		"properties": map[string]interface{}{
			"resourceId": spcResourceID,
			"spec": map[string]interface{}{
				"control_plane_version": map[string]interface{}{
					"desired_version": controlPlaneVersion,
				},
			},
			"status": map[string]interface{}{
				"control_plane_version": map[string]interface{}{
					"active_versions": []interface{}{
						map[string]interface{}{
							"version": controlPlaneVersion,
						},
					},
				},
			},
		},
	}

	spcBytes, err := json.Marshal(spcDoc)
	require.NoError(t, err)

	require.NoError(t, testInfo.LoadContent(ctx, spcBytes))
}

// runClusterCrossVersionPUT creates a cluster via createVersion, then performs a
// GET-then-PUT via updateVersion, and verifies that all createVersion fields are
// preserved in a final GET via createVersion.
func runClusterCrossVersionPUT(
	t *testing.T,
	testInfo *integrationutils.IntegrationTestInfo,
	subscriptionID, createVersion, putVersion, clusterName string,
) {
	t.Helper()
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	resourceID := clusterResourceID(clusterName)

	createClusterAndComplete(t, ctx, testInfo, createVersion, subscriptionID, clusterName)
	_, beforeMap := getResourceResponse(t, ctx, testInfo, createVersion, resourceID)

	oldBody, _ := getResourceResponse(t, ctx, testInfo, putVersion, resourceID)

	putAccessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, putVersion)
	require.NoError(t, putAccessor.CreateOrUpdate(ctx, resourceID, oldBody))

	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	_, afterMap := getResourceResponse(t, ctx, testInfo, createVersion, resourceID)

	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (%s GET before %s PUT):\n%s", createVersion, putVersion, prettyJSON(t, beforeMap))
		t.Logf("after (%s GET after %s PUT):\n%s", createVersion, putVersion, prettyJSON(t, afterMap))
		t.Errorf("cross-version PUT data loss: %s GET-then-PUT lost %s fields:\n%s", putVersion, createVersion, diff)
	}
}

// runClusterCrossVersionPATCH creates a cluster via createVersion, then patches tags
// via patchVersion, and verifies that all createVersion fields are preserved in a
// final GET via createVersion.
func runClusterCrossVersionPATCH(
	t *testing.T,
	testInfo *integrationutils.IntegrationTestInfo,
	subscriptionID, createVersion, patchVersion, clusterName string,
) {
	t.Helper()
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	resourceID := clusterResourceID(clusterName)

	createClusterAndComplete(t, ctx, testInfo, createVersion, subscriptionID, clusterName)
	_, beforeMap := getResourceResponse(t, ctx, testInfo, createVersion, resourceID)

	patchBody := []byte(`{"tags": {"patched": "true"}}`)
	patchAccessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, patchVersion)
	require.NoError(t, patchAccessor.Patch(ctx, resourceID, patchBody))

	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	_, afterMap := getResourceResponse(t, ctx, testInfo, createVersion, resourceID)

	afterTags, ok := afterMap["tags"].(map[string]any)
	require.True(t, ok, "PATCH response should have tags")
	require.Contains(t, afterTags, "patched", "PATCH should have added the new tag")
	beforeMap["tags"] = afterMap["tags"]

	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (%s GET before %s PATCH, tags equalized):\n%s", createVersion, patchVersion, prettyJSON(t, beforeMap))
		t.Logf("after (%s GET after %s PATCH):\n%s", createVersion, patchVersion, prettyJSON(t, afterMap))
		t.Errorf("cross-version PATCH data loss: %s PATCH lost %s fields:\n%s", patchVersion, createVersion, diff)
	}
}

func nodePoolCreatePayload(nodePoolName, apiVersion string) []byte {
	subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"

	switch apiVersion {
	case v20240610:
		// v20240610 payload — omits optional fields (osDisk.diskStorageAccountType, nodeDrainTimeoutMinutes) to test preservation
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
      "id": "4.20.8"
    }
  },
  "type": "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools"
}`, nodePoolName, subscriptionID))

	case v20251223:
		// v20251223 payload — includes all optional fields (osDisk.diskStorageAccountType, diskType, nodeDrainTimeoutMinutes)
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
      "id": "4.20.8"
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
	require.NoError(t, integrationutils.StampRandomClusterServiceID(
		ctx,
		testInfo.ResourcesDBClient(),
		clusterResourceID(clusterName),
	))
	accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, apiVersion)
	require.NoError(t, accessor.CreateOrUpdate(ctx, resourceID, nodePoolCreatePayload(nodePoolName, apiVersion)))

	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	// Setting the Cluster Service ID for the node pool is needed until we move all cs interactions to the backend.
	csID, err := integrationutils.CalculateClusterServiceIDFromNodePoolResourceID(ctx, testInfo.ResourcesDBClient(), resourceID)
	require.NoError(t, err)
	require.NoError(t, integrationutils.SetClusterServiceID(ctx, testInfo.ResourcesDBClient(), resourceID, csID))
}

// externalAuthCreatePayload returns the ExternalAuth creation payload.
// v20240610 and v20251223 are currently identical (no version-specific fields yet).
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
	require.NoError(t, integrationutils.StampRandomClusterServiceID(
		ctx,
		testInfo.ResourcesDBClient(),
		clusterResourceID(clusterName),
	))
	accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, apiVersion)
	require.NoError(t, accessor.CreateOrUpdate(ctx, resourceID, externalAuthCreatePayload(apiVersion)))

	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	// Setting the Cluster Service ID for the external auth is needed until we move all cs interactions to the backend.
	csID, err := integrationutils.CalculateClusterServiceIDFromExternalAuthResourceID(ctx, testInfo.ResourcesDBClient(), resourceID)
	require.NoError(t, err)
	require.NoError(t, integrationutils.SetClusterServiceID(ctx, testInfo.ResourcesDBClient(), resourceID, csID))
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

// testCrossVersionNodePoolPUT verifies that a v20240610 GET-then-PUT preserves
// all v20251223 nodepool fields.
func testCrossVersionNodePoolPUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-np-put-v20251223v20240610"
	nodePoolName := "np01"
	resourceID := nodePoolResourceID(clusterName, nodePoolName)

	// Step 1: Create parent cluster via v20251223
	createClusterAndComplete(t, ctx, testInfo, v20251223, subscriptionID, clusterName)

	// Step 2: Create nodepool via v20251223 with all fields populated
	createNodePoolAndComplete(t, ctx, testInfo, v20251223, subscriptionID, clusterName, nodePoolName)

	// Step 3: GET via v20251223 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v20251223, resourceID)

	// Step 4: GET via v20240610 → this drops any v20251223-only fields from the response
	v20240610Body, _ := getResourceResponse(t, ctx, testInfo, v20240610, resourceID)

	// Step 5: PUT via v20240610 using the v20240610 GET response body
	v20240610Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v20240610)
	require.NoError(t, v20240610Accessor.CreateOrUpdate(ctx, resourceID, v20240610Body))

	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	// Step 6: GET via v20251223 → snapshot after the v20240610 round-trip ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v20251223, resourceID)

	// Step 7: Compare — all v20251223 fields should be preserved
	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v20251223 GET before v20240610 PUT):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v20251223 GET after v20240610 PUT):\n%s", prettyJSON(t, afterMap))
		t.Errorf("NodePool cross-version PUT data loss: v20240610 GET-then-PUT lost v20251223 fields:\n%s", diff)
	}
}

// testCrossVersionNodePoolPATCH verifies that a v20240610 PATCH of an unrelated
// nodepool field preserves all v20251223 fields.
func testCrossVersionNodePoolPATCH(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-np-patch-v20251223v20240610"
	nodePoolName := "np01"
	resourceID := nodePoolResourceID(clusterName, nodePoolName)

	// Step 1: Create parent cluster and nodepool via v20251223
	createClusterAndComplete(t, ctx, testInfo, v20251223, subscriptionID, clusterName)
	createNodePoolAndComplete(t, ctx, testInfo, v20251223, subscriptionID, clusterName, nodePoolName)

	// Step 2: GET via v20251223 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v20251223, resourceID)

	// Step 3: PATCH via v20240610 — only change tags (unrelated to v20251223-only fields)
	patchBody := []byte(`{"tags": {"patched": "true"}}`)
	v20240610Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v20240610)
	require.NoError(t, v20240610Accessor.Patch(ctx, resourceID, patchBody))

	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	// Step 4: GET via v20251223 → snapshot after the v20240610 PATCH ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v20251223, resourceID)

	// Step 5: Tags are what we changed — equalize them and compare everything else
	afterTags, ok := afterMap["tags"].(map[string]any)
	require.True(t, ok, "PATCH response should have tags")
	require.Contains(t, afterTags, "patched", "PATCH should have added the new tag")
	beforeMap["tags"] = afterMap["tags"]

	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v20251223 GET before v20240610 PATCH, tags equalized):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v20251223 GET after v20240610 PATCH):\n%s", prettyJSON(t, afterMap))
		t.Errorf("NodePool cross-version PATCH data loss: v20240610 PATCH lost v20251223 fields:\n%s", diff)
	}
}

// testCrossVersionExternalAuthPUT verifies that a v20240610 GET-then-PUT preserves
// all v20251223 external auth fields.
func testCrossVersionExternalAuthPUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-ea-put-v20251223v20240610"
	authName := "default"
	resourceID := externalAuthResourceID(clusterName, authName)

	// Step 1: Create parent cluster via v20251223
	createClusterAndComplete(t, ctx, testInfo, v20251223, subscriptionID, clusterName)

	// Step 2: Create external auth via v20251223
	createExternalAuthAndComplete(t, ctx, testInfo, v20251223, subscriptionID, clusterName, authName)

	// Step 3: GET via v20251223 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v20251223, resourceID)

	// Step 4: GET via v20240610 → this drops any v20251223-only fields from the response
	v20240610Body, _ := getResourceResponse(t, ctx, testInfo, v20240610, resourceID)

	// Step 5: PUT via v20240610 using the v20240610 GET response body
	v20240610Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v20240610)
	require.NoError(t, v20240610Accessor.CreateOrUpdate(ctx, resourceID, v20240610Body))

	// Complete the update operation
	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	// Step 6: GET via v20251223 → snapshot after the v20240610 round-trip ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v20251223, resourceID)

	// Step 7: Compare — all v20251223 fields should be preserved
	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v20251223 GET before v20240610 PUT):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v20251223 GET after v20240610 PUT):\n%s", prettyJSON(t, afterMap))
		t.Errorf("ExternalAuth cross-version PUT data loss: v20240610 GET-then-PUT lost v20251223 fields:\n%s", diff)
	}
}

// testCrossVersionExternalAuthPATCH verifies that a v20240610 PATCH of an
// unrelated field preserves all v20251223 external auth fields.
func testCrossVersionExternalAuthPATCH(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "xvrt-ea-patch-v20251223v20240610"
	authName := "default"
	resourceID := externalAuthResourceID(clusterName, authName)

	// Step 1: Create parent cluster via v20251223
	createClusterAndComplete(t, ctx, testInfo, v20251223, subscriptionID, clusterName)

	// Step 2: Create external auth via v20251223
	createExternalAuthAndComplete(t, ctx, testInfo, v20251223, subscriptionID, clusterName, authName)

	// Step 3: GET via v20251223 → snapshot of all fields ("before")
	_, beforeMap := getResourceResponse(t, ctx, testInfo, v20251223, resourceID)

	// Step 4: PATCH an unrelated field via v20240610
	patchBody := []byte(`{"properties": {"issuer": {"url": "https://patched-issuer.example.com"}}}`)
	v20240610Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v20240610)
	require.NoError(t, v20240610Accessor.Patch(ctx, resourceID, patchBody))

	// Complete the update operation
	parsedID := metadataapi.Must(azcorearm.ParseResourceID(resourceID))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, parsedID.Name))

	// Step 5: GET via v20251223 → snapshot after the v20240610 PATCH ("after")
	_, afterMap := getResourceResponse(t, ctx, testInfo, v20251223, resourceID)

	// Step 6: Equalize the patched field before comparing everything else
	beforeProps, _ := beforeMap["properties"].(map[string]any)
	afterProps, _ := afterMap["properties"].(map[string]any)
	beforeIssuer, _ := beforeProps["issuer"].(map[string]any)
	afterIssuer, _ := afterProps["issuer"].(map[string]any)
	require.Equal(t, "https://patched-issuer.example.com", afterIssuer["url"], "PATCH should have updated the issuer URL")
	beforeIssuer["url"] = afterIssuer["url"]

	// Step 7: Compare — all other v20251223 fields should be preserved
	diff, equals := databasemutationhelpers.ResourceInstanceEquals(t, beforeMap, afterMap)
	if !equals {
		t.Logf("before (v20251223 GET before v20240610 PATCH, ca equalized):\n%s", prettyJSON(t, beforeMap))
		t.Logf("after (v20251223 GET after v20240610 PATCH):\n%s", prettyJSON(t, afterMap))
		t.Errorf("ExternalAuth cross-version PATCH data loss: v20240610 PATCH lost v20251223 fields:\n%s", diff)
	}
}
