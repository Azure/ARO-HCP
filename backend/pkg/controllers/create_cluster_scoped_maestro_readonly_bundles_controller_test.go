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

// errorInjectingDBClientForCreate wraps MockDBClient to return error-injecting CRUDs.
type errorInjectingDBClientForCreate struct {
	*databasetesting.MockDBClient
	spcCRUD database.ServiceProviderClusterCRUD
}

func (e *errorInjectingDBClientForCreate) ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName string) database.ServiceProviderClusterCRUD {
	if e.spcCRUD != nil {
		return e.spcCRUD
	}
	return e.MockDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName)
}

var _ database.DBClient = &errorInjectingDBClientForCreate{}

// errorInjectingSPCCRUDForCreate wraps ServiceProviderClusterCRUD to allow error injection.
type errorInjectingSPCCRUDForCreate struct {
	database.ServiceProviderClusterCRUD
	getErr error
}

func (e *errorInjectingSPCCRUDForCreate) Get(ctx context.Context, resourceID string) (*api.ServiceProviderCluster, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.ServiceProviderClusterCRUD.Get(ctx, resourceID)
}

var _ database.ServiceProviderClusterCRUD = &errorInjectingSPCCRUDForCreate{}

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_buildClusterEmptyHostedCluster(t *testing.T) {
	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		maestroSourceEnvironmentIdentifier: "testenv",
		uuidV4Generator:                    uuid.NewRandom,
	}

	csClusterID := "11111111111111111111111111111111"
	csClusterDomainPrefix := "test-domprefix"
	hc := syncer.buildClusterEmptyHostedCluster(csClusterID, csClusterDomainPrefix)

	assert.NotNil(t, hc)
	assert.Equal(t, "HostedCluster", hc.Kind)
	assert.Equal(t, hsv1beta1.SchemeGroupVersion.String(), hc.APIVersion)
	assert.Equal(t, csClusterDomainPrefix, hc.Name)
	assert.Equal(t, fmt.Sprintf("ocm-%s-%s", syncer.maestroSourceEnvironmentIdentifier, csClusterID), hc.Namespace)
}

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_getHostedClusterNamespace(t *testing.T) {
	envName := "testenv"
	csClusterID := "11111111111111111111111111111111"
	expected := fmt.Sprintf("ocm-%s-%s", envName, csClusterID)

	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		maestroSourceEnvironmentIdentifier: envName,
		uuidV4Generator:                    uuid.NewRandom,
	}

	result := syncer.getHostedClusterNamespace(envName, csClusterID)
	assert.Equal(t, expected, result)
}

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_buildInitialMaestroBundleReference(t *testing.T) {
	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		uuidV4Generator: uuid.NewRandom,
	}

	ref, err := syncer.buildInitialMaestroBundleReference(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)
	require.NoError(t, err)

	assert.NotNil(t, ref)
	assert.Equal(t, api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster, ref.Name)
	assert.NotEmpty(t, ref.MaestroAPIMaestroBundleName)
	assert.Empty(t, ref.MaestroAPIMaestroBundleID)

	// Verify the name is a valid UUID
	_, err = uuid.Parse(ref.MaestroAPIMaestroBundleName)
	assert.NoError(t, err, "MaestroAPIMaestroBundleName should be a valid UUID")
}

