// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_buildNodePoolEmptyHypershiftNodePool(t *testing.T) {
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: "testenv",
			uuidV4Generator:                    uuid.NewRandom,
		},
	}

	csNodePoolID := "my-nodepool"
	csClusterID := "11111111111111111111111111111111"
	csClusterDomainPrefix := "f4z6d5t2j3y5g7v"
	np := syncer.buildNodePoolEmptyHypershiftNodePool(csNodePoolID, csClusterID, csClusterDomainPrefix)

	assert.NotNil(t, np)
	assert.Equal(t, "NodePool", np.Kind)
	assert.Equal(t, hsv1beta1.SchemeGroupVersion.String(), np.APIVersion)
	assert.Equal(t, fmt.Sprintf("%s-%s", csClusterDomainPrefix, csNodePoolID), np.Name)
	assert.Equal(t, fmt.Sprintf("ocm-%s-%s", syncer.maestroSourceEnvironmentIdentifier, csClusterID), np.Namespace)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_buildInitialReadonlyMaestroBundleForNodePool(t *testing.T) {
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: "testenv",
			uuidV4Generator:                    uuid.NewRandom,
		},
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/my-nodepool")),
		},
	}
	maestroBundleNamespacedName := types.NamespacedName{
		Name:      "test-maestro-api-maestro-bundle-name",
		Namespace: "test-maestro-consumer",
	}

	csClusterDomainPrefix := "f4z6d5t2j3y5g7v"
	bundle := syncer.buildInitialReadonlyMaestroBundleForNodePool(nodePool, cluster, maestroBundleNamespacedName, csClusterDomainPrefix)
	require.NotNil(t, bundle)

	assert.Equal(t, "test-maestro-api-maestro-bundle-name", bundle.Name)
	assert.Equal(t, "test-maestro-consumer", bundle.Namespace)
	require.Len(t, bundle.Spec.Workload.Manifests, 1)
	require.Len(t, bundle.Spec.ManifestConfigs, 1)

	// Verify manifest config
	manifestConfig := bundle.Spec.ManifestConfigs[0]
	assert.Equal(t, "nodepools", manifestConfig.ResourceIdentifier.Resource)
	assert.Equal(t, hsv1beta1.SchemeGroupVersion.Group, manifestConfig.ResourceIdentifier.Group)
	assert.Equal(t, fmt.Sprintf("%s-%s", csClusterDomainPrefix, "my-nodepool"), manifestConfig.ResourceIdentifier.Name)
	assert.Equal(t, "ocm-testenv-11111111111111111111111111111111", manifestConfig.ResourceIdentifier.Namespace)

	// Verify the label value
	assert.Equal(t, readonlyBundleManagedByK8sLabelValueNodePoolScoped, bundle.Labels[readonlyBundleManagedByK8sLabelKey])

	// Verify manifest object
	expectedNodePool := syncer.buildNodePoolEmptyHypershiftNodePool("my-nodepool", "11111111111111111111111111111111", csClusterDomainPrefix)
	assert.Equal(t, expectedNodePool, bundle.Spec.Workload.Manifests[0].Object)
}

// errorInjectingDBClientForNodePoolCreate wraps MockDBClient to return error-injecting CRUDs for node pool tests.
type errorInjectingDBClientForNodePoolCreate struct {
	*databasetesting.MockDBClient
	spnpCRUD database.ServiceProviderNodePoolCRUD
}

func (e *errorInjectingDBClientForNodePoolCreate) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ServiceProviderNodePoolCRUD {
	if e.spnpCRUD != nil {
		return e.spnpCRUD
	}
	return e.MockDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName)
}

var _ database.DBClient = &errorInjectingDBClientForNodePoolCreate{}

// errorInjectingSPNPCRUD wraps ServiceProviderNodePoolCRUD to allow error injection.
type errorInjectingSPNPCRUD struct {
	database.ServiceProviderNodePoolCRUD
	getErr error
}

