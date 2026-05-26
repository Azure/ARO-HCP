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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
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
		CosmosMetadata: arm.CosmosMetadata{ResourceID: nodepoolResourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
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
							ResourceIdentifiers: []api.MaestroBundleResourceIdentifier{
								{
									APIVersion: "hypershift.openshift.io/v1beta1",
									Kind:       "NodePool",
									Resource:   "nodepools",
									Name:       "test-domain-test-nodepool",
									Namespace:  "ocm-test-env-11111111111111111111111111111111",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "complete bundle reference - ID unchanged",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
				Status: api.ServiceProviderNodePoolStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        bundleInternalName,
							MaestroAPIMaestroBundleName: "complete-bundle-name",
							MaestroAPIMaestroBundleID:   "complete-bundle-id",
							ResourceIdentifiers: []api.MaestroBundleResourceIdentifier{
								{
									APIVersion: "hypershift.openshift.io/v1beta1",
									Kind:       "NodePool",
									Resource:   "nodepools",
									Name:       "test-domain-test-nodepool",
									Namespace:  "ocm-test-env-11111111111111111111111111111111",
								},
							},
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
							ResourceIdentifiers: []api.MaestroBundleResourceIdentifier{
								{
									APIVersion: "hypershift.openshift.io/v1beta1",
									Kind:       "NodePool",
									Resource:   "nodepools",
									Name:       "test-domain-test-nodepool",
									Namespace:  "ocm-test-env-11111111111111111111111111111111",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple refs - only synced ref is updated, other refs unchanged",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
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
							ResourceIdentifiers: []api.MaestroBundleResourceIdentifier{
								{
									APIVersion: "hypershift.openshift.io/v1beta1",
									Kind:       "NodePool",
									Resource:   "nodepools",
									Name:       "test-domain-test-nodepool",
									Namespace:  "ocm-test-env-11111111111111111111111111111111",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "maestro get or create error - returns last persisted SPNP",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
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
							ResourceIdentifiers: []api.MaestroBundleResourceIdentifier{
								{
									APIVersion: "hypershift.openshift.io/v1beta1",
									Kind:       "NodePool",
									Resource:   "nodepools",
									Name:       "test-domain-test-nodepool",
									Namespace:  "ocm-test-env-11111111111111111111111111111111",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "no bundle ref initially - bundle name persisted then getOrCreate fails - returns SPNP with name set, no ID",
			initialSPNP: &api.ServiceProviderNodePool{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
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
				CosmosMetadata: arm.CosmosMetadata{ResourceID: nodepoolResourceID},
				TrackedResource: arm.TrackedResource{
					Resource: arm.Resource{
						ID:   nodepoolResourceID,
						Name: "test-nodepool",
					},
				},
				ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
					ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
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
				assert.Equal(t, wantRef.ResourceIdentifiers, gotRef.ResourceIdentifiers)
			}
		})
	}
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_NodePoolNotFound(t *testing.T) {
	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		nodePoolLister:                       &listertesting.SliceNodePoolLister{},
		serviceProviderNodePoolLister:        &listertesting.SliceServiceProviderNodePoolLister{},
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	// No nodepool in cache -> SyncOnce returns nil (no work to do)
	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_EmptyClusterServiceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockResourcesDBClient := databasetesting.NewMockResourcesDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: nodepoolResourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: nil,
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		nodePoolLister:                       &listertesting.SliceNodePoolLister{NodePools: []*api.HCPOpenShiftClusterNodePool{nodepool}},
		serviceProviderNodePoolLister:        &listertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: []*api.ServiceProviderNodePool{spnp}},
		clusterServiceClient:                 mockClusterService,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

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
		CosmosMetadata: arm.CosmosMetadata{ResourceID: nodepoolResourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
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
		nodePoolLister:                       &listertesting.SliceNodePoolLister{NodePools: []*api.HCPOpenShiftClusterNodePool{nodepool}},
		serviceProviderNodePoolLister:        &listertesting.SliceServiceProviderNodePoolLister{},
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
	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: nodepoolResourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
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

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		nodePoolLister:                       &listertesting.SliceNodePoolLister{NodePools: []*api.HCPOpenShiftClusterNodePool{nodepool}},
		serviceProviderNodePoolLister:        &listertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: []*api.ServiceProviderNodePool{spnp}},
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

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

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: nodepoolResourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	// SPNP with no bundle reference (needs syncing)
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
		},
	}
	spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = spnpCRUD.Create(ctx, spnp, nil)
	require.NoError(t, err)

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		nodePoolLister:                       &listertesting.SliceNodePoolLister{NodePools: []*api.HCPOpenShiftClusterNodePool{nodepool}},
		serviceProviderNodePoolLister:        &listertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: []*api.ServiceProviderNodePool{spnp}},
		clusterServiceClient:                 mockClusterService,
		maestroClientBuilder:                 mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), *nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	csCluster, err := arohcpv1alpha1.NewCluster().
		DomainPrefix("test-domain").
		Build()
	require.NoError(t, err)
	mockClusterService.EXPECT().
		GetCluster(gomock.Any(), *nodepool.ServiceProviderProperties.ClusterServiceID).
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

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
		HCPNodePoolName:   "test-nodepool",
	}

	nodepoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool"))
	nodepool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: nodepoolResourceID},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodepoolResourceID,
				Name: "test-nodepool",
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))),
		},
	}
	nodepoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	_, err := nodepoolsCRUD.Create(ctx, nodepool, nil)
	require.NoError(t, err)

	bundleInternalName := api.MaestroBundleInternalNameReadonlyHypershiftNodePool
	spnpResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool/serviceProviderNodePools/default"))
	spnp := &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
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

	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                      &alwaysSyncCooldownChecker{},
		resourcesDBClient:                    mockResourcesDBClient,
		nodePoolLister:                       &listertesting.SliceNodePoolLister{NodePools: []*api.HCPOpenShiftClusterNodePool{nodepool}},
		serviceProviderNodePoolLister:        &listertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: []*api.ServiceProviderNodePool{spnp}},
		clusterServiceClient:                 mockClusterService,
		maestroClientBuilder:                 mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier:   "test-env",
		maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
	}

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), *nodepool.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)

	csCluster, err := arohcpv1alpha1.NewCluster().DomainPrefix("test-domain").Build()
	require.NoError(t, err)
	mockClusterService.EXPECT().
		GetCluster(gomock.Any(), *nodepool.ServiceProviderProperties.ClusterServiceID).
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

