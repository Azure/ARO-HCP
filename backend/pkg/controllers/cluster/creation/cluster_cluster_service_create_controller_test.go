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

package creation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// Test constants
const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testTenantID            = "11111111-1111-1111-1111-111111111111"
	testClusterUID          = "00000000-0000-0000-0000-000000000000"
	// testManagedResourceGroup must match what coreapitesting.MinimumValidClusterTestCase() sets.
	testManagedResourceGroup = "testManagedResourceGroup"
	// testStampIdentifier / testProvisionShardID drive the management-cluster
	// placement + provision-shard-pinning fixtures.
	testStampIdentifier  = "1"
	testProvisionShardID = "shard-abc123"
)

// testManagementClusterResourceID returns the resource ID of the placed management cluster.
func testManagementClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(fleetapihelpers.ToManagementClusterResourceID(testStampIdentifier))
}

// newTestManagementCluster returns a management cluster carrying the CS provision
// shard used by the provision-shard-pinning tests.
func newTestManagementCluster() *fleetapi.ManagementCluster {
	resourceID := testManagementClusterResourceID()
	return &fleetapi.ManagementCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: testStampIdentifier},
		Status: fleetapi.ManagementClusterStatus{
			PublicDNSZoneResourceID:        metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/dns/providers/Microsoft.Network/dnsZones/example.com")),
			ClusterServiceProvisionShardID: ptr.To(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/provision_shards/" + testProvisionShardID))),
		},
	}
}

// testClusterResourceID builds the ARM resource ID for the test cluster.
func testClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
}

// newTestCluster returns a Cluster based on MinimumValidClusterTestCase with
// test-constant IDs. Callers can further customize it via functional opts.
// MinimumValidClusterTestCase is used as the base because createClusterServiceCluster
// calls ocm.BuildCSCluster, which requires a fully-populated cluster (version, DNS, subnet, etc.).
func newTestCluster(opts ...func(*coreapi.Cluster)) *coreapi.Cluster {
	rid := testClusterResourceID()
	cluster := coreapitesting.MinimumValidClusterTestCase()
	cluster.CosmosMetadata = coreapi.CosmosMetadata{
		ResourceID:   rid,
		PartitionKey: strings.ToLower(rid.SubscriptionID),
	}
	cluster.ID = rid
	cluster.Name = testClusterName
	cluster.Type = rid.ResourceType.String()
	cluster.ServiceProviderProperties.ClusterServiceID = nil
	cluster.ServiceProviderProperties.PendingClusterServiceID = nil
	cluster.ServiceProviderProperties.ClusterUID = testClusterUID
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

// newTestSubscription returns a minimal Subscription with tenant ID set.
func newTestSubscription() *coreapi.Subscription {
	rid := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		Properties: &coreapi.SubscriptionProperties{TenantId: ptr.To(testTenantID)},
	}
}

// newTestSPC returns a ServiceProviderCluster for the test cluster.
// Callers can customize it via functional opts.
func newTestSPC(opts ...func(*coreapi.ServiceProviderCluster)) *coreapi.ServiceProviderCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
		testClusterResourceID().String(),
		coreapi.ServiceProviderClusterResourceTypeName,
		coreapi.ServiceProviderClusterResourceName,
	)))
	spc := &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID},
		Spec:           coreapi.ServiceProviderClusterSpec{},
	}
	spc.SetPartitionKey(testSubscriptionID)
	for _, opt := range opts {
		opt(spc)
	}
	return spc
}

func setDenyAssignmentsCreated(spc *coreapi.ServiceProviderCluster) {
	spc.Status.AzureResources.DenyAssignments.AzureResources = []coreapi.DenyAssignmentReference{{
		DenyAssignmentType:       "resources-deny-assignment",
		DenyAssignmentResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/testManagedResourceGroup/providers/Microsoft.Authorization/denyAssignments/00000000-0000-0000-0000-000000000001")),
	}}
	recheckTime := metav1.Now()
	spc.Status.AzureResources.DenyAssignments.EarliestRecheckTime = &recheckTime
}

