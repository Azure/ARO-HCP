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
	"strings"
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

// errorInjectingResourcesDBClientForNodePoolCreate wraps mockResourcesDBClient to return error-injecting CRUDs.
type errorInjectingResourcesDBClientForNodePoolCreate struct {
	*databasetesting.MockResourcesDBClient
	spnpCRUD database.ServiceProviderNodePoolCRUD
}

func (e *errorInjectingResourcesDBClientForNodePoolCreate) ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName string) database.ServiceProviderNodePoolCRUD {
	if e.spnpCRUD != nil {
		return e.spnpCRUD
	}
	return e.MockResourcesDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName)
}

var _ database.ResourcesDBClient = &errorInjectingResourcesDBClientForNodePoolCreate{}

// errorInjectingSPNPCRUDForCreate wraps ServiceProviderNodePoolCRUD to allow error injection.
type errorInjectingSPNPCRUDForCreate struct {
	database.ServiceProviderNodePoolCRUD
	getErr error
}

func (e *errorInjectingSPNPCRUDForCreate) Get(ctx context.Context, resourceID string) (*api.ServiceProviderNodePool, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.ServiceProviderNodePoolCRUD.Get(ctx, resourceID)
}

var _ database.ServiceProviderNodePoolCRUD = &errorInjectingSPNPCRUDForCreate{}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_buildClusterEmptyNodePool(t *testing.T) {
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroSourceEnvironmentIdentifier:   "testenv",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	csClusterID := "11111111111111111111111111111111"
	csClusterDomainPrefix := "test-domprefix"
	csNodePoolID := "nodepool-id-1234"
	expectedNodePoolName := fmt.Sprintf("%s-%s", csClusterDomainPrefix, csNodePoolID)
	np := syncer.buildClusterEmptyNodePool(csClusterID, csClusterDomainPrefix, csNodePoolID)

	assert.NotNil(t, np)
	assert.Equal(t, "NodePool", np.Kind)
	assert.Equal(t, hsv1beta1.SchemeGroupVersion.String(), np.APIVersion)
	assert.Equal(t, expectedNodePoolName, np.Name)
	assert.Equal(t, fmt.Sprintf("ocm-%s-%s", syncer.maestroSourceEnvironmentIdentifier, csClusterID), np.Namespace)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_getNodePoolNamespace(t *testing.T) {
	envName := "testenv"
	csClusterID := "11111111111111111111111111111111"
	expected := fmt.Sprintf("ocm-%s-%s", envName, csClusterID)

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroSourceEnvironmentIdentifier: envName,
	}

	result := syncer.getNodePoolNamespace(envName, csClusterID)
	assert.Equal(t, expected, result)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_getNodePoolName(t *testing.T) {
	envName := "testenv"
	csClusterDomainPrefix := "test-domprefix"
	csNodePoolID := "nodepool-abc"
	expected := fmt.Sprintf("%s-%s", csClusterDomainPrefix, csNodePoolID)

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroSourceEnvironmentIdentifier: envName,
	}

	result := syncer.getNodePoolName(csClusterDomainPrefix, csNodePoolID)
	assert.Equal(t, expected, result)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_buildInitialMaestroBundleReferenceForNodePool(t *testing.T) {
	generator := maestro.NewMaestroAPIMaestroBundleNameGenerator()
	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool

	ref, err := buildInitialMaestroBundleReference(bundleInternalName, generator)
	require.NoError(t, err)

	assert.NotNil(t, ref)
	assert.Equal(t, bundleInternalName, ref.Name)
	assert.NotEmpty(t, ref.MaestroAPIMaestroBundleName)
	assert.Empty(t, ref.MaestroAPIMaestroBundleID)

	// Verify the name is a valid UUID
	_, err = uuid.Parse(ref.MaestroAPIMaestroBundleName)
	assert.NoError(t, err, "MaestroAPIMaestroBundleName should be a valid UUID")
}

func TestBuildInitialReadonlyMaestroBundleForNodePool(t *testing.T) {
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		maestroSourceEnvironmentIdentifier:   "testenv",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}

	csClusterDomainPrefix := "test-domprefix"
	maestroBundleNamespacedName := types.NamespacedName{
		Name:      "test-maestro-api-maestro-bundle-name",
		Namespace: "test-maestro-consumer",
	}
	expectedNodePoolName := fmt.Sprintf("%s-%s", csClusterDomainPrefix, strings.ToLower(nodepool.Name))

	bundle := syncer.buildInitialReadonlyMaestroBundleForNodePool(nodepool, csClusterDomainPrefix, maestroBundleNamespacedName)
	require.NotNil(t, bundle)

	assert.Equal(t, "test-maestro-api-maestro-bundle-name", bundle.Name)
	assert.Equal(t, "test-maestro-consumer", bundle.Namespace)
	require.Len(t, bundle.Spec.Workload.Manifests, 1)
	require.Len(t, bundle.Spec.ManifestConfigs, 1)

	// Verify manifest config
	manifestConfig := bundle.Spec.ManifestConfigs[0]
	assert.Equal(t, "nodepools", manifestConfig.ResourceIdentifier.Resource)
	assert.Equal(t, hsv1beta1.SchemeGroupVersion.Group, manifestConfig.ResourceIdentifier.Group)
	assert.Equal(t, expectedNodePoolName, manifestConfig.ResourceIdentifier.Name)
	expectedNamespace := "ocm-testenv-11111111111111111111111111111111"
	assert.Equal(t, expectedNamespace, manifestConfig.ResourceIdentifier.Namespace)

	expectedNodePool := syncer.buildClusterEmptyNodePool(
		nodepool.ServiceProviderProperties.ClusterServiceID.ID(),
		csClusterDomainPrefix,
		nodepool.ID.Name,
	)
	assert.Equal(t, expectedNodePool, bundle.Spec.Workload.Manifests[0].Object)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_syncMaestroBundle(t *testing.T) {
	syncMaestroBundleTestDeterministicUUID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")
	var syncMaestroBundleTestOtherBundleName api.MaestroBundleInternalName = "otherReadonlyBundle"

	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool

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
							Name:                        bundleInternalName,
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
							Name:                        bundleInternalName,
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
							Name:                        bundleInternalName,
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
							Name:                        bundleInternalName,
							MaestroAPIMaestroBundleName: "complete-bundle-name",
							MaestroAPIMaestroBundleID:   "complete-bundle-id",
						},
					},
				},
			},
		},
		{
			name: "multiple refs - only synced ref is updated, other refs unchanged",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
				ResourceID:     *spnpResourceID,
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        syncMaestroBundleTestOtherBundleName,
							MaestroAPIMaestroBundleName: "other-bundle-name",
							MaestroAPIMaestroBundleID:   "other-bundle-id",
						},
						{
							Name:                        bundleInternalName,
							MaestroAPIMaestroBundleName: "nodepool-bundle-name",
							MaestroAPIMaestroBundleID:   "",
						},
					},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				createdBundle := &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{Name: "nodepool-bundle-name", Namespace: "test-consumer", UID: "nodepool-bundle-uid"},
				}
				m.EXPECT().Get(gomock.Any(), "nodepool-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				m.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createdBundle, nil)
			},
			wantServiceProviderNodePool: &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        syncMaestroBundleTestOtherBundleName,
							MaestroAPIMaestroBundleName: "other-bundle-name",
							MaestroAPIMaestroBundleID:   "other-bundle-id",
						},
						{
							Name:                        bundleInternalName,
							MaestroAPIMaestroBundleName: "nodepool-bundle-name",
							MaestroAPIMaestroBundleID:   "nodepool-bundle-uid",
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
							Name:                        bundleInternalName,
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
							Name:                        bundleInternalName,
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
						return createdBundle, nil
					},
				)
			},
			wantServiceProviderNodePool: &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        bundleInternalName,
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
							Name:                        bundleInternalName,
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

			syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
				maestroSourceEnvironmentIdentifier:   "test-env",
				maestroAPIMaestroBundleNameGenerator: maestro.NewAlwaysSameNameMaestroAPIMaestroBundleNameGenerator(syncMaestroBundleTestDeterministicUUID.String()),
			}
			ctx := context.Background()
			nodepool := &api.HCPOpenShiftClusterNodePool{
				TrackedResource: arm.TrackedResource{
					Resource: arm.Resource{
						ID:   nodepoolResourceID,
						Name: "test-nodepool",
					},
				},
				ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
					ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
				},
			}

			mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
			spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools("test-sub", "test-rg", "test-cluster", "test-nodepool")
			createdSPNP, err := spnpCRUD.Create(ctx, tt.initialSPNP, nil)
			require.NoError(t, err)
			provisionShard := buildTestProvisionShard("test-consumer")

			result, err := syncer.syncMaestroBundle(
				ctx,
				bundleInternalName,
				createdSPNP,
				nodepool,
				mockMaestro,
				spnpCRUD,
				provisionShard,
				"test-domain",
			)

			assert.Equal(t, tt.wantErr, err != nil)
			if tt.wantErr {
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
				assert.Equal(t, wantRef.MaestroAPIMaestroBundleID, gotRef.MaestroAPIMaestroBundleID)
			}
		})
	}
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_NodePoolNotFound(t *testing.T) {
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	// No nodepool in DB -> Get returns NotFound -> SyncOnce returns nil (no work to do)
	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_EmptyClusterServiceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		clusterServiceClient:                 mockClusterService,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.InternalID{},
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	// Cluster service ID not yet populated: skip sync (no OCM / Maestro calls), even though a bundle still needs syncing.
	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)

	// SPNP should be unchanged (sync did not run).
	got, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	ref, err := got.Status.MaestroReadonlyBundles.Get(bundleInternalName)
	require.NoError(t, err)
	assert.Nil(t, ref, "Maestro bundle ref should not be created when ClusterServiceID is empty")
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_GetServiceProviderNodePoolError(t *testing.T) {
	ctx := context.Background()

	baseMockResourcesDBClient := databasetesting.NewMockResourcesDBClient()

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}

	nodepoolsCRUD := baseMockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	expectedError := fmt.Errorf("database error")
	mockResourcesDBClient := &errorInjectingResourcesDBClientForNodePoolCreate{
		MockResourcesDBClient: baseMockResourcesDBClient,
		spnpCRUD: &errorInjectingSPNPCRUDForCreate{
			getErr: expectedError,
		},
	}

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderNodePool")
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_AllBundlesAlreadySynced(t *testing.T) {
	ctx := context.Background()
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{
					Name:                        bundleInternalName,
					MaestroAPIMaestroBundleName: "bundle-name",
					MaestroAPIMaestroBundleID:   "bundle-id",
				},
			},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	// Since all bundles are synced, no cluster service or maestro calls should be made
	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_SyncLoopExecutesWithBundleCreation(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		clusterServiceClient:                 mockClusterService,
		maestroClientBuilder:                 mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	// SPNP with no bundle reference (needs syncing)
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	csCluster, err := arohcpv1alpha1.NewCluster().
		DomainPrefix("test-domain").
		Build()
	require.NoError(t, err)
	mockClusterService.EXPECT().
		GetCluster(gomock.Any(), nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(csCluster, nil)

	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	mockMaestroClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, k8serrors.NewNotFound(workv1.Resource("manifestwork"), "test-bundle"))

	createdBundle := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-bundle-name",
			Namespace: "test-consumer",
			UID:       "new-bundle-id",
		},
	}
	mockMaestroClient.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createdBundle, nil)

	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	updatedSPNP, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	require.NotNil(t, updatedSPNP)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	bundleRef, err := updatedSPNP.Status.MaestroReadonlyBundles.Get(bundleInternalName)
	require.NoError(t, err)
	require.NotNil(t, bundleRef)
	assert.NotEmpty(t, bundleRef.MaestroAPIMaestroBundleName)
	assert.Equal(t, string(createdBundle.UID), bundleRef.MaestroAPIMaestroBundleID)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_ProcessesPartiallySyncedBundles(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		clusterServiceClient:                 mockClusterService,
		maestroClientBuilder:                 mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		ResourceID:     *spnpResourceID,
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{
					Name:                        api.MaestroBundleInternalName("otherReadonlyBundle"),
					MaestroAPIMaestroBundleName: "other-bundle-name",
					MaestroAPIMaestroBundleID:   "other-bundle-id", // fully synced - never touched
				},
				{
					Name:                        bundleInternalName,
					MaestroAPIMaestroBundleName: "nodepool-bundle-name",
					MaestroAPIMaestroBundleID:   "", // partially synced - syncMaestroBundle will be called
				},
			},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	csCluster, err := arohcpv1alpha1.NewCluster().DomainPrefix("test-domain").Build()
	require.NoError(t, err)
	mockClusterService.EXPECT().
		GetCluster(gomock.Any(), nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(csCluster, nil)

	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	// Only the partially-synced bundle triggers a sync; maestro Get fails so we get an error
	mockMaestroClient.EXPECT().
		Get(gomock.Any(), "nodepool-bundle-name", gomock.Any()).
		Return(nil, fmt.Errorf("maestro API error"))

	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to sync Maestro Bundle")
	assert.Contains(t, err.Error(), "maestro API error")
}
