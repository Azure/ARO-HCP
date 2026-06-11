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
package backupcontroller

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	workv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDeleteOrphanedBackupManifestWorks_ensureOrphanedBackupManifestWorksAreDeleted(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))

	tests := []struct {
		name      string
		setupMock func(*maestro.MockClient) map[string]*backupShardServiceProviderClusters
		wantErr   bool
		errSubstr string
	}{
		{
			name: "empty index success",
			setupMock: func(*maestro.MockClient) map[string]*backupShardServiceProviderClusters {
				return nil
			},
		},
		{
			name: "list error",
			setupMock: func(m *maestro.MockClient) map[string]*backupShardServiceProviderClusters {
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("maestro list error"))
				return map[string]*backupShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to list Maestro Bundles",
		},
		{
			name: "skips bundle without backup managed-by label",
			setupMock: func(m *maestro.MockClient) map[string]*backupShardServiceProviderClusters {
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "other-bundle",
								Namespace: "consumer",
								Labels:    map[string]string{backupScheduleManagedByK8sLabelKey: "other-value"},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				return map[string]*backupShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
				}
			},
		},
		{
			name: "skips referenced backup ManifestWork",
			setupMock: func(m *maestro.MockClient) map[string]*backupShardServiceProviderClusters {
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					Status: api.ServiceProviderClusterStatus{
						BackupScheduleManifestWorkName: "my-cluster-dr",
					},
				}
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "my-cluster-dr",
								Namespace: "consumer",
								Labels:    map[string]string{backupScheduleManagedByK8sLabelKey: backupScheduleManagedByK8sLabelValue},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				return map[string]*backupShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{spc}},
				}
			},
		},
		{
			name: "deletes orphaned backup ManifestWork",
			setupMock: func(m *maestro.MockClient) map[string]*backupShardServiceProviderClusters {
				spc := &api.ServiceProviderCluster{
					CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
					Status: api.ServiceProviderClusterStatus{
						BackupScheduleManifestWorkName: "other-cluster-dr",
					},
				}
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "orphaned-cluster-dr",
								Namespace: "consumer",
								Labels:    map[string]string{backupScheduleManagedByK8sLabelKey: backupScheduleManagedByK8sLabelValue},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				m.EXPECT().Delete(gomock.Any(), "orphaned-cluster-dr", gomock.Any()).Return(nil)
				return map[string]*backupShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{spc}},
				}
			},
		},
		{
			name: "delete error joined but not fatal",
			setupMock: func(m *maestro.MockClient) map[string]*backupShardServiceProviderClusters {
				bundleList := &workv1.ManifestWorkList{
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "orphaned-cluster-dr",
								Namespace: "consumer",
								Labels:    map[string]string{backupScheduleManagedByK8sLabelKey: backupScheduleManagedByK8sLabelValue},
							},
						},
					},
				}
				m.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
				m.EXPECT().Delete(gomock.Any(), "orphaned-cluster-dr", gomock.Any()).Return(fmt.Errorf("delete failed"))
				return map[string]*backupShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
				}
			},
			wantErr:   true,
			errSubstr: "failed to delete backup ManifestWork",
		},
		{
			name: "pagination lists and deletes across pages",
			setupMock: func(m *maestro.MockClient) map[string]*backupShardServiceProviderClusters {
				page1 := &workv1.ManifestWorkList{
					ListMeta: metav1.ListMeta{Continue: "token"},
					Items: []workv1.ManifestWork{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "orphan-page1-dr",
								Namespace: "consumer",
								Labels:    map[string]string{backupScheduleManagedByK8sLabelKey: backupScheduleManagedByK8sLabelValue},
							},
						},
					},
				}
				page2 := &workv1.ManifestWorkList{Items: []workv1.ManifestWork{}}
				labelSelector := fmt.Sprintf("%s=%s", backupScheduleManagedByK8sLabelKey, backupScheduleManagedByK8sLabelValue)
				m.EXPECT().List(gomock.Any(), metav1.ListOptions{Limit: 400, Continue: "", LabelSelector: labelSelector}).Return(page1, nil)
				m.EXPECT().Delete(gomock.Any(), "orphan-page1-dr", gomock.Any()).Return(nil)
				m.EXPECT().List(gomock.Any(), metav1.ListOptions{Limit: 400, Continue: "token", LabelSelector: labelSelector}).Return(page2, nil)
				return map[string]*backupShardServiceProviderClusters{
					"shard-1": {maestroClient: m, maestroClientCancelFunc: func() {}, serviceProviderClusters: []*api.ServiceProviderCluster{}},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockMaestro := maestro.NewMockClient(ctrl)
			index := tt.setupMock(mockMaestro)
			c := &deleteOrphanedBackupManifestWorks{}
			err := c.ensureOrphanedBackupManifestWorksAreDeleted(ctx, index)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDeleteOrphanedBackupManifestWorks_SyncOnce_NoServiceProviderClusters_Success(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	mockDB := databasetesting.NewMockResourcesDBClient()
	mockFleetDB := databasetesting.NewMockFleetDBClient()
	controller := NewDeleteOrphanedBackupManifestWorksController(mockDB, mockFleetDB, nil, "test-env")

	err := controller.SyncOnce(ctx, nil)
	require.NoError(t, err)
}

func TestDeleteOrphanedBackupManifestWorks_SyncOnce_FullFlow_DeletesOrphanedBackupMW(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)
	mockDB := databasetesting.NewMockResourcesDBClient()
	mockFleetDB := databasetesting.NewMockFleetDBClient()
	mockMaestroBuilder := maestro.NewMockMaestroClientBuilder(ctrl)
	mockMaestro := maestro.NewMockClient(ctrl)

	testStampID := "mc1"
	testShardID := "22222222222222222222222222222222"

	mc := newTestManagementCluster(testStampID, testShardID, "test-consumer", "https://maestro.example.com:443", "maestro.example.com:444")
	_, err := mockFleetDB.Stamps().ManagementClusters(testStampID).Create(ctx, mc, nil)
	require.NoError(t, err)

	mgmtClusterResourceID := api.Must(fleet.ToManagementClusterResourceID(testStampID))

	spcResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/serviceProviderClusters/default"))
	spc := &api.ServiceProviderCluster{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: spcResourceID},
		Status: api.ServiceProviderClusterStatus{
			BackupScheduleManifestWorkName: "kept-cluster-dr",
			ManagementClusterResourceID:    mgmtClusterResourceID,
		},
	}
	spcCRUD := mockDB.ServiceProviderClusters("sub", "rg", "cluster")
	_, err = spcCRUD.Create(ctx, spc, nil)
	require.NoError(t, err)

	sourceID := maestro.GenerateMaestroSourceID("test-env", testShardID)
	mockMaestroBuilder.EXPECT().NewClient(gomock.Any(), "https://maestro.example.com:443", "maestro.example.com:444", "test-consumer", sourceID).Return(mockMaestro, nil)

	bundleList := &workv1.ManifestWorkList{
		Items: []workv1.ManifestWork{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kept-cluster-dr",
					Namespace: "consumer",
					Labels:    map[string]string{backupScheduleManagedByK8sLabelKey: backupScheduleManagedByK8sLabelValue},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphaned-cluster-dr",
					Namespace: "consumer",
					Labels:    map[string]string{backupScheduleManagedByK8sLabelKey: backupScheduleManagedByK8sLabelValue},
				},
			},
		},
	}
	mockMaestro.EXPECT().List(gomock.Any(), gomock.Any()).Return(bundleList, nil)
	mockMaestro.EXPECT().Delete(gomock.Any(), "orphaned-cluster-dr", gomock.Any()).Return(nil)

	controller := NewDeleteOrphanedBackupManifestWorksController(mockDB, mockFleetDB, mockMaestroBuilder, "test-env")
	err = controller.SyncOnce(ctx, nil)
	require.NoError(t, err)
}