// setStorageAccountNetworkAccessValidationPassed marks the
// StorageAccountNetworkAccessValidation condition as Passed, satisfying the
// createPreconditionStorageAccountNetworkAccessValidationPassed gate.
func setStorageAccountNetworkAccessValidationPassed(spc *coreapi.ServiceProviderCluster) {
	meta.SetStatusCondition(&spc.Status.Validations, metav1.Condition{
		Type:   validationutils.StorageAccountNetworkAccessValidationName,
		Status: metav1.ConditionTrue,
		Reason: "AsExpected",
	})
}

// setStorageAccountNetworkAccessValidationFailed marks the
// StorageAccountNetworkAccessValidation condition as Failed, as it would be once the
// validation controller detects a conflicting Azure Policy assignment.
func setStorageAccountNetworkAccessValidationFailed(spc *coreapi.ServiceProviderCluster) {
	meta.SetStatusCondition(&spc.Status.Validations, metav1.Condition{
		Type:    validationutils.StorageAccountNetworkAccessValidationName,
		Status:  metav1.ConditionFalse,
		Reason:  "StorageAccountPublicNetworkAccessDenied",
		Message: "a conflicting Azure Policy assignment denies Storage Account public network access",
	})
}

// setStorageAccountNetworkAccessValidationUnknown marks the
// StorageAccountNetworkAccessValidation condition as Unknown, as it would be while the
// validation controller is still inconclusive (e.g. a transient Azure API error).
func setStorageAccountNetworkAccessValidationUnknown(spc *coreapi.ServiceProviderCluster) {
	meta.SetStatusCondition(&spc.Status.Validations, metav1.Condition{
		Type:    validationutils.StorageAccountNetworkAccessValidationName,
		Status:  metav1.ConditionUnknown,
		Reason:  "InternalError",
		Message: "unable to verify Storage Account network access policy compliance",
	})
}