func TestBuildInitialReadonlyMaestroBundleForHostedCluster(t *testing.T) {
	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		maestroSourceEnvironmentIdentifier: "testenv",
		uuidV4Generator:                    uuid.NewRandom,
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID: clusterResourceID,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}

	csClusterDomainPrefix := "test-domprefix"
	maestroBundleNamespacedName := types.NamespacedName{
		Name:      "test-maestro-api-maestro-bundle-name",
		Namespace: "test-maestro-consumer",
	}

	bundle := syncer.buildInitialReadonlyMaestroBundleForHostedCluster(cluster, csClusterDomainPrefix, maestroBundleNamespacedName)
	require.NotNil(t, bundle)

	assert.Equal(t, "test-maestro-api-maestro-bundle-name", bundle.Name)
	assert.Equal(t, "test-maestro-consumer", bundle.Namespace)
	require.Len(t, bundle.Spec.Workload.Manifests, 1)
	require.Len(t, bundle.Spec.ManifestConfigs, 1)

	// Verify manifest config
	manifestConfig := bundle.Spec.ManifestConfigs[0]
	assert.Equal(t, "hostedclusters", manifestConfig.ResourceIdentifier.Resource)
	assert.Equal(t, hsv1beta1.SchemeGroupVersion.Group, manifestConfig.ResourceIdentifier.Group)
	assert.Equal(t, "test-domprefix", manifestConfig.ResourceIdentifier.Name)
	assert.Equal(t, "ocm-testenv-11111111111111111111111111111111", manifestConfig.ResourceIdentifier.Namespace)

	expectedHostedCluster := syncer.buildClusterEmptyHostedCluster(cluster.ServiceProviderProperties.ClusterServiceID.ID(), csClusterDomainPrefix)
	assert.Equal(t, expectedHostedCluster, bundle.Spec.Workload.Manifests[0].Object)
}

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_getOrCreateMaestroBundle(t *testing.T) {
	desiredBundle := &workv1.ManifestWork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-maestro-api-maestro-bundle-name",
			Namespace: "test-maestro-consumer",
		},
		Spec: workv1.ManifestWorkSpec{
			ManifestConfigs: []workv1.ManifestConfigOption{
				{
					ResourceIdentifier: workv1.ResourceIdentifier{
						Name:      "hostedcluster-name",
						Namespace: "ocm-testenv-11111111111111111111111111111111",
					},
				},
			},
		},
	}

	tests := []struct {
		name       string
		setupMock  func(*maestro.MockClient, *workv1.ManifestWork)
		wantBundle *workv1.ManifestWork
		wantErr    bool
		errSubstr  string
	}{
		{
			name: "returns existing bundle if it already exists",
			setupMock: func(m *maestro.MockClient, want *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(want, nil)
			},
			wantBundle: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-maestro-api-maestro-bundle-name", Namespace: "test-maestro-consumer", UID: "existing-uid",
				},
			},
		},
		{
			name: "creates new bundle if it does not exist",
			setupMock: func(m *maestro.MockClient, want *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				m.EXPECT().Create(gomock.Any(), desiredBundle, gomock.Any()).Return(want, nil)
			},
			wantBundle: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-maestro-api-maestro-bundle-name", Namespace: "test-maestro-consumer", UID: "new-uid",
				},
			},
		},
		{
			name: "returns existing bundle when internal call to create returns AlreadyExists and then the following get succeeds",
			setupMock: func(m *maestro.MockClient, want *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				m.EXPECT().Create(gomock.Any(), desiredBundle, gomock.Any()).Return(nil, k8serrors.NewAlreadyExists(schema.GroupResource{}, "test-maestro-api-maestro-bundle-name"))
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(want, nil)
			},
			wantBundle: &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-maestro-api-maestro-bundle-name", Namespace: "test-maestro-consumer", UID: "existing-uid",
				},
			},
		},
		{
			name: "returns error if it fails to get the bundle",
			setupMock: func(m *maestro.MockClient, _ *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(nil, fmt.Errorf("connection error"))
			},
			wantErr:   true,
			errSubstr: "failed to get Maestro Bundle",
		},
		{
			name: "returns error if it fails to create the bundle",
			setupMock: func(m *maestro.MockClient, _ *workv1.ManifestWork) {
				m.EXPECT().Get(gomock.Any(), "test-maestro-api-maestro-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "test-maestro-api-maestro-bundle-name"))
				m.EXPECT().Create(gomock.Any(), desiredBundle, gomock.Any()).Return(nil, fmt.Errorf("maestro API error"))
			},
			wantErr:   true,
			errSubstr: "failed to create Maestro Bundle: maestro API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := maestro.NewMockClient(ctrl)
			tt.setupMock(mockMaestro, tt.wantBundle)
			syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
				uuidV4Generator: uuid.NewRandom,
			}

			result, err := syncer.getOrCreateMaestroBundle(context.Background(), mockMaestro, desiredBundle)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantBundle, result)
			}
		})
	}
}
func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_syncMaestroBundle(t *testing.T) {
	// syncMaestroBundleTestDeterministicUUID is the fixed UUID used when testing "no bundle reference initially" so returned values are deterministic.
	syncMaestroBundleTestDeterministicUUID := uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee")

	// syncMaestroBundleTestOtherBundleName is a second bundle internal name used only in tests to exercise multiple refs in MaestroReadonlyBundles.
	var syncMaestroBundleTestOtherBundleName api.MaestroBundleInternalName = "otherReadonlyBundle"

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))

	tests := []struct {
		name                       string
		initialSPC                 *api.ServiceProviderCluster
		maestroClientSetupMock     func(*maestro.MockClient)
		wantServiceProviderCluster *api.ServiceProviderCluster
		wantErr                    bool
		wantErrSubstr              string
	}{
		{
			name: "existing reference but no ID - sets new ID and preserves name",
			initialSPC: &api.ServiceProviderCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
				ResourceID:     *spcResourceID,
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
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
			wantServiceProviderCluster: &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
							MaestroAPIMaestroBundleName: "existing-bundle-name",
							MaestroAPIMaestroBundleID:   "new-bundle-uid",
						},
					},
				},
			},
		},
		{
			name: "complete bundle reference - ID unchanged",
			initialSPC: &api.ServiceProviderCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
				ResourceID:     *spcResourceID,
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
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
			wantServiceProviderCluster: &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
							MaestroAPIMaestroBundleName: "complete-bundle-name",
							MaestroAPIMaestroBundleID:   "complete-bundle-id",
						},
					},
				},
			},
		},
		{
			name: "multiple refs - only synced ref is updated, other refs unchanged",
			initialSPC: &api.ServiceProviderCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
				ResourceID:     *spcResourceID,
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        syncMaestroBundleTestOtherBundleName,
							MaestroAPIMaestroBundleName: "other-bundle-name",
							MaestroAPIMaestroBundleID:   "other-bundle-id",
						},
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
							MaestroAPIMaestroBundleName: "hosted-cluster-bundle-name",
							MaestroAPIMaestroBundleID:   "",
						},
					},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				createdBundle := &workv1.ManifestWork{
					ObjectMeta: metav1.ObjectMeta{Name: "hosted-cluster-bundle-name", Namespace: "test-consumer", UID: "hosted-cluster-bundle-uid"},
				}
				m.EXPECT().Get(gomock.Any(), "hosted-cluster-bundle-name", gomock.Any()).Return(nil, k8serrors.NewNotFound(schema.GroupResource{}, "not-found"))
				m.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createdBundle, nil)
			},
			wantServiceProviderCluster: &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        syncMaestroBundleTestOtherBundleName,
							MaestroAPIMaestroBundleName: "other-bundle-name",
							MaestroAPIMaestroBundleID:   "other-bundle-id",
						},
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
							MaestroAPIMaestroBundleName: "hosted-cluster-bundle-name",
							MaestroAPIMaestroBundleID:   "hosted-cluster-bundle-uid",
						},
					},
				},
			},
		},
		{
			name: "maestro get or create error - returns last persisted SPC",
			initialSPC: &api.ServiceProviderCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
				ResourceID:     *spcResourceID,
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
							MaestroAPIMaestroBundleName: "bundle-name",
							MaestroAPIMaestroBundleID:   "",
						},
					},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				m.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("maestro connection error"))
			},
			wantServiceProviderCluster: &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
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
			initialSPC: &api.ServiceProviderCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
				ResourceID:     *spcResourceID,
				Status: api.ServiceProviderClusterStatus{
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
			wantServiceProviderCluster: &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
							MaestroAPIMaestroBundleName: syncMaestroBundleTestDeterministicUUID.String(),
							MaestroAPIMaestroBundleID:   "new-bundle-uid",
						},
					},
				},
			},
		},
		{
			name: "no bundle ref initially - bundle name persisted then getOrCreate fails - returns SPC with name set, no ID",
			initialSPC: &api.ServiceProviderCluster{
				CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
				ResourceID:     *spcResourceID,
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
				},
			},
			maestroClientSetupMock: func(m *maestro.MockClient) {
				deterministicName := syncMaestroBundleTestDeterministicUUID.String()
				// Name is persisted (first Replace) then getOrCreateMaestroBundle is called; Get fails
				m.EXPECT().Get(gomock.Any(), deterministicName, gomock.Any()).Return(nil, fmt.Errorf("maestro connection error"))
			},
			wantServiceProviderCluster: &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					MaestroReadonlyBundles: api.MaestroBundleReferenceList{
						{
							Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
							MaestroAPIMaestroBundleName: syncMaestroBundleTestDeterministicUUID.String(),
							MaestroAPIMaestroBundleID:   "", // not set yet - failure happened before Create
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
			syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
				maestroSourceEnvironmentIdentifier: "test-env",
				uuidV4Generator:                    deterministicUUIDGenerator,
			}
			ctx := context.Background()
			cluster := &api.HCPOpenShiftCluster{
				TrackedResource: arm.TrackedResource{
					Resource: arm.Resource{ID: clusterResourceID},
				},
				ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
					ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
				},
			}

			mockDB := databasetesting.NewMockDBClient()
			spcCRUD := mockDB.ServiceProviderClusters("test-sub", "test-rg", "test-cluster")
			createdSPC, err := spcCRUD.Create(ctx, tt.initialSPC, nil)
			require.NoError(t, err)
			provisionShard := buildTestProvisionShard("test-consumer")

			result, err := syncer.syncMaestroBundle(
				ctx,
				api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
				createdSPC,
				cluster,
				mockMaestro,
				spcCRUD,
				provisionShard,
				"test-domain",
			)

			assert.Equal(t, tt.wantErr, err != nil)
			require.NotNil(t, result)

			wantList := tt.wantServiceProviderCluster.Status.MaestroReadonlyBundles
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

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_generateNewMaestroAPIMaestroBundleName(t *testing.T) {
	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		uuidV4Generator: uuid.NewRandom,
	}

	// Test successful generation
	name1, err := syncer.generateNewMaestroAPIMaestroBundleName()
	require.NoError(t, err)
	assert.NotEmpty(t, name1)

	// Verify it's a valid UUID
	_, err = uuid.Parse(name1)
	assert.NoError(t, err, "Generated name should be a valid UUID")

	// Test that multiple calls generate different UUIDs
	name2, err := syncer.generateNewMaestroAPIMaestroBundleName()
	require.NoError(t, err)
	assert.NotEqual(t, name1, name2, "Multiple calls should generate different UUIDs")

	// Verify second name is also a valid UUID
	_, err = uuid.Parse(name2)
	assert.NoError(t, err, "Second generated name should also be a valid UUID")
}

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_buildInitialReadonlyMaestroBundle(t *testing.T) {
	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		uuidV4Generator: uuid.NewRandom,
	}

	maestroBundleNamespacedName := types.NamespacedName{
		Name:      "custom-bundle",
		Namespace: "custom-namespace",
	}

	// Test with a generic object (we use ConfigMap as an example)
	configMap := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "test-namespace",
		},
	}

	resourceIdentifier := workv1.ResourceIdentifier{
		Group:     "",
		Resource:  "configmaps",
		Name:      configMap.Name,
		Namespace: configMap.Namespace,
	}

	bundle := syncer.buildInitialReadonlyMaestroBundle(maestroBundleNamespacedName, resourceIdentifier, configMap)

	// Verify bundle metadata
	assert.NotNil(t, bundle)
	assert.Equal(t, "custom-bundle", bundle.Name)
	assert.Equal(t, "custom-namespace", bundle.Namespace)
	assert.Equal(t, "0", bundle.ResourceVersion)

	// Verify workload manifests
	require.Len(t, bundle.Spec.Workload.Manifests, 1)
	require.NotNil(t, bundle.Spec.Workload.Manifests[0].Object)
	assert.Equal(t, bundle.Spec.Workload.Manifests[0].Object, configMap)

	// Verify manifest configs
	require.Len(t, bundle.Spec.ManifestConfigs, 1)
	manifestConfig := bundle.Spec.ManifestConfigs[0]

	// Verify resource identifier
	assert.Equal(t, resourceIdentifier.Group, manifestConfig.ResourceIdentifier.Group)
	assert.Equal(t, resourceIdentifier.Resource, manifestConfig.ResourceIdentifier.Resource)
	assert.Equal(t, resourceIdentifier.Name, manifestConfig.ResourceIdentifier.Name)
	assert.Equal(t, resourceIdentifier.Namespace, manifestConfig.ResourceIdentifier.Namespace)

	// Verify update strategy is read-only
	require.NotNil(t, manifestConfig.UpdateStrategy)
	assert.Equal(t, workv1.UpdateStrategyTypeReadOnly, manifestConfig.UpdateStrategy.Type)

	// Verify feedback rules
	require.Len(t, manifestConfig.FeedbackRules, 1)
	feedbackRule := manifestConfig.FeedbackRules[0]
	assert.Equal(t, workv1.JSONPathsType, feedbackRule.Type)
	require.Len(t, feedbackRule.JsonPaths, 1)
	assert.Equal(t, "resource", feedbackRule.JsonPaths[0].Name)
	assert.Equal(t, "@", feedbackRule.JsonPaths[0].Path)
}

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_SyncOnce_ClusterNotFound(t *testing.T) {
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		maestroSourceEnvironmentIdentifier: "test-env",
		uuidV4Generator:                    uuid.NewRandom,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	// No cluster in DB -> Get returns NotFound -> SyncOnce returns nil (no work to do)
	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

// TestCreateMaestroReadonlyBundlesSyncer_SyncOnce_GetServiceProviderClusterError uses error-injecting wrappers
// to inject an error from ServiceProviderClusters().Get().
func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_SyncOnce_GetServiceProviderClusterError(t *testing.T) {
	ctx := context.Background()

	baseMockDB := databasetesting.NewMockDBClient()

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{Resource: arm.Resource{ID: clusterResourceID}},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}

	// Add the cluster to the database
	clustersCRUD := baseMockDB.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Use error-injecting wrapper to simulate SPC Get error
	expectedError := fmt.Errorf("database error")
	mockDBClient := &errorInjectingDBClientForCreate{
		MockDBClient: baseMockDB,
		spcCRUD: &errorInjectingSPCCRUDForCreate{
			getErr: expectedError,
		},
	}

	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		maestroSourceEnvironmentIdentifier: "test-env",
		uuidV4Generator:                    uuid.NewRandom,
	}

	err = syncer.SyncOnce(ctx, key)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get or create ServiceProviderCluster")
}

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_SyncOnce_AllBundlesAlreadySynced(t *testing.T) {
	ctx := context.Background()
	mockDBClient := databasetesting.NewMockDBClient()
	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		maestroSourceEnvironmentIdentifier: "test-env",
		uuidV4Generator:                    uuid.NewRandom,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	var syncMaestroBundleTestOtherBundleName api.MaestroBundleInternalName = "otherReadonlyBundle"
	// SPC with all bundles already synced (both name and ID populated)
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{
					Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
					MaestroAPIMaestroBundleName: "bundle-name",
					MaestroAPIMaestroBundleID:   "bundle-id",
				},
				{
					Name:                        syncMaestroBundleTestOtherBundleName,
					MaestroAPIMaestroBundleName: "bundle-nameother",
					MaestroAPIMaestroBundleID:   "bundle-idother",
				},
			},
		},
	}
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	// Since all bundles are synced, no other calls should be made (no cluster service or maestro calls)
	err = syncer.SyncOnce(ctx, key)
	assert.NoError(t, err)
}