const (
	nodePoolMaestroDeleteTestSubscriptionID    = "00000000-0000-0000-0000-000000000000"
	nodePoolMaestroDeleteTestResourceGroupName = "test-rg"
	nodePoolMaestroDeleteTestClusterName       = "test-cluster"
	nodePoolMaestroDeleteTestNodePoolName      = "test-nodepool"
	nodePoolMaestroDeleteTestClusterServiceID  = "/api/aro_hcp/v1alpha1/clusters/abc123"
	nodePoolMaestroDeleteTestNodePoolCSID      = nodePoolMaestroDeleteTestClusterServiceID + "/node_pools/" + nodePoolMaestroDeleteTestNodePoolName

	nodePoolMaestroDeleteTestRESTURL  = "https://maestro.example.com:8000"
	nodePoolMaestroDeleteTestGRPC     = "maestro.example.com:8090"
	nodePoolMaestroDeleteTestConsumer = "test-consumer"
	nodePoolMaestroDeleteTestEnv      = "test-env"
	// Stamp name must be 1-3 lowercase alphanumeric characters (fleet validation).
	nodePoolMaestroDeleteTestStampID = "ts1"
)

func nodePoolMaestroDeleteTestProvisionShardInternalID(t *testing.T) *api.InternalID {
	t.Helper()
	return api.Ptr(api.Must(api.NewInternalID(
		"/api/aro_hcp/v1alpha1/provision_shards/22222222-2222-2222-2222-222222222222")))
}

func nodePoolMaestroDeleteTestMaestroSourceID(t *testing.T) string {
	t.Helper()
	return maestro.GenerateMaestroSourceID(nodePoolMaestroDeleteTestEnv, nodePoolMaestroDeleteTestProvisionShardInternalID(t).ID())
}