func TestClusterClusterServiceCreate_SyncOnce(t *testing.T) {
	desiredVersion := ptr.To(semver.MustParse("4.20.0"))
	clusterInternalID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))
	pendingClusterServiceID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))

	tests := []struct {
		name                           string
		listCluster                    *coreapi.Cluster                // cluster seeded into the lister (nil = not found)
		dbCluster                      *coreapi.Cluster                // cluster stored in the DB
		existingServiceProviderCluster *coreapi.ServiceProviderCluster // nil = not pre-seeded; controller get-or-creates
		managementClusters             []*fleetapi.ManagementCluster   // seeded into the fleet lister
		denyAssignmentsDisabled        bool                            // simulates an environment without a real FPA
		setupMockCS                    func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec
		expectError                    bool
		verifyDB                       func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name: "successful sync dispatches using persisted DNS names",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
				c.ServiceProviderProperties.DNS = coreapi.ServiceProviderDNSProfile{BaseDomain: "abcde.example.com"}
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				spc.Spec.ManagementClusterResourceID = testManagementClusterResourceID()
				setDenyAssignmentsCreated(spc)
				setStorageAccountNetworkAccessValidationPassed(spc)
			}),
			managementClusters: []*fleetapi.ManagementCluster{newTestManagementCluster()},
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().
					ListClusters(gomock.Any()).
					Return(ocm.NewSimpleClusterListIterator(nil, nil))
				mockCS.EXPECT().
					PostCluster(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
						built, buildErr := builder.Build()
						require.NoError(t, buildErr)
						assert.Equal(t, pendingClusterServiceID.ID(), built.ID(), "PostCluster should use the final segment of PendingClusterServiceID")
						assert.Equal(t, testProvisionShardID, built.ProvisionShardID(), "PostCluster should pin the provision shard from the placed management cluster")
						assert.Equal(t, "abcde.example.com", built.DNS().BaseDomain())
						assert.Equal(t, "testcluster", built.DomainPrefix())
						csCluster, err := arohcpv1alpha1.NewCluster().
							ID(pendingClusterServiceID.ID()).
							HREF(testClusterServiceIDStr).
							Build()
						require.NoError(t, err)
						return csCluster, nil
					})
				return mockCS
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.ClusterServiceID, "successful create persists ID in the same reconcile")
				assert.Equal(t, "abcde.example.com", cluster.ServiceProviderProperties.DNS.BaseDomain)
			},
		},
		{
			name: "skip when cluster already has ClusterServiceID",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.ClusterServiceID = &clusterInternalID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.ClusterServiceID = &clusterInternalID
			}),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.ClusterServiceID)
				assert.Equal(t, testClusterServiceIDStr, cluster.ServiceProviderProperties.ClusterServiceID.String())
			},
		},
		{
			name:        "skip when PendingClusterServiceID is nil",
			listCluster: newTestCluster(),
			dbCluster:   newTestCluster(),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID)
			},
		},
		{
			name: "desired version not set waits without dispatching",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				// Placement is resolved so the needsWork placement gate passes; the desired
				// version is intentionally left nil so this case isolates the desired-version
				// precondition.
				spc.Spec.ManagementClusterResourceID = testManagementClusterResourceID()
			}),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID)
			},
		},
		{
			name: "deny assignments still pending waits without dispatching",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				// The desired version is resolved (that precondition passes)...
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				// ...but deny assignments are still pending, so cluster creation must not dispatch yet.
				spc.Status.AzureResources.DenyAssignments.PendingAzureResources = []coreapi.DenyAssignmentReference{{DenyAssignmentType: "resources-deny-assignment", DenyAssignmentResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/testManagedResourceGroup/providers/Microsoft.Authorization/denyAssignments/00000000-0000-0000-0000-000000000001"))}}
			}),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				// No CS calls are expected: gomock fails the test if the controller dispatches.
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID, "cluster creation must not dispatch while deny assignments are pending")
			},
		},
		{
			name: "deny assignments disabled (no real FPA) dispatches without waiting on them",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
				c.ServiceProviderProperties.DNS = coreapi.ServiceProviderDNSProfile{BaseDomain: "abcde.example.com"}
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				// Desired version is resolved, and NO deny assignments are tracked because the
				// ClusterDenyAssignment controller is disabled in this environment.
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				// Placement is resolved so the (independent) placement gate does not block; this
				// case isolates the deny-assignment behaviour.
				spc.Spec.ManagementClusterResourceID = testManagementClusterResourceID()
				setStorageAccountNetworkAccessValidationPassed(spc)
			}),
			managementClusters:      []*fleetapi.ManagementCluster{newTestManagementCluster()},
			denyAssignmentsDisabled: true,
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCS.EXPECT().
					ListClusters(gomock.Any()).
					Return(ocm.NewSimpleClusterListIterator(nil, nil))
				mockCS.EXPECT().
					PostCluster(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
						csCluster, err := arohcpv1alpha1.NewCluster().
							ID(pendingClusterServiceID.ID()).
							HREF(testClusterServiceIDStr).
							Build()
						require.NoError(t, err)
						return csCluster, nil
					})
				return mockCS
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.ClusterServiceID, "successful create persists ID in the same reconcile")
			},
		},
		{
			name: "confirmed deny assignments without a completed reconcile wait without dispatching",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				setDenyAssignmentsCreated(spc)
				spc.Status.AzureResources.DenyAssignments.EarliestRecheckTime = nil
			}),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID, "cluster creation must not dispatch before deny-assignment reconciliation completes")
			},
		},
		{
			name: "defer creation when placement intent (Spec.ManagementClusterResourceID) is not resolved",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				// Deny assignments are already created so the (independent) deny-assignment gate
				// passes; this case isolates the placement gate.
				setDenyAssignmentsCreated(spc)
				// Spec.ManagementClusterResourceID intentionally left nil: placement not resolved.
			}),
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				// needsWork gates on placement (Spec.ManagementClusterResourceID) read from
				// the ServiceProviderCluster cache before SyncOnce runs, so the Cluster
				// Service ListClusters lookup never happens while placement is unresolved.
				// gomock fails the test if any CS call is made.
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				// No CS cluster created: ClusterServiceID stays nil and the pending
				// ID is preserved for the next attempt once placement lands.
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID)
				assert.NotNil(t, cluster.ServiceProviderProperties.PendingClusterServiceID)
			},
		},
		{
			name: "adopts existing Cluster Service cluster for Azure resource",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				spc.Spec.ManagementClusterResourceID = testManagementClusterResourceID()
				setDenyAssignmentsCreated(spc)
				setStorageAccountNetworkAccessValidationPassed(spc)
			}),
			managementClusters: []*fleetapi.ManagementCluster{newTestManagementCluster()},
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
				// Build the CS cluster with Azure fields matching the test cluster so it
				// passes the csClustersMatchingClusterByAzureInfo filter.
				csCluster, err := arohcpv1alpha1.NewCluster().
					HREF(testClusterServiceIDStr).
					DomainPrefix("testcluster").
					Azure(arohcpv1alpha1.NewAzure().
						SubscriptionID(strings.ToLower(testSubscriptionID)).
						ResourceGroupName(strings.ToLower(testResourceGroupName)).
						ResourceName(strings.ToLower(testClusterName)).
						TenantID(testTenantID).
						ManagedResourceGroupName(testManagedResourceGroup)).
					Build()
				require.NoError(t, err)
				mockCS.EXPECT().
					ListClusters(gomock.Any()).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{csCluster}, nil))
				return mockCS
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.ClusterServiceID)
				assert.Equal(t, testClusterServiceIDStr, cluster.ServiceProviderProperties.ClusterServiceID.String())
			},
		},
		{
			name: "storage account network access validation not yet reported waits without dispatching",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				// All other preconditions pass; the StorageAccountNetworkAccessValidation
				// condition is intentionally left absent (controller hasn't reported yet).
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				spc.Spec.ManagementClusterResourceID = testManagementClusterResourceID()
				setDenyAssignmentsCreated(spc)
			}),
			managementClusters: []*fleetapi.ManagementCluster{newTestManagementCluster()},
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				// No CS calls are expected: gomock fails the test if the controller dispatches.
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID, "cluster creation must not dispatch before StorageAccountNetworkAccessValidation has reported a result")
			},
		},
		{
			name: "storage account network access validation Unknown waits without dispatching",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				spc.Spec.ManagementClusterResourceID = testManagementClusterResourceID()
				setDenyAssignmentsCreated(spc)
				setStorageAccountNetworkAccessValidationUnknown(spc)
			}),
			managementClusters: []*fleetapi.ManagementCluster{newTestManagementCluster()},
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID, "cluster creation must not dispatch while StorageAccountNetworkAccessValidation is Unknown")
			},
		},
		{
			name: "storage account network access validation Failed waits without dispatching",
			listCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			dbCluster: newTestCluster(func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &pendingClusterServiceID
			}),
			existingServiceProviderCluster: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
				spc.Spec.ManagementClusterResourceID = testManagementClusterResourceID()
				setDenyAssignmentsCreated(spc)
				setStorageAccountNetworkAccessValidationFailed(spc)
			}),
			managementClusters: []*fleetapi.ManagementCluster{newTestManagementCluster()},
			setupMockCS: func(ctrl *gomock.Controller) ocm.ClusterServiceClientSpec {
				// No CS calls are expected: a conflicting policy must stop the Cluster Service
				// cluster from ever being created, not merely delay it. gomock fails the test
				// if the controller dispatches.
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.ClusterServiceID, "cluster creation must not dispatch once StorageAccountNetworkAccessValidation has Failed")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)

			subscription := newTestSubscription()
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{subscription, tt.dbCluster})
			require.NoError(t, err)

			if tt.existingServiceProviderCluster != nil {
				_, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, tt.existingServiceProviderCluster, nil)
				require.NoError(t, err)
			}

			mockCS := tt.setupMockCS(ctrl)

			var listerClusters []*coreapi.Cluster
			if tt.listCluster != nil {
				listerClusters = []*coreapi.Cluster{tt.listCluster}
			}
			var listerSPCs []*coreapi.ServiceProviderCluster
			if tt.existingServiceProviderCluster != nil {
				listerSPCs = []*coreapi.ServiceProviderCluster{tt.existingServiceProviderCluster}
			}
			syncer := &clusterClusterServiceCreateSyncer{
				resourcesDBClient:            mockDB,
				clusterLister:                &corelistertesting.SliceClusterLister{Clusters: listerClusters},
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: listerSPCs},
				subscriptionLister:           &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{subscription}},
				managementClusterLister:      &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: tt.managementClusters},
				clustersServiceClient:        mockCS,
				denyAssignmentsEnabled:       !tt.denyAssignmentsDisabled,
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}

			err = syncer.SyncOnce(ctx, key)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockDB)
			}
		})
	}
}