func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_SyncOnce_SyncLoopExecutesWithBundleCreation(t *testing.T) {
	ctrl := gomock.NewController(t)

	ctx := context.Background()

	// Setup mocks
	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroClientBuilder:               mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier: "test-env",
		uuidV4Generator:                    uuid.NewRandom,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	// Setup cluster
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID: clusterResourceID,
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}

	// Create cluster in mock DB
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Setup ServiceProviderCluster with NO bundle reference (needs syncing)
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID: spcResourceID,
		},
		ResourceID: *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{},
		},
	}

	// Create SPC in mock DB
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
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

	// Execute SyncOnce - this should trigger the sync loop
	err = syncer.SyncOnce(ctx, key)
	require.NoError(t, err)

	// Verify that the ServiceProviderCluster was updated with the bundle reference
	updatedSPC, err := spcCRUD.Get(ctx, "default")
	require.NoError(t, err)
	require.NotNil(t, updatedSPC)

	bundleRef, err := updatedSPC.Status.MaestroReadonlyBundles.Get(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)
	require.NoError(t, err)
	require.NotNil(t, bundleRef)
	assert.NotEmpty(t, bundleRef.MaestroAPIMaestroBundleName)
	assert.Equal(t, string(createdBundle.UID), bundleRef.MaestroAPIMaestroBundleID)
}