func newNodePoolMaestroDeleteTestManagementCluster(t *testing.T) *fleet.ManagementCluster {
	t.Helper()
	mcRID := api.Must(fleet.ToManagementClusterResourceID(nodePoolMaestroDeleteTestStampID))
	shardID := api.Must(api.NewInternalID(
		"/api/aro_hcp/v1alpha1/provision_shards/22222222-2222-2222-2222-222222222222"))
	return &fleet.ManagementCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: mcRID},
		ResourceID:     mcRID,
		Spec: fleet.ManagementClusterSpec{
			SchedulingPolicy: fleet.ManagementClusterSchedulingPolicySchedulable,
		},
		Status: fleet.ManagementClusterStatus{
			AKSResourceID: api.Must(azcorearm.ParseResourceID(
				"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/test-mgmt")),
			PublicDNSZoneResourceID: api.Must(azcorearm.ParseResourceID(
				"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com")),
			HostedClustersSecretsKeyVaultURL:                     "https://kv-cx-secrets.vault.azure.net",
			HostedClustersManagedIdentitiesKeyVaultURL:           "https://kv-cx-mi.vault.azure.net",
			HostedClustersSecretsKeyVaultManagedIdentityClientID: "12345678-1234-1234-1234-123456789012",
			ClusterServiceProvisionShardID:                       ptr.To(shardID),
			MaestroConsumerName:                                  nodePoolMaestroDeleteTestConsumer,
			MaestroRESTAPIURL:                                    nodePoolMaestroDeleteTestRESTURL,
			MaestroGRPCTarget:                                    nodePoolMaestroDeleteTestGRPC,
			KubeApplierCosmosContainerName:                       "Manifests-MC-ts1",
		},
	}
}

func newNodePoolMaestroDeleteTestServiceProviderCluster(t *testing.T, mcResourceID *azcorearm.ResourceID) *api.ServiceProviderCluster {
	t.Helper()
	spcRID := api.Must(azcorearm.ParseResourceID(api.ToServiceProviderClusterResourceIDString(
		nodePoolMaestroDeleteTestSubscriptionID, nodePoolMaestroDeleteTestResourceGroupName, nodePoolMaestroDeleteTestClusterName)))
	return &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcRID},
		Status: api.ServiceProviderClusterStatus{
			ManagementClusterResourceID: mcResourceID,
		},
	}
}

func newNodePoolMaestroDeleteTestSPNP(t *testing.T, bundles api.MaestroBundleReferenceList) *api.ServiceProviderNodePool {
	t.Helper()
	spnpResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + nodePoolMaestroDeleteTestSubscriptionID +
			"/resourceGroups/" + nodePoolMaestroDeleteTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + nodePoolMaestroDeleteTestClusterName +
			"/nodePools/" + nodePoolMaestroDeleteTestNodePoolName +
			"/serviceProviderNodePools/default"))
	return &api.ServiceProviderNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spnpResourceID},
		Status: api.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: bundles,
		},
	}
}

