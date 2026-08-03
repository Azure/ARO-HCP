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

package denyassignments

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/azuremockclient"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testTenantID          = "11111111-1111-1111-1111-111111111111"
	testManagedRG         = "testManagedResourceGroup"
)

func testClusterResourceID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
}

func testManagedResourceGroupID() *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testManagedRG,
	))
}

func testKey() controllerutils.HCPClusterKey {
	return controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
}

func testSubscription() *coreapi.Subscription {
	rid := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID))
	return &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   rid,
			PartitionKey: strings.ToLower(rid.SubscriptionID),
		},
		ResourceID: rid,
		Properties: &coreapi.SubscriptionProperties{TenantId: ptr.To(testTenantID)},
	}
}

func testIdentityResourceID(name string) *azcorearm.ResourceID {
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name,
	))
}

func newTestCluster(opts ...func(*coreapi.HCPOpenShiftCluster)) *coreapi.HCPOpenShiftCluster {
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

	csID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/abc123"))
	cluster.ServiceProviderProperties.PendingClusterServiceID = &csID
	cluster.CustomerProperties.Platform.ManagedResourceGroup = testManagedRG

	cpOps := map[string]*azcorearm.ResourceID{
		"cluster-api-azure":        testIdentityResourceID("capi-azure"),
		"cloud-controller-manager": testIdentityResourceID("ccm"),
		"disk-csi-driver":          testIdentityResourceID("disk-csi"),
		"control-plane":            testIdentityResourceID("control-plane"),
		"image-registry":           testIdentityResourceID("image-registry"),
		"file-csi-driver":          testIdentityResourceID("file-csi"),
		"kms":                      testIdentityResourceID("kms"),
		"ingress":                  testIdentityResourceID("ingress"),
		"cloud-network-config":     testIdentityResourceID("cloud-network-config"),
	}
	dpOps := map[string]*azcorearm.ResourceID{
		"image-registry":  testIdentityResourceID("dp-image-registry"),
		"disk-csi-driver": testIdentityResourceID("dp-disk-csi"),
		"file-csi-driver": testIdentityResourceID("dp-file-csi"),
	}
	serviceManagedID := testIdentityResourceID("service-managed")

	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = cpOps
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = dpOps
	cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = serviceManagedID

	allIdentities := make(map[string]*coreapi.UserAssignedIdentity)
	for _, id := range cpOps {
		allIdentities[strings.ToLower(id.String())] = &coreapi.UserAssignedIdentity{PrincipalID: ptr.To("principal-" + id.Name)}
	}
	for _, id := range dpOps {
		allIdentities[strings.ToLower(id.String())] = &coreapi.UserAssignedIdentity{PrincipalID: ptr.To("principal-" + id.Name)}
	}
	allIdentities[strings.ToLower(serviceManagedID.String())] = &coreapi.UserAssignedIdentity{PrincipalID: ptr.To("principal-service-managed")}
	cluster.Identity = &coreapi.ManagedServiceIdentity{
		UserAssignedIdentities: allIdentities,
	}

	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

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
	spc.Status.AzureResources.ManagedResourceGroup.AzureResource = testManagedResourceGroupID()
	for _, opt := range opts {
		opt(spc)
	}
	return spc
}

func denyAssignmentNotFoundError() error {
	return &azcore.ResponseError{ErrorCode: "DenyAssignmentNotFound"}
}

func resourceNotFoundError() error {
	return &azcore.ResponseError{StatusCode: 404}
}