// TestCreateMaestroReadonlyBundlesSyncer_SyncOnce_ProcessesPartiallySyncedBundles
// tests that when the SPC has one bundle fully synced and another partially synced, SyncOnce
// only attempts to sync the partial one. We mock syncMaestroBundle to fail (maestro Get returns error)
// to avoid complex full sync mocking.
func TestCreateClusterScopedMaestroReadonlyBundlesSyncer_SyncOnce_ProcessesPartiallySyncedBundles(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx := context.Background()

	mockDBClient := databasetesting.NewMockDBClient()
	mockClusterService := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestroClient := maestro.NewMockClient(ctrl)

	syncer := &createClusterScopedMaestroReadonlyBundlesSyncer{
		cooldownChecker:                    &alwaysSyncCooldownChecker{},
		cosmosClient:                       mockDBClient,
		clusterServiceClient:               mockClusterService,
		maestroClientBuilder:               mockMaestroBuilder,
		maestroSourceEnvironmentIdentifier: "test-env",
		uuidV4Generator:                    uuid.NewRandom,
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111")),
		},
	}
	clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	_, err := clustersCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)

	// SPC: one bundle fully synced (otherReadonlyBundle), one partially synced (ReadonlyHypershiftHostedCluster - name set, no ID).
	// Only the recognized bundle (ReadonlyHypershiftHostedCluster) is synced; the other is left as-is.
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		ResourceID:     *spcResourceID,
		Status: api.ServiceProviderClusterStatus{
			MaestroReadonlyBundles: api.MaestroBundleReferenceList{
				{
					Name:                        api.MaestroBundleInternalName("otherReadonlyBundle"),
					MaestroAPIMaestroBundleName: "other-bundle-name",
					MaestroAPIMaestroBundleID:   "other-bundle-id", // fully synced - never touched
				},
				{
					Name:                        api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster,
					MaestroAPIMaestroBundleName: "hosted-cluster-bundle-name",
					MaestroAPIMaestroBundleID:   "", // partially synced - syncMaestroBundle will be called
				},
			},
		},
	}
	spcCRUD := mockDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	provisionShard := buildTestProvisionShard("test-consumer")
	mockClusterService.EXPECT().
		GetClusterProvisionShard(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(provisionShard, nil)
	csCluster, err := arohcpv1alpha1.NewCluster().DomainPrefix("test-domain").Build()
	require.NoError(t, err)
	mockClusterService.EXPECT().
		GetCluster(gomock.Any(), cluster.ServiceProviderProperties.ClusterServiceID).
		Return(csCluster, nil)

	restEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	grpcEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	consumerName := provisionShard.MaestroConfig().ConsumerName()
	sourceID := maestro.GenerateMaestroSourceID("test-env", provisionShard.ID())
	mockMaestroBuilder.EXPECT().
		NewClient(gomock.Any(), restEndpoint, grpcEndpoint, consumerName, sourceID).
		Return(mockMaestroClient, nil)

	// Only the partially-synced bundle triggers a sync; maestro Get fails so we get an error without full Create flow
	mockMaestroClient.EXPECT().
		Get(gomock.Any(), "hosted-cluster-bundle-name", gomock.Any()).
		Return(nil, fmt.Errorf("maestro API error"))
	err = syncer.SyncOnce(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to sync Maestro Bundle")
	assert.Contains(t, err.Error(), "maestro API error")
}

// alwaysSyncCooldownChecker is a simple mock implementation of CooldownChecker
type alwaysSyncCooldownChecker struct{}

func (m *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

// buildTestProvisionShard creates a test provision shard for unit tests
func buildTestProvisionShard(maestroConsumerName string) *arohcpv1alpha1.ProvisionShard {
	provisionShard, err := arohcpv1alpha1.NewProvisionShard().
		ID("22222222222222222222222222222222").
		MaestroConfig(
			arohcpv1alpha1.NewProvisionShardMaestroConfig().
				ConsumerName(maestroConsumerName).
				RestApiConfig(
					arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().
						Url("https://maestro.example.com:443"),
				).
				GrpcApiConfig(
					arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().
						Url("https://maestro.example.com:444"),
				),
		).
		Build()
	if err != nil {
		panic(err)
	}

	return provisionShard
}