func newNodePoolMaestroDeleteTestNodePool(t *testing.T, opts func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + nodePoolMaestroDeleteTestSubscriptionID +
			"/resourceGroups/" + nodePoolMaestroDeleteTestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + nodePoolMaestroDeleteTestClusterName +
			"/nodePools/" + nodePoolMaestroDeleteTestNodePoolName))
	nodePoolInternalID := api.Ptr(api.Must(api.NewInternalID(nodePoolMaestroDeleteTestNodePoolCSID)))
	np := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: nodePoolMaestroDeleteTestNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Platform: api.NodePoolPlatformProfile{
				OSDisk: api.OSDiskProfile{
					DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
					DiskType:               api.OsDiskTypeManaged,
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: nodePoolInternalID,
		},
	}
	if opts != nil {
		opts(np)
	}
	return np
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_shouldReconcileDelete(t *testing.T) {
	fixedNow := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{}
	bundles := api.MaestroBundleReferenceList{
		{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
	}

	tests := []struct {
		name     string
		nodePool *api.HCPOpenShiftClusterNodePool
		spnp     *api.ServiceProviderNodePool
		want     bool
	}{
		{
			name:     "all nil: false",
			nodePool: newNodePoolMaestroDeleteTestNodePool(t, nil),
			spnp:     newNodePoolMaestroDeleteTestSPNP(t, bundles),
			want:     false,
		},
		{
			name: "DeletionTimestamp only: false",
			nodePool: newNodePoolMaestroDeleteTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow}
			}),
			spnp: newNodePoolMaestroDeleteTestSPNP(t, bundles),
			want: false,
		},
		{
			name: "DeletionTimestamp + CSDeletionTimestamp but CSID set: false",
			nodePool: newNodePoolMaestroDeleteTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow}
			}),
			spnp: newNodePoolMaestroDeleteTestSPNP(t, bundles),
			want: false,
		},
		{
			name: "all node pool conditions met but no bundles: false",
			nodePool: newNodePoolMaestroDeleteTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow}
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			spnp: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{}),
			want: false,
		},
		{
			name: "all conditions met: true",
			nodePool: newNodePoolMaestroDeleteTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow}
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			spnp: newNodePoolMaestroDeleteTestSPNP(t, bundles),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncer.shouldReconcileDelete(tt.nodePool, tt.spnp)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCreateNodePoolScopedMaestroReadonlyBundlesSyncer_SyncOnce_Delete(t *testing.T) {
	fixedNow := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	readyToDelete := func(np *api.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		np.ServiceProviderProperties.ClusterServiceID = nil
	}

	mcResourceID := api.Must(fleet.ToManagementClusterResourceID(nodePoolMaestroDeleteTestStampID))

	tests := []struct {
		name                   string
		existingNodePool       *api.HCPOpenShiftClusterNodePool
		existingSPNP           *api.ServiceProviderNodePool
		existingSPC            *api.ServiceProviderCluster
		fleetResources         []any
		setupMocks             func(*maestro.MockMaestroClientBuilder, *maestro.MockClient)
		wantErr                bool
		wantErrSubstr          string
		wantRemainingBundles   int
		wantRemainingBundleRef *api.MaestroBundleInternalName
	}{
		{
			name: "nodepool not found: no-op",
		},
		{
			name: "nodepool not marked for deletion: no-op",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				// No ClusterServiceID so neither create nor delete reconciliation runs.
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
		},
		{
			name:             "no SPNP: no-op",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
		},
		{
			name:             "SPNP with empty bundle list: no-op",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPNP:     newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{}),
		},
		{
			name:             "ServiceProviderCluster not found: error",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			wantErr:              true,
			wantErrSubstr:        "ServiceProviderCluster not found",
			wantRemainingBundles: 1,
		},
		{
			name:             "SPC without management cluster: clears bundle refs",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, nil),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			wantRemainingBundles: 0,
		},
		{
			name:             "single bundle: successful delete and confirmed gone",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
			},
			wantRemainingBundles: 0,
		},
		{
			name:             "single bundle: delete ok but still exists in maestro, reference kept",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(&workv1.ManifestWork{}, nil)
			},
			wantRemainingBundles: 1,
		},
		{
			name:             "single bundle: delete ok but Get returns error, reference kept",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil, fmt.Errorf("maestro connection error"))
			},
			wantErr:              true,
			wantErrSubstr:        "failed to verify deletion of Maestro Bundle",
			wantRemainingBundles: 1,
		},
		{
			name:             "single bundle: maestro delete 404 then Get 404 treated as success",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
			},
			wantRemainingBundles: 0,
		},
		{
			name:             "single bundle: maestro error",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(fmt.Errorf("maestro connection error"))
			},
			wantErr:              true,
			wantErrSubstr:        "failed to delete Maestro Bundle",
			wantRemainingBundles: 1,
		},
		{
			name:             "multiple bundles: all succeed",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				{Name: "bundleB", MaestroAPIMaestroBundleName: "name-b", MaestroAPIMaestroBundleID: "id-b"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
				mc.EXPECT().Delete(gomock.Any(), "name-b", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-b", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-b"))
			},
			wantRemainingBundles: 0,
		},
		{
			name:             "multiple bundles: second delete fails",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				{Name: "bundleB", MaestroAPIMaestroBundleName: "name-b", MaestroAPIMaestroBundleID: "id-b"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-a"))
				mc.EXPECT().Delete(gomock.Any(), "name-b", metav1.DeleteOptions{}).Return(fmt.Errorf("maestro error"))
			},
			wantErr:                true,
			wantErrSubstr:          "failed to delete Maestro Bundle",
			wantRemainingBundles:   1,
			wantRemainingBundleRef: ptr.To(api.MaestroBundleInternalName("bundleB")),
		},
		{
			name:             "multiple bundles: first still exists after delete",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
				{Name: "bundleB", MaestroAPIMaestroBundleName: "name-b", MaestroAPIMaestroBundleID: "id-b"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-a", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-a", metav1.GetOptions{}).Return(&workv1.ManifestWork{}, nil)
				mc.EXPECT().Delete(gomock.Any(), "name-b", metav1.DeleteOptions{}).Return(nil)
				mc.EXPECT().Get(gomock.Any(), "name-b", metav1.GetOptions{}).Return(nil,
					k8serrors.NewNotFound(schema.GroupResource{}, "name-b"))
			},
			wantRemainingBundles:   1,
			wantRemainingBundleRef: ptr.To(api.MaestroBundleInternalName("bundleA")),
		},
		{
			name:             "bundle with empty maestro name: removed without maestro call",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "", MaestroAPIMaestroBundleID: ""},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(mc, nil)
			},
			wantRemainingBundles: 0,
		},
		{
			name:             "management cluster not in fleet DB",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources:       nil,
			wantErr:              true,
			wantErrSubstr:        "failed to get management cluster",
			wantRemainingBundles: 1,
		},
		{
			name:             "maestro client creation fails",
			existingNodePool: newNodePoolMaestroDeleteTestNodePool(t, readyToDelete),
			existingSPC:      newNodePoolMaestroDeleteTestServiceProviderCluster(t, mcResourceID),
			existingSPNP: newNodePoolMaestroDeleteTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "bundleA", MaestroAPIMaestroBundleName: "name-a", MaestroAPIMaestroBundleID: "id-a"},
			}),
			fleetResources: []any{newNodePoolMaestroDeleteTestManagementCluster(t)},
			setupMocks: func(mb *maestro.MockMaestroClientBuilder, mc *maestro.MockClient) {
				mb.EXPECT().NewClient(gomock.Any(), nodePoolMaestroDeleteTestRESTURL, nodePoolMaestroDeleteTestGRPC,
					nodePoolMaestroDeleteTestConsumer, nodePoolMaestroDeleteTestMaestroSourceID(t)).Return(nil, fmt.Errorf("client error"))
			},
			wantErr:              true,
			wantErrSubstr:        "failed to create Maestro client",
			wantRemainingBundles: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
			mockMaestroClient := maestro.NewMockClient(ctrl)

			if tt.setupMocks != nil {
				tt.setupMocks(mockMaestroBuilder, mockMaestroClient)
			}

			resources := []any{}
			if tt.existingNodePool != nil {
				resources = append(resources, tt.existingNodePool)
			}
			if tt.existingSPC != nil {
				resources = append(resources, tt.existingSPC)
			}
			if tt.existingSPNP != nil {
				resources = append(resources, tt.existingSPNP)
			}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			fleetDBClient, err := databasetesting.NewMockFleetDBClientWithResources(ctx, tt.fleetResources)
			require.NoError(t, err)

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			if tt.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tt.existingNodePool)
			}
			spnpForLister := []*api.ServiceProviderNodePool{}
			if tt.existingSPNP != nil {
				spnpForLister = append(spnpForLister, tt.existingSPNP)
			}

			syncer := &createNodePoolScopedMaestroReadonlyBundlesSyncer{
				cooldownChecker:                      &alwaysSyncCooldownChecker{},
				resourcesDBClient:                    mockResourcesDBClient,
				fleetDBClient:                        fleetDBClient,
				nodePoolLister:                       &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				serviceProviderNodePoolLister:        &listertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: spnpForLister},
				maestroSourceEnvironmentIdentifier:   nodePoolMaestroDeleteTestEnv,
				maestroClientBuilder:                 mockMaestroBuilder,
				maestroAPIMaestroBundleNameGenerator: maestro.NewMaestroAPIMaestroBundleNameGenerator(),
			}

			key := controllerutils.HCPNodePoolKey{
				SubscriptionID:    nodePoolMaestroDeleteTestSubscriptionID,
				ResourceGroupName: nodePoolMaestroDeleteTestResourceGroupName,
				HCPClusterName:    nodePoolMaestroDeleteTestClusterName,
				HCPNodePoolName:   nodePoolMaestroDeleteTestNodePoolName,
			}

			err = syncer.SyncOnce(ctx, key)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSubstr)
			} else {
				require.NoError(t, err)
			}

			if tt.existingSPNP != nil {
				spnpCRUD := mockResourcesDBClient.ServiceProviderNodePools(
					nodePoolMaestroDeleteTestSubscriptionID,
					nodePoolMaestroDeleteTestResourceGroupName,
					nodePoolMaestroDeleteTestClusterName,
					nodePoolMaestroDeleteTestNodePoolName,
				)
				updatedSPNP, err := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
				require.NoError(t, err)
				assert.Len(t, updatedSPNP.Status.MaestroReadonlyBundles, tt.wantRemainingBundles)

				if tt.wantRemainingBundleRef != nil {
					ref, err := updatedSPNP.Status.MaestroReadonlyBundles.Get(*tt.wantRemainingBundleRef)
					require.NoError(t, err)
					assert.NotNil(t, ref)
				}
			}
		})
	}
}