func matchingGetResponseForAllTypes(cluster *coreapi.HCPOpenShiftCluster) func(ctx context.Context, scope string, denyAssignmentID string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
	refs, _ := allDenyAssignmentReferences(cluster)
	nameToType := make(map[string]string, len(refs))
	for _, ref := range refs {
		nameToType[ref.DenyAssignmentResourceID.Name] = ref.DenyAssignmentType
	}

	defs := denyAssignmentDefinitions(cluster)
	defsByType := make(map[string]denyAssignmentDefinition, len(defs))
	for _, d := range defs {
		defsByType[d.denyAssignmentType] = d
	}

	return func(ctx context.Context, scope string, denyAssignmentID string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
		daType, ok := nameToType[denyAssignmentID]
		if !ok {
			return armauthorization.DenyAssignmentsClientGetResponse{}, denyAssignmentNotFoundError()
		}
		def := defsByType[daType]
		notActions := def.notActions
		if notActions == nil {
			notActions = []string{}
		}
		dataActions := def.dataActions
		if dataActions == nil {
			dataActions = []string{}
		}
		excludedIDs, _ := collectExcludedPrincipalIDs(cluster, def)
		principalIDs, _ := resolvePrincipalIDs(cluster, excludedIDs)
		excludedPrincipals := make([]*armauthorization.Principal, 0, len(principalIDs))
		for _, pid := range principalIDs {
			excludedPrincipals = append(excludedPrincipals, &armauthorization.Principal{ID: ptr.To(pid)})
		}

		return armauthorization.DenyAssignmentsClientGetResponse{
			DenyAssignment: armauthorization.DenyAssignment{
				Properties: &armauthorization.DenyAssignmentProperties{
					Permissions: []*armauthorization.DenyAssignmentPermission{
						{
							Actions:     to.SliceOfPtrs(def.actions...),
							NotActions:  to.SliceOfPtrs(notActions...),
							DataActions: to.SliceOfPtrs(dataActions...),
						},
					},
					ExcludePrincipals: excludedPrincipals,
				},
			},
		}, nil
	}
}

func TestGenerateDenyAssignmentUUIDMatchesClusterService(t *testing.T) {
	// referenceClusterServiceUUID replicates Cluster Service's derivation exactly
	// (pkg/utils/uuid/generators.go generateUuidV5WithSeparator): a v5 UUID over the shared
	// namespace and strings.Join([]string{suffix, clusterID}, "$"). If this diverges from
	// generateDenyAssignmentUUID, the RP and Cluster Service would compute different deny assignment
	// IDs for the same cluster and stop recognizing each other's assignments.
	referenceClusterServiceUUID := func(clusterID, suffix string) string {
		ns := uuid.MustParse(denyAssignmentNamespaceUUID)
		return uuid.NewSHA1(ns, []byte(strings.Join([]string{suffix, clusterID}, "$"))).String()
	}

	clusterIDs := []string{"2abcdef1234567890abcdef123456789", "another-cs-cluster-id"}
	for _, clusterID := range clusterIDs {
		for _, def := range denyAssignmentDefinitions(newTestCluster()) {
			assert.Equal(t,
				referenceClusterServiceUUID(clusterID, def.denyAssignmentType),
				generateDenyAssignmentUUID(clusterID, def.denyAssignmentType),
				"deny assignment UUID for %q must match Cluster Service's derivation", def.denyAssignmentType)
		}
	}

	// Golden value (computed with Cluster Service's algorithm) guards against the namespace,
	// separator, or salt order changing on both sides at once.
	assert.Equal(t,
		"c4ff85a1-5daa-5ed4-b4e2-fdf60a7d24ad",
		generateDenyAssignmentUUID("2abcdef1234567890abcdef123456789", "compute-deny-assignment"),
		"deny assignment UUID derivation must not change (would desync from Cluster Service)")
}

func TestSyncOnce(t *testing.T) {
	tests := []struct {
		name    string
		cluster *coreapi.HCPOpenShiftCluster
	}{
		{
			name:    "cluster not found returns nil",
			cluster: nil,
		},
		{
			name: "deletion timestamp set returns nil",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			var clusters []*coreapi.HCPOpenShiftCluster
			if tt.cluster != nil {
				clusters = []*coreapi.HCPOpenShiftCluster{tt.cluster}
			}
			syncer := &clusterDenyAssignmentSyncer{
				clock:         clocktesting.NewFakeClock(time.Now()),
				clusterLister: &corelistertesting.SliceClusterLister{Clusters: clusters},
			}
			err := syncer.SyncOnce(ctx, testKey())
			require.NoError(t, err)
		})
	}
}

