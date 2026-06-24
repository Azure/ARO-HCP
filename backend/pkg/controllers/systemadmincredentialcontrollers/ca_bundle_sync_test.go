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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestCABundleSyncSyncer_SyncOnce(t *testing.T) {
	desireName := systemadmincredhelpers.ReadDesireNameServingCA
	testCABundle := "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----\n"

	tests := []struct {
		name        string
		cluster     *api.HCPOpenShiftCluster
		spc         *api.ServiceProviderCluster
		readDesires []*kubeapplier.ReadDesire
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
		wantErr     bool
	}{
		{
			name:    "updates CA bundle from Secret",
			cluster: newTestCluster(),
			spc:     newTestSPC(testManagementClusterResourceID),
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				secret := &corev1.Secret{
					Data: map[string][]byte{
						"ca.crt": []byte(testCABundle),
					},
				}
				secretJSON, _ := json.Marshal(secret)
				rd.Status.KubeContent = &runtime.RawExtension{Raw: secretJSON}
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, testCABundle, spc.Status.ServingCABundle)
			},
		},
		{
			name:    "no-op when CA bundle already matches",
			cluster: newTestCluster(),
			spc: func() *api.ServiceProviderCluster {
				spc := newTestSPC(testManagementClusterResourceID)
				spc.Status.ServingCABundle = testCABundle
				return spc
			}(),
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				secret := &corev1.Secret{
					Data: map[string][]byte{
						"ca.crt": []byte(testCABundle),
					},
				}
				secretJSON, _ := json.Marshal(secret)
				rd.Status.KubeContent = &runtime.RawExtension{Raw: secretJSON}
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, testCABundle, spc.Status.ServingCABundle)
			},
		},
		{
			name:        "no ReadDesire found does nothing",
			cluster:     newTestCluster(),
			spc:         newTestSPC(testManagementClusterResourceID),
			readDesires: nil,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Empty(t, spc.Status.ServingCABundle)
			},
		},
		{
			name:    "empty KubeContent does nothing",
			cluster: newTestCluster(),
			spc:     newTestSPC(testManagementClusterResourceID),
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				// nil KubeContent
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Empty(t, spc.Status.ServingCABundle)
			},
		},
		{
			name:    "no recognized CA key does nothing",
			cluster: newTestCluster(),
			spc:     newTestSPC(testManagementClusterResourceID),
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				secret := &corev1.Secret{
					Data: map[string][]byte{
						"some-other-key": []byte("some-data"),
					},
				}
				secretJSON, _ := json.Marshal(secret)
				rd.Status.KubeContent = &runtime.RawExtension{Raw: secretJSON}
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Empty(t, spc.Status.ServingCABundle)
			},
		},
		{
			name:        "cluster not found does nothing",
			cluster:     nil,
			spc:         nil,
			readDesires: nil,
		},
		{
			name: "cluster with DeletionTimestamp does nothing",
			cluster: func() *api.HCPOpenShiftCluster {
				c := newTestClusterWithDeletion()
				return c
			}(),
			spc:         newTestSPC(testManagementClusterResourceID),
			readDesires: nil,
		},
		{
			name:    "tries tls.crt when ca.crt absent",
			cluster: newTestCluster(),
			spc:     newTestSPC(testManagementClusterResourceID),
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				secret := &corev1.Secret{
					Data: map[string][]byte{
						"tls.crt": []byte(testCABundle),
					},
				}
				secretJSON, _ := json.Marshal(secret)
				rd.Status.KubeContent = &runtime.RawExtension{Raw: secretJSON}
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Equal(t, testCABundle, spc.Status.ServingCABundle)
			},
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

			syncer := &caBundleSyncSyncer{
				cooldownChecker:   &alwaysSyncCooldownChecker{},
				resourcesDBClient: mockDB,
				readDesireLister: &listertesting.SliceReadDesireLister{
					Desires: tt.readDesires,
				},
			}

			err = syncer.SyncOnce(ctx, newTestClusterKey())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB)
			}
		})
	}
}
