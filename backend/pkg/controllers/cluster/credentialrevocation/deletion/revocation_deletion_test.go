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

package deletion

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestRevocationDeletion_SyncOnce(t *testing.T) {
	testKey := controllerutils.SystemAdminCredentialRevocationKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		RevocationName:    testRevocationName,
	}

	tests := []struct {
		name        string
		setupDB     func(db *corecosmosstoragetesting.MockResourcesDBClient)
		expectError bool
	}{
		{
			name:        "revocation not found returns nil",
			setupDB:     func(db *corecosmosstoragetesting.MockResourcesDBClient) {},
			expectError: false,
		},
		{
			name: "revocation without DeletionTimestamp is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestRevocation(t, db, testRevocationName)
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			tc.setupDB(db)

			syncer := &revocationDeletion{
				resourcesDBClient: db,
			}

			err := syncer.SyncOnce(ctx, testKey)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type interruptedDeletionDB struct {
	corecosmosstorage.ResourcesDBClient
	fail bool
}
type interruptedClusterCRUD struct {
	corecosmosstorage.HCPClusterCRUD
	db *interruptedDeletionDB
}
type interruptedRevocationCRUD struct {
	corecosmosstorage.SystemAdminCredentialRevocationsCRUD
	db *interruptedDeletionDB
}

func (db *interruptedDeletionDB) HCPClusters(sub, rg string) corecosmosstorage.HCPClusterCRUD {
	return &interruptedClusterCRUD{HCPClusterCRUD: db.ResourcesDBClient.HCPClusters(sub, rg), db: db}
}
func (c *interruptedClusterCRUD) SystemAdminCredentialRevocations(cluster string) corecosmosstorage.SystemAdminCredentialRevocationsCRUD {
	return &interruptedRevocationCRUD{SystemAdminCredentialRevocationsCRUD: c.HCPClusterCRUD.SystemAdminCredentialRevocations(cluster), db: c.db}
}
func (c *interruptedRevocationCRUD) Delete(ctx context.Context, name string) error {
	if c.db.fail {
		c.db.fail = false
		return errors.New("interrupted after release")
	}
	return c.SystemAdminCredentialRevocationsCRUD.Delete(ctx, name)
}

func TestRevocationDeletionReleaseRecovery(t *testing.T) {
	for _, newerOwner := range []bool{false, true} {
		t.Run(fmt.Sprint(newerOwner), func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			rev := createTestRevocation(t, db, testRevocationName, func(r *coreapi.SystemAdminCredentialRevocation) {
				now := metav1.Now()
				r.Status.DeletionTimestamp = &now
			})
			id := rev.ResourceID.Parent
			cluster := &coreapi.Cluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: id.SubscriptionID}, TrackedResource: coreapi.TrackedResource{Resource: coreapi.Resource{ID: id}}, ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{SystemAdminCredentialRevocationResourceID: rev.ResourceID, RevokeCredentialsOperationID: rev.Spec.OperationID}}
			stamp := metav1.NewTime(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
			cluster.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt = &stamp
			crud := db.HCPClusters(id.SubscriptionID, id.ResourceGroupName)
			_, err := crud.Create(ctx, cluster, nil)
			require.NoError(t, err)
			mc := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/mc/providers/Microsoft.ContainerService/managedClusters/mc"))
			spcID := metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/serviceProviderClusters/default"))
			spc := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spcID}, Status: coreapi.ServiceProviderClusterStatus{ManagementClusterResourceID: mc}}
			clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
			clients.Register(mc, kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient())
			interrupted := &interruptedDeletionDB{ResourcesDBClient: db, fail: true}
			c := &revocationDeletion{resourcesDBClient: interrupted, kubeApplierDBClients: clients, serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{spc}}}
			key := controllerutils.SystemAdminCredentialRevocationKey{SubscriptionID: id.SubscriptionID, ResourceGroupName: id.ResourceGroupName, HCPClusterName: id.Name, RevocationName: rev.ResourceID.Name}
			require.ErrorContains(t, c.SyncOnce(ctx, key), "interrupted after release")
			live, err := crud.Get(ctx, id.Name)
			require.NoError(t, err)
			require.Nil(t, live.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID)
			require.Nil(t, live.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt)
			require.Empty(t, live.ServiceProviderProperties.RevokeCredentialsOperationID)
			if newerOwner {
				live.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = metadataapi.Must(azcorearm.ParseResourceID(id.String() + "/systemAdminCredentialRevocations/newer"))
				live.ServiceProviderProperties.RevokeCredentialsOperationID = "newer"
				live.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt = &stamp
				_, err = crud.Replace(ctx, live, nil)
				require.NoError(t, err)
			}
			require.NoError(t, c.SyncOnce(ctx, key))
			_, err = crud.SystemAdminCredentialRevocations(id.Name).Get(ctx, rev.ResourceID.Name)
			require.True(t, cosmosstorageutils.IsNotFoundError(err))
			live, err = crud.Get(ctx, id.Name)
			require.NoError(t, err)
			if newerOwner {
				require.Equal(t, "newer", live.ServiceProviderProperties.RevokeCredentialsOperationID)
				require.True(t, stamp.Equal(live.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt))
			} else {
				require.Nil(t, live.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID)
			}
		})
	}
}