func TestSyncDenyAssignmentNeedsWork(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	syncer := &clusterDenyAssignmentSyncer{clock: fakeClock}

	tests := []struct {
		name     string
		cluster  *coreapi.HCPOpenShiftCluster
		spc      *coreapi.ServiceProviderCluster
		expected bool
	}{
		{
			name: "no cluster service ID",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = nil
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			spc:      newTestSPC(),
			expected: false,
		},
		{
			name: "no managed resource group",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.ManagedResourceGroup = ""
			}),
			spc:      newTestSPC(),
			expected: false,
		},
		{
			name: "empty control plane operators",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators = nil
			}),
			spc:      newTestSPC(),
			expected: false,
		},
		{
			name: "empty data plane operators",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators = nil
			}),
			spc:      newTestSPC(),
			expected: false,
		},
		{
			name: "nil service managed identity",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity = nil
			}),
			spc:      newTestSPC(),
			expected: false,
		},
		{
			name: "nil control plane operator value",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators["cluster-api-azure"] = nil
			}),
			spc:      newTestSPC(),
			expected: false,
		},
		{
			name: "nil data plane operator value",
			cluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators["image-registry"] = nil
			}),
			spc:      newTestSPC(),
			expected: false,
		},
		{
			name:    "has pending deny assignments",
			cluster: newTestCluster(),
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.AzureResources.DenyAssignments.PendingAzureResources = []coreapi.DenyAssignmentReference{
					{DenyAssignmentType: "some-type"},
				}
			}),
			expected: true,
		},
		{
			name:     "no azure resources and no pending -- first time",
			cluster:  newTestCluster(),
			spc:      newTestSPC(),
			expected: true,
		},
		{
			name:    "azure resources present, before recheck time",
			cluster: newTestCluster(),
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.AzureResources.DenyAssignments.AzureResources = []coreapi.DenyAssignmentReference{
					{DenyAssignmentType: "resources-deny-assignment"},
				}
				future := metav1.NewTime(fakeClock.Now().Add(1 * time.Hour))
				spc.Status.AzureResources.DenyAssignments.EarliestRecheckTime = &future
			}),
			expected: false,
		},
		{
			name:    "azure resources present, past recheck time",
			cluster: newTestCluster(),
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.AzureResources.DenyAssignments.AzureResources = []coreapi.DenyAssignmentReference{
					{DenyAssignmentType: "resources-deny-assignment"},
				}
				past := metav1.NewTime(fakeClock.Now().Add(-1 * time.Hour))
				spc.Status.AzureResources.DenyAssignments.EarliestRecheckTime = &past
			}),
			expected: true,
		},
		{
			name:    "azure resources present, nil recheck time",
			cluster: newTestCluster(),
			spc: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.AzureResources.DenyAssignments.AzureResources = []coreapi.DenyAssignmentReference{
					{DenyAssignmentType: "resources-deny-assignment"},
				}
			}),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncer.syncDenyAssignmentNeedsWork(tt.cluster, tt.spc)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestSyncDenyAssignmentUpsert(t *testing.T) {
	fakeClock := clocktesting.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	tests := []struct {
		name                 string
		cluster              *coreapi.HCPOpenShiftCluster
		existingSPC          *coreapi.ServiceProviderCluster
		mockDenyAssignments  *azuremockclient.DenyAssignmentsClientFunc
		mockGenericResources *azuremockclient.GenericResourcesClientFunc
		expectError          bool
		verify               func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:        "first time initializes pending, create fails leaves them pending",
			cluster:     newTestCluster(),
			existingSPC: newTestSPC(),
			mockDenyAssignments: &azuremockclient.DenyAssignmentsClientFunc{
				GetFunc: func(ctx context.Context, scope string, id string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
					return armauthorization.DenyAssignmentsClientGetResponse{}, denyAssignmentNotFoundError()
				},
			},
			mockGenericResources: &azuremockclient.GenericResourcesClientFunc{
				CreateErr: fmt.Errorf("simulated create failure"),
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.NotEmpty(t, spc.Status.AzureResources.DenyAssignments.PendingAzureResources, "pending should be populated after first-time initialization")
			},
		},
		{
			name:    "removes stale deny assignment not in definitions",
			cluster: newTestCluster(),
			existingSPC: newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.AzureResources.DenyAssignments.AzureResources = []coreapi.DenyAssignmentReference{
					{
						DenyAssignmentType:       "stale-type-not-in-definitions",
						DenyAssignmentResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testManagedRG + "/providers/Microsoft.Authorization/denyAssignments/stale-uuid")),
					},
				}
			}),
			mockDenyAssignments: &azuremockclient.DenyAssignmentsClientFunc{
				GetFunc: func(ctx context.Context, scope string, id string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
					return armauthorization.DenyAssignmentsClientGetResponse{}, denyAssignmentNotFoundError()
				},
			},
			mockGenericResources: &azuremockclient.GenericResourcesClientFunc{
				DeleteErr: resourceNotFoundError(),
				CreateErr: fmt.Errorf("simulated create failure"),
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				for _, ref := range spc.Status.AzureResources.DenyAssignments.AzureResources {
					assert.NotEqual(t, "stale-type-not-in-definitions", ref.DenyAssignmentType, "stale type should have been removed")
				}
			},
		},
		{
			name:    "existing content up to date sets recheck time",
			cluster: newTestCluster(),
			existingSPC: func() *coreapi.ServiceProviderCluster {
				cluster := newTestCluster()
				refs, _ := allDenyAssignmentReferences(cluster)
				return newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
					spc.Status.AzureResources.DenyAssignments.AzureResources = refs
				})
			}(),
			mockDenyAssignments: &azuremockclient.DenyAssignmentsClientFunc{
				GetFunc: matchingGetResponseForAllTypes(newTestCluster()),
			},
			mockGenericResources: &azuremockclient.GenericResourcesClientFunc{},
			expectError:          false,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.NotEmpty(t, spc.Status.AzureResources.DenyAssignments.AzureResources, "AzureResources should remain populated")
				assert.Empty(t, spc.Status.AzureResources.DenyAssignments.PendingAzureResources, "PendingAzureResources should be empty")
				assert.NotNil(t, spc.Status.AzureResources.DenyAssignments.EarliestRecheckTime, "EarliestRecheckTime should be set")
			},
		},
		{
			name:    "existing content mismatched triggers update attempt, failure moves to pending",
			cluster: newTestCluster(),
			existingSPC: func() *coreapi.ServiceProviderCluster {
				cluster := newTestCluster()
				refs, _ := allDenyAssignmentReferences(cluster)
				return newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
					spc.Status.AzureResources.DenyAssignments.AzureResources = refs
				})
			}(),
			mockDenyAssignments: &azuremockclient.DenyAssignmentsClientFunc{
				GetFunc: func(ctx context.Context, scope string, id string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
					return armauthorization.DenyAssignmentsClientGetResponse{
						DenyAssignment: armauthorization.DenyAssignment{
							Properties: &armauthorization.DenyAssignmentProperties{
								Permissions: []*armauthorization.DenyAssignmentPermission{
									{
										Actions:     to.SliceOfPtrs("wrong-action"),
										NotActions:  to.SliceOfPtrs[string](),
										DataActions: to.SliceOfPtrs[string](),
									},
								},
							},
						},
					}, nil
				},
			},
			mockGenericResources: &azuremockclient.GenericResourcesClientFunc{
				CreateErr: fmt.Errorf("simulated create failure"),
			},
			expectError: true,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.NotEmpty(t, spc.Status.AzureResources.DenyAssignments.PendingAzureResources, "failed ensure should move refs to pending")
			},
		},
		{
			name:    "recheck time in future skips all work",
			cluster: newTestCluster(),
			existingSPC: func() *coreapi.ServiceProviderCluster {
				cluster := newTestCluster()
				refs, _ := allDenyAssignmentReferences(cluster)
				future := metav1.NewTime(fakeClock.Now().Add(6 * time.Hour))
				return newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
					spc.Status.AzureResources.DenyAssignments.AzureResources = refs
					spc.Status.AzureResources.DenyAssignments.EarliestRecheckTime = &future
				})
			}(),
			mockDenyAssignments:  nil,
			mockGenericResources: nil,
			expectError:          false,
			verify: func(t *testing.T, ctx context.Context, mockDB *corecosmosstoragetesting.MockResourcesDBClient) {
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.NotEmpty(t, spc.Status.AzureResources.DenyAssignments.AzureResources, "AzureResources should be untouched")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

			if tt.existingSPC != nil {
				_, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, tt.existingSPC, nil)
				require.NoError(t, err)
			}

			var builder *azuremockclient.FirstPartyApplicationClientBuilderFunc
			if tt.mockDenyAssignments != nil || tt.mockGenericResources != nil {
				builder = &azuremockclient.FirstPartyApplicationClientBuilderFunc{
					GenericResourcesClientVal: tt.mockGenericResources,
					DenyAssignmentsClientVal:  tt.mockDenyAssignments,
				}
			}

			syncer := &clusterDenyAssignmentSyncer{
				clock:                 fakeClock,
				resourcesDBClient:     mockDB,
				clusterLister:         &corelistertesting.SliceClusterLister{Clusters: []*coreapi.HCPOpenShiftCluster{tt.cluster}},
				subscriptionLister:    &corelistertesting.SliceSubscriptionLister{Subscriptions: []*coreapi.Subscription{testSubscription()}},
				azureFPAClientBuilder: builder,
			}

			err := syncer.SyncOnce(ctx, testKey())
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB)
			}
		})
	}
}