func TestStorageAccountNetworkAccessCreationPrecondition(t *testing.T) {
	for _, state := range []metadataapi.ClusterImageRegistryState{metadataapi.ClusterImageRegistryStateEnabled, metadataapi.ClusterImageRegistryStateDisabled} {
		for _, status := range []string{"absent", string(metav1.ConditionTrue), string(metav1.ConditionFalse), string(metav1.ConditionUnknown)} {
			t.Run(string(state)+"/"+status, func(t *testing.T) {
				cluster := newTestCluster(func(cluster *coreapi.Cluster) {
					cluster.CustomerProperties.ClusterImageRegistry.State = state
				})
				spc := newTestSPC()
				if status != "absent" {
					meta.SetStatusCondition(&spc.Status.Validations, metav1.Condition{
						Type:   validationutils.StorageAccountNetworkAccessValidationName,
						Status: metav1.ConditionStatus(status),
						Reason: "Test",
					})
				}
				controller := &clusterClusterServiceCreateSyncer{}
				ready, err := controller.createPreconditionStorageAccountNetworkAccessValidationPassed(context.Background(), cluster, spc)
				require.NoError(t, err)
				assert.Equal(t, state == metadataapi.ClusterImageRegistryStateDisabled || status == string(metav1.ConditionTrue), ready)
			})
		}
	}
}