func (e *errorInjectingSPNPCRUD) Get(ctx context.Context, resourceID string) (*api.ServiceProviderNodePool, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.ServiceProviderNodePoolCRUD.Get(ctx, resourceID)
}

var _ database.ServiceProviderNodePoolCRUD = &errorInjectingSPNPCRUD{}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_syncMaestroBundle(t *testing.T) {
	syncMaestroBundleTestDeterministicUUID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool/serviceProviderNodePools/default"))

	tests := []struct {
		name                        string
		initialSPNP                 *api.ServiceProviderNodePool
		maestroClientSetupMock      func(*maestro.MockClient)
		wantServiceProviderNodePool *api.ServiceProviderNodePool
		wantErr                     bool
		wantErrSubstr               string
	}{
		{
			name: "existing reference but no ID - sets new ID and preserves name",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
				ResourceID:     *spnpResourceID,
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
							MaestroAPIMaestroBundleName: "existing-bundle-name",
							MaestroAPIMaestroBundleID:   "",
						},
					},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				createdBundle := &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{Name: "existing-bundle-name", Namespace: "test-consumer", UID: "new-bundle-uid"},
				}
				m.EXPECT().Get(gomock.Any(), "existing-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				m.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createdBundle, nil)
			},
			wantServiceProviderNodePool: &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
							MaestroAPIMaestroBundleName: "existing-bundle-name",
							MaestroAPIMaestroBundleID:   "new-bundle-uid",
						},
					},
				},
			},
		},
		{
			name: "complete bundle reference - ID unchanged",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
				ResourceID:     *spnpResourceID,
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
							MaestroAPIMaestroBundleName: "complete-bundle-name",
							MaestroAPIMaestroBundleID:   "complete-bundle-id",
						},
					},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				existingBundle := &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{Name: "complete-bundle-name", Namespace: "test-consumer", UID: "complete-bundle-id"},
				}
				m.EXPECT().Get(gomock.Any(), "complete-bundle-name", gomock.Any()).Return(existingBundle, nil)
			},
			wantServiceProviderNodePool: &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
							MaestroAPIMaestroBundleName: "complete-bundle-name",
							MaestroAPIMaestroBundleID:   "complete-bundle-id",
						},
					},
				},
			},
		},
		{
			name: "maestro get or create error - returns last persisted SPNP",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
				ResourceID:     *spnpResourceID,
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
							MaestroAPIMaestroBundleName: "bundle-name",
							MaestroAPIMaestroBundleID:   "",
						},
					},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("maestro connection error"))
			},
			wantServiceProviderNodePool: &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
							MaestroAPIMaestroBundleName: "bundle-name",
							MaestroAPIMaestroBundleID:   "",
						},
					},
				},
			},
			wantErr:       true,
			wantErrSubstr: "failed to get or create Maestro Bundle",
		},
		{
			name: "no bundle reference initially - creates ref with deterministic UUID name and new ID",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
				ResourceID:     *spnpResourceID,
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				deterministicName := syncMaestroBundleTestDeterministicUUID.String()
				createdBundle := &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{Name: deterministicName, Namespace: "test-consumer", UID: "new-bundle-uid"},
				}
				m.EXPECT().Get(gomock.Any(), deterministicName, gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				m.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, mw *workv1.ManifestWork, opts metav1.CreateOptions) (*workv1.ManifestWork, error) {
						assert.Equal(t, deterministicName, mw.Name)
						assert.Equal(t, "test-consumer", mw.Namespace)
						assert.Len(t, mw.Spec.Workload.Manifests, 1)
						assert.Len(t, mw.Spec.ManifestConfigs, 1)
						assert.Equal(t, workv1.UpdateStrategyTypeReadOnly, mw.Spec.ManifestConfigs[0].UpdateStrategy.Type)
						assert.Equal(t, readonlyBundleManagedByK8sLabelValueNodePoolScoped, mw.Labels[readonlyBundleManagedByK8sLabelKey])
						return createdBundle, nil
					},
				)
			},
			wantServiceProviderNodePool: &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
							MaestroAPIMaestroBundleName: syncMaestroBundleTestDeterministicUUID.String(),
							MaestroAPIMaestroBundleID:   "new-bundle-uid",
						},
					},
				},
			},
		},
		{
			name: "no bundle ref initially - bundle name persisted then getOrCreate fails - returns SPNP with name set, no ID",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
				ResourceID:     *spnpResourceID,
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				deterministicName := syncMaestroBundleTestDeterministicUUID.String()
				m.EXPECT().Get(gomock.Any(), deterministicName, gomock.Any()).Return(nil, fmt.Errorf("maestro connection error"))
			},
			wantServiceProviderNodePool: &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
							MaestroAPIMaestroBundleName: syncMaestroBundleTestDeterministicUUID.String(),
							MaestroAPIMaestroBundleID:   "",
						},
					},
				},
			},
			wantErr:       true,
			wantErrSubstr: "failed to get or create Maestro Bundle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := maestro.NewMockClient(ctrl)
			tt.maestroClientSetupMock(mockMaestro)

			deterministicUUIDGenerator := func() (uuid.UUID, error) { return syncMaestroBundleTestDeterministicUUID, nil }
			syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
				maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
					maestroSourceEnvironmentIdentifier: "test-env",
					uuidV4Generator:                    deterministicUUIDGenerator,
				},
			}
			ctx := context.Background()
			cluster := &api.HCPOpenShiftCluster{
				TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
				},
			}
			nodePool := &api.HCPOpenShiftClusterNodePool{
				TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
				ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
					ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/my-nodepool")),
				},
			}

			mockDB := databasetesting.NewMockDBClient()
			spnpCRUD := mockDB.ServiceProviderNodePools("test-sub", "test-rg", "test-cluster", "my-nodepool")
			createdSPNP, err := spnpCRUD.Create(ctx, tt.initialSPNP, nil)
			require.NoError(t, err)
			provisionShard := buildTestProvisionShard("test-consumer")

			result, err := syncer.syncMaestroBundle(
				ctx,
				api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
				createdSPNP,
				nodePool,
				cluster,
				mockMaestro,
				spnpCRUD,
				provisionShard,
				"test-domain",
			)

			assert.Equal(t, tt.wantErr, err != nil)
			if tt.wantErr && tt.wantErrSubstr != "" {
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
			}
			require.NotNil(t, result)

			wantList := tt.wantServiceProviderNodePool.Status.MaestroReadonlyBundles
			gotList := result.Status.MaestroReadonlyBundles
			require.Len(t, gotList, len(wantList), "result should have the same number of bundle refs as want")
			for _, wantRef := range wantList {
				gotRef, err := gotList.Get(wantRef.Name)
				require.NoError(t, err)
				require.NotNil(t, gotRef, "result missing bundle ref for name %q", wantRef.Name)
				assert.Equal(t, wantRef.Name, gotRef.Name)
				assert.Equal(t, wantRef.MaestroAPIMaestroBundleName, gotRef.MaestroAPIMaestroBundleName)
			}
		})
	}
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_NodePoolNotFound(t *testing.T) {
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: "test-env",
			uuidV4Generator:                    uuid.NewRandom,
		},
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	// No node pool in DB -> Get returns NotFound -> SyncOnce returns nil (no work to do)
	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_NodePoolNotRegisteredInCS(t *testing.T) {
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: "test-env",
			uuidV4Generator:                    uuid.NewRandom,
		},
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "my-nodepool",
	}

	// Create a node pool with an empty ClusterServiceID (not yet registered in CS)
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
		// ServiceProviderProperties.ClusterServiceID is zero-value (empty)
	}
	nodePoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodePoolsCRUD.Create(ctx, nodePool, nil)
	require.NoError(t, err)

	// SyncOnce should return nil without attempting any CS or Maestro interactions
	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_GetServiceProviderNodePoolError(t *testing.T) {
	ctx := context.Background()

	baseMockDB := databasetesting.NewMockDBClient()

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "my-nodepool",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/my-nodepool")),
		},
	}

	clustersCRUD := baseMockDB.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)
	nodePoolsCRUD := baseMockDB.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err = nodePoolsCRUD.Create(ctx, nodePool, nil)
	require.NoError(t, err)

	// Use error-injecting wrapper to simulate SPNP Get error
	expectedError := fmt.Errorf("database error")
	mockDBClient := &errorInjectingDBClientForNodePoolCreate{
		MockDBClient: baseMockDB,
		spnpCRUD: &errorInjectingSPNPCRUD{
			getErr: expectedError,
		},
	}

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: "test-env",
			uuidV4Generator:                    uuid.NewRandom,
		},
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderNodePool")
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_AllBundlesAlreadySynced(t *testing.T) {
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: "test-env",
			uuidV4Generator:                    uuid.NewRandom,
		},
		cooldownChecker: &alwaysSyncCooldownChecker{},
		cosmosClient:    mockDBClient,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "my-nodepool",
	}

	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/my-nodepool")),
		},
	}
	nodePoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodePoolsCRUD.Create(ctx, nodePool, nil)
	require.NoError(t, err)

	// SPNP with all bundles already synced (both name and ID populated)
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{
					Name:                        api.MaestroBundleInternalNameReadonlyHypershiftNodePool,
					MaestroAPIMaestroBundleName: "bundle-name",
					MaestroAPIMaestroBundleID:   "bundle-id",
				},
			},
		},
	}
	spnpCRUD := mockDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	// Since all bundles are synced, no other calls should be made
	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_SyncLoopExecutesWithBundleCreation(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroReadonlyBundleHelper: maestroReadonlyBundleHelper{
			maestroSourceEnvironmentIdentifier: "test-env",
			maestroClientBuilder:               mockMaestroBuilder,
			uuidV4Generator:                    uuid.NewRandom,
		},
		cooldownChecker:      &alwaysSyncCooldownChecker{},
		cosmosClient:         mockDBClient,
		clusterServiceClient: mockClusterService,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "my-nodepool",
	}

	// Setup cluster
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Setup node pool
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool"))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: nodePoolResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/my-nodepool")),
		},
	}
	nodePoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err = nodePoolsCRUD.Create(ctx, nodePool, nil)
	require.NoError(t, err)

	// Setup SPNP with NO bundle reference (needs syncing)
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/my-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
		},
	}
	spnpCRUD := mockDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	// Setup cluster service mocks
	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	csCluster, err := arohcpv1alpha1.NewCluster().
		DomainPrefix("test-domain").
		Build()
	require.NoError(t, err)
	mockClusterService.EXPECT().
		GetCluster(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(csCluster, nil)

	// Setup maestro builder mock
	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	// Maestro Get will return NotFound (bundle doesn't exist yet)
	mockMaestroClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, k8serrors.NewNotFound(workv1.Resource("manifestwork"), "test-bundle"))

	// Maestro Create will be called to create the bundle
	createdBundle := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-bundle-name",
			Namespace: "test-consumer",
			UID:       "new-bundle-id",
		},
	}
	mockMaestroClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createdBundle, nil)

	// Execute SyncOnce
	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	// Verify that the SPNP was updated with the bundle reference
	updatedSPNP, err := spnpCRUD.Get(ctx, "default")
	require.NoError(t, err)
	require.NotNil(t, updatedSPNP)

	bundleRef, err := updatedSPNP.Status.MaestroReadonlyBundles.Get(api.MaestroBundleInternalNameReadonlyHypershiftNodePool)
	require.NoError(t, err)
	require.NotNil(t, bundleRef)
	assert.NotEmpty(t, bundleRef.MaestroAPIMaestroBundleName)
	assert.Equal(t, string(createdBundle.UID), bundleRef.MaestroAPIMaestroBundleID)
}