func TestEnsureDenyAssignmentReferences(t *testing.T) {
	cluster := newTestCluster()
	defs := denyAssignmentDefinitions(cluster)
	defsByType := make(map[string]denyAssignmentDefinition, len(defs))
	for _, d := range defs {
		defsByType[d.denyAssignmentType] = d
	}

	allRefs, err := allDenyAssignmentReferences(cluster)
	require.NoError(t, err)

	var resourcesRef coreapi.DenyAssignmentReference
	for _, ref := range allRefs {
		if ref.DenyAssignmentType == denyAssignmentSuffixResources {
			resourcesRef = ref
			break
		}
	}

	tests := []struct {
		name                 string
		refs                 []coreapi.DenyAssignmentReference
		defsByType           map[string]denyAssignmentDefinition
		mockDenyAssignments  *azuremockclient.DenyAssignmentsClientFunc
		mockGenericResources *azuremockclient.GenericResourcesClientFunc
		expectSucceeded      int
		expectFailed         int
		expectError          bool
	}{
		{
			name:       "existing content up to date",
			refs:       []coreapi.DenyAssignmentReference{resourcesRef},
			defsByType: defsByType,
			mockDenyAssignments: &azuremockclient.DenyAssignmentsClientFunc{
				GetFunc: matchingGetResponseForAllTypes(cluster),
			},
			mockGenericResources: &azuremockclient.GenericResourcesClientFunc{},
			expectSucceeded:      1,
			expectFailed:         0,
			expectError:          false,
		},
		{
			name:       "content mismatch triggers update attempt",
			refs:       []coreapi.DenyAssignmentReference{resourcesRef},
			defsByType: defsByType,
			mockDenyAssignments: &azuremockclient.DenyAssignmentsClientFunc{
				GetFunc: func(ctx context.Context, scope string, id string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
					return armauthorization.DenyAssignmentsClientGetResponse{
						DenyAssignment: armauthorization.DenyAssignment{
							Properties: &armauthorization.DenyAssignmentProperties{
								Permissions: []*armauthorization.DenyAssignmentPermission{
									{Actions: to.SliceOfPtrs("wrong-action"), NotActions: to.SliceOfPtrs[string](), DataActions: to.SliceOfPtrs[string]()},
								},
							},
						},
					}, nil
				},
			},
			mockGenericResources: &azuremockclient.GenericResourcesClientFunc{CreateErr: fmt.Errorf("simulated create failure")},
			expectSucceeded:      0,
			expectFailed:         1,
			expectError:          true,
		},
		{
			name: "unknown type goes to failed and surfaces an error",
			refs: []coreapi.DenyAssignmentReference{
				{
					DenyAssignmentType:       "unknown-type",
					DenyAssignmentResourceID: metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testManagedRG + "/providers/Microsoft.Authorization/denyAssignments/test-uuid")),
				},
			},
			defsByType:           map[string]denyAssignmentDefinition{},
			mockDenyAssignments:  &azuremockclient.DenyAssignmentsClientFunc{},
			mockGenericResources: &azuremockclient.GenericResourcesClientFunc{},
			expectSucceeded:      0,
			expectFailed:         1,
			expectError:          true,
		},
		{
			name:       "deny assignment not found triggers create",
			refs:       []coreapi.DenyAssignmentReference{resourcesRef},
			defsByType: defsByType,
			mockDenyAssignments: &azuremockclient.DenyAssignmentsClientFunc{
				GetFunc: func(ctx context.Context, scope string, id string, opts *armauthorization.DenyAssignmentsClientGetOptions) (armauthorization.DenyAssignmentsClientGetResponse, error) {
					return armauthorization.DenyAssignmentsClientGetResponse{}, denyAssignmentNotFoundError()
				},
			},
			mockGenericResources: &azuremockclient.GenericResourcesClientFunc{CreateErr: fmt.Errorf("simulated create failure")},
			expectSucceeded:      0,
			expectFailed:         1,
			expectError:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			syncer := &clusterDenyAssignmentSyncer{clock: clocktesting.NewFakeClock(time.Now())}

			succeeded, failed, err := syncer.ensureDenyAssignmentReferences(ctx, cluster, tt.mockDenyAssignments, tt.mockGenericResources,
				testManagedResourceGroupID(), tt.defsByType, tt.refs)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Len(t, succeeded, tt.expectSucceeded)
			assert.Len(t, failed, tt.expectFailed)
		})
	}
}