func TestClusterClusterServiceCreate_findAROHCPClusterByAzureInfo(t *testing.T) {
	azureTestCluster := func(t *testing.T, sub, rg, name, tenant, mrg string) *arohcpv1alpha1.Cluster {
		t.Helper()
		c, err := arohcpv1alpha1.NewCluster().
			Name(name).
			Azure(arohcpv1alpha1.NewAzure().
				SubscriptionID(sub).
				ResourceGroupName(rg).
				ResourceName(name).
				TenantID(tenant).
				ManagedResourceGroupName(mrg)).
			Build()
		require.NoError(t, err)
		return c
	}

	ctx := context.Background()
	defaultSub := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	defaultRG := "my-rg"
	defaultResName := "MyCluster"
	defaultTenant := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	defaultMRG := "arohcp-mycluster-uuid"

	searchString := func(sub, rg, name, tenant, mrg string) string {
		return fmt.Sprintf("azure.subscription_id = '%s' and azure.resource_group_name = '%s' and azure.resource_name = '%s' and azure.tenant_id = '%s' and azure.managed_resource_group_name = '%s'",
			strings.ToLower(sub), strings.ToLower(rg), strings.ToLower(name), tenant, mrg,
		)
	}

	tests := []struct {
		name        string
		sub         string
		rg          string
		resName     string
		tenant      string
		mrg         string
		setupMockCS func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster)
		wantErr     bool
	}{
		{
			name: "found on primary search",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantRG := strings.ToLower(defaultRG)
				wantName := strings.ToLower(defaultResName)
				match := azureTestCluster(t, wantSub, wantRG, wantName, defaultTenant, defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{match}, nil))
				return mock, match
			},
		},
		{
			name: "found with uppercase subscription id",
			sub:  strings.ToUpper(defaultSub),
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantRG := strings.ToLower(defaultRG)
				wantName := strings.ToLower(defaultResName)
				match := azureTestCluster(t, wantSub, wantRG, wantName, defaultTenant, defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{match}, nil))
				return mock, match
			},
		},
		{
			name: "found with uppercase resource group",
			rg:   strings.ToUpper(defaultRG),
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantRG := strings.ToLower(defaultRG)
				wantName := strings.ToLower(defaultResName)
				match := azureTestCluster(t, wantSub, wantRG, wantName, defaultTenant, defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{match}, nil))
				return mock, match
			},
		},
		{
			name:    "found with uppercase resource name",
			resName: strings.ToUpper(defaultResName),
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantRG := strings.ToLower(defaultRG)
				wantName := strings.ToLower(defaultResName)
				match := azureTestCluster(t, wantSub, wantRG, wantName, defaultTenant, defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{match}, nil))
				return mock, match
			},
		},
		{
			name: "not found when list is empty",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator(nil, nil))
				return mock, nil
			},
		},
		{
			name: "not found when cs returns cluster with different resource name",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantRG := strings.ToLower(defaultRG)
				mismatch := azureTestCluster(t, wantSub, wantRG, "other-name", defaultTenant, defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{mismatch}, nil))
				return mock, nil
			},
		},
		{
			name: "not found when cs returns cluster with different resource group",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantName := strings.ToLower(defaultResName)
				mismatch := azureTestCluster(t, wantSub, "other-rg", wantName, defaultTenant, defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{mismatch}, nil))
				return mock, nil
			},
		},
		{
			name: "not found when cs returns cluster with different subscription",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantRG := strings.ToLower(defaultRG)
				wantName := strings.ToLower(defaultResName)
				mismatch := azureTestCluster(t, "00000000-0000-0000-0000-000000000001", wantRG, wantName, defaultTenant, defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{mismatch}, nil))
				return mock, nil
			},
		},
		{
			name: "not found when cs returns cluster with different tenant",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantRG := strings.ToLower(defaultRG)
				wantName := strings.ToLower(defaultResName)
				mismatch := azureTestCluster(t, wantSub, wantRG, wantName, "cccccccc-cccc-cccc-cccc-cccccccccccc", defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{mismatch}, nil))
				return mock, nil
			},
		},
		{
			name: "not found when cs returns cluster with different managed resource group",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantRG := strings.ToLower(defaultRG)
				wantName := strings.ToLower(defaultResName)
				mismatch := azureTestCluster(t, wantSub, wantRG, wantName, defaultTenant, "arohcp-other-mrg")
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{mismatch}, nil))
				return mock, nil
			},
		},
		{
			name: "multiple matches error",
			setupMockCS: func(t *testing.T, ctrl *gomock.Controller, wantSearch string) (ocm.ClusterServiceClientSpec, *arohcpv1alpha1.Cluster) {
				wantSub := strings.ToLower(defaultSub)
				wantRG := strings.ToLower(defaultRG)
				wantName := strings.ToLower(defaultResName)
				a := azureTestCluster(t, wantSub, wantRG, wantName, defaultTenant, defaultMRG)
				b := azureTestCluster(t, wantSub, wantRG, wantName, defaultTenant, defaultMRG)
				mock := ocm.NewMockClusterServiceClientSpec(ctrl)
				mock.EXPECT().
					ListClusters(wantSearch).
					Return(ocm.NewSimpleClusterListIterator([]*arohcpv1alpha1.Cluster{a, b}, nil))
				return mock, nil
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := defaultSub
			if tt.sub != "" {
				sub = tt.sub
			}
			rg := defaultRG
			if tt.rg != "" {
				rg = tt.rg
			}
			resName := defaultResName
			if tt.resName != "" {
				resName = tt.resName
			}
			tenant := defaultTenant
			if tt.tenant != "" {
				tenant = tt.tenant
			}
			mrg := defaultMRG
			if tt.mrg != "" {
				mrg = tt.mrg
			}
			wantSearch := searchString(sub, rg, resName, tenant, mrg)

			ctrl := gomock.NewController(t)
			mockCS, want := tt.setupMockCS(t, ctrl, wantSearch)

			got, err := ocm.FindClusterByAzureInfo(ctx, mockCS, sub, rg, resName, tenant, mrg)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if want != nil {
				require.Same(t, want, got)
			} else {
				require.Nil(t, got)
			}
		})
	}
}
