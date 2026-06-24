// Copyright 2025 Microsoft Corporation
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

package systemadmincredentialcontrollers

import (
	"context"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestServingCAReadDesireCreatorSyncer_SyncOnce(t *testing.T) {
	tests := []struct {
		name               string
		cluster            *api.HCPOpenShiftCluster
		spc                *api.ServiceProviderCluster
		kubeApplierDesires []any
		verify             func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients)
		wantErr            bool
	}{
		{
			name:               "creates ReadDesire when none exists",
			cluster:            newTestClusterWithCSID(),
			spc:                newTestSPC(testManagementClusterResourceID),
			kubeApplierDesires: nil,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				kaClient := ka.For(ctx, testManagementClusterResourceID)
				require.NotNil(t, kaClient)
				readCRUD, err := kaClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err)
				rd, err := readCRUD.Get(ctx, systemadmincredhelpers.ReadDesireNameServingCA)
				require.NoError(t, err)
				assert.Equal(t, "", rd.Spec.TargetItem.Group)
				assert.Equal(t, "v1", rd.Spec.TargetItem.Version)
				assert.Equal(t, "secrets", rd.Spec.TargetItem.Resource)
				assert.Equal(t, servingCASecretName, rd.Spec.TargetItem.Name)
				assert.Equal(t, hcpNamespace(), rd.Spec.TargetItem.Namespace)
			},
		},
		{
			name:    "idempotent when ReadDesire already exists with matching target",
			cluster: newTestClusterWithCSID(),
			spc:     newTestSPC(testManagementClusterResourceID),
			kubeApplierDesires: func() []any {
				rd := newTestClusterScopedReadDesire(systemadmincredhelpers.ReadDesireNameServingCA)
				rd.Spec.TargetItem = kubeapplier.ResourceReference{
					Group:     "",
					Version:   "v1",
					Resource:  "secrets",
					Namespace: hcpNamespace(),
					Name:      servingCASecretName,
				}
				return []any{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				kaClient := ka.For(ctx, testManagementClusterResourceID)
				require.NotNil(t, kaClient)
				readCRUD, err := kaClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err)
				rd, err := readCRUD.Get(ctx, systemadmincredhelpers.ReadDesireNameServingCA)
				require.NoError(t, err)
				assert.Equal(t, servingCASecretName, rd.Spec.TargetItem.Name)
			},
		},
		{
			name:               "cluster not found does nothing",
			cluster:            nil,
			spc:                nil,
			kubeApplierDesires: nil,
		},
		{
			name:               "cluster without ClusterServiceID does nothing",
			cluster:            newTestCluster(), // no CSID
			spc:                newTestSPC(testManagementClusterResourceID),
			kubeApplierDesires: nil,
		},
		{
			name:    "cluster with DeletionTimestamp does nothing",
			cluster: newTestClusterWithDeletion(),
			spc:     newTestSPC(testManagementClusterResourceID),
		},
		{
			name:               "no ManagementClusterResourceID does nothing",
			cluster:            newTestClusterWithCSID(),
			spc:                newTestSPC(nil), // nil MC
			kubeApplierDesires: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{}
			if tt.cluster != nil {
				resources = append(resources, tt.cluster)
			}
			if tt.spc != nil {
				resources = append(resources, tt.spc)
			}

			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockKA := databasetesting.NewMockKubeApplierDBClients()
			mockKAClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, tt.kubeApplierDesires)
			require.NoError(t, err)
			mockKA.Register(testManagementClusterResourceID, mockKAClient)

			syncer := &servingCAReadDesireCreatorSyncer{
				cooldownChecker:                     &alwaysSyncCooldownChecker{},
				resourcesDBClient:                   mockDB,
				kubeApplierDBClients:                mockKA,
				hostedClusterNamespaceEnvIdentifier: testEnvIdentifier,
			}

			err = syncer.SyncOnce(ctx, newTestClusterKey())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB, mockKA)
			}
		})
	}
}