func TestDeleteDenyAssignment(t *testing.T) {
	rid := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testManagedRG + "/providers/Microsoft.Authorization/denyAssignments/test-uuid"))

	tests := []struct {
		name        string
		deleteErr   error
		expectError bool
	}{
		{
			name:        "resource not found is no-op",
			deleteErr:   resourceNotFoundError(),
			expectError: false,
		},
		{
			name:        "other error propagates",
			deleteErr:   fmt.Errorf("some azure error"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockGenericResources := &azuremockclient.GenericResourcesClientFunc{DeleteErr: tt.deleteErr}
			syncer := &clusterDenyAssignmentSyncer{clock: clocktesting.NewFakeClock(time.Now())}

			err := syncer.deleteDenyAssignment(ctx, mockGenericResources, rid)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Len(t, mockGenericResources.DeleteCalls, 1)
		})
	}
}

func TestDenyAssignmentNeedsUpdate(t *testing.T) {
	actions := []string{"action1", "action2"}
	notActions := []string{"notAction1"}
	dataActions := []string{}
	excludedPrincipalIDs := []string{"principal-1"}

	tests := []struct {
		name     string
		existing *armauthorization.DenyAssignment
		expected bool
	}{
		{
			name:     "nil properties",
			existing: &armauthorization.DenyAssignment{},
			expected: true,
		},
		{
			name: "matching content",
			existing: &armauthorization.DenyAssignment{
				Properties: &armauthorization.DenyAssignmentProperties{
					Permissions: []*armauthorization.DenyAssignmentPermission{
						{Actions: to.SliceOfPtrs(actions...), NotActions: to.SliceOfPtrs(notActions...), DataActions: to.SliceOfPtrs[string]()},
					},
					ExcludePrincipals: []*armauthorization.Principal{{ID: ptr.To("principal-1")}},
				},
			},
			expected: false,
		},
		{
			name: "different actions",
			existing: &armauthorization.DenyAssignment{
				Properties: &armauthorization.DenyAssignmentProperties{
					Permissions: []*armauthorization.DenyAssignmentPermission{
						{Actions: to.SliceOfPtrs("wrong-action"), NotActions: to.SliceOfPtrs(notActions...), DataActions: to.SliceOfPtrs[string]()},
					},
					ExcludePrincipals: []*armauthorization.Principal{{ID: ptr.To("principal-1")}},
				},
			},
			expected: true,
		},
		{
			name: "different excluded principals",
			existing: &armauthorization.DenyAssignment{
				Properties: &armauthorization.DenyAssignmentProperties{
					Permissions: []*armauthorization.DenyAssignmentPermission{
						{Actions: to.SliceOfPtrs(actions...), NotActions: to.SliceOfPtrs(notActions...), DataActions: to.SliceOfPtrs[string]()},
					},
					ExcludePrincipals: []*armauthorization.Principal{{ID: ptr.To("wrong-principal")}},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := denyAssignmentNeedsUpdate(tt.existing, actions, notActions, dataActions, excludedPrincipalIDs)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestReplaceServiceProviderClusterIfChanged(t *testing.T) {
	tests := []struct {
		name              string
		modifyReplacement func(spc *coreapi.ServiceProviderCluster)
		priorErrs         []error
		expectError       bool
		expectNilReturn   bool
	}{
		{
			name:              "no change, no errors",
			modifyReplacement: nil,
			priorErrs:         nil,
			expectError:       false,
			expectNilReturn:   false,
		},
		{
			name: "with change persists",
			modifyReplacement: func(spc *coreapi.ServiceProviderCluster) {
				spc.Status.AzureResources.DenyAssignments.PendingAzureResources = []coreapi.DenyAssignmentReference{
					{DenyAssignmentType: "new-type"},
				}
			},
			priorErrs:       nil,
			expectError:     false,
			expectNilReturn: false,
		},
		{
			name:              "prior errors return error",
			modifyReplacement: nil,
			priorErrs:         []error{fmt.Errorf("prior error")},
			expectError:       true,
			expectNilReturn:   true,
		},
		{
			name:              "nil error in slice is not treated as error",
			modifyReplacement: nil,
			priorErrs:         []error{nil},
			expectError:       false,
			expectNilReturn:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			mockDB := corecosmosstoragetesting.NewMockResourcesDBClient()

			spc := newTestSPC()
			_, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spc, nil)
			require.NoError(t, err)

			spcCRUD := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
			original, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			replacement := original.DeepCopy()

			if tt.modifyReplacement != nil {
				tt.modifyReplacement(replacement)
			}

			returned, retReplacement, err := replaceServiceProviderClusterIfChanged(ctx, spcCRUD, original, replacement, tt.priorErrs)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tt.expectNilReturn {
				assert.Nil(t, returned)
				assert.Nil(t, retReplacement)
			} else {
				assert.NotNil(t, returned)
				assert.NotNil(t, retReplacement)
			}
		})
	}
}

func TestAppendDenyAssignmentReference(t *testing.T) {
	ref1 := coreapi.DenyAssignmentReference{DenyAssignmentType: "type-a"}
	ref2 := coreapi.DenyAssignmentReference{DenyAssignmentType: "type-b"}
	ref3 := coreapi.DenyAssignmentReference{DenyAssignmentType: "type-a"}

	result := appendDenyAssignmentReference(nil, ref1, ref2, ref3)
	assert.Len(t, result, 2)
	assert.Equal(t, "type-a", result[0].DenyAssignmentType)
	assert.Equal(t, "type-b", result[1].DenyAssignmentType)

	result = appendDenyAssignmentReference([]coreapi.DenyAssignmentReference{ref1}, ref2, ref3)
	assert.Len(t, result, 2)
}

func TestRemoveDenyAssignmentRef(t *testing.T) {
	refs := []coreapi.DenyAssignmentReference{
		{DenyAssignmentType: "type-a"},
		{DenyAssignmentType: "type-b"},
		{DenyAssignmentType: "type-c"},
	}
	result := removeDenyAssignmentRef(refs, "type-b")
	assert.Len(t, result, 2)
	for _, ref := range result {
		assert.NotEqual(t, "type-b", ref.DenyAssignmentType)
	}
}
