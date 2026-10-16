// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package frontend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// Observe the existing transaction helper contract, not Cosmos atomicity itself.
type reservationTransactionDB struct {
	corecosmosstorage.ResourcesDBClient
	t           *testing.T
	transaction cosmosstorageutils.DBTransaction
	operation   *coreapi.Operation
	checked     bool
}

type reservationClusterCRUD struct {
	corecosmosstorage.HCPClusterCRUD
	db *reservationTransactionDB
}

type reservationOperationCRUD struct {
	corecosmosstorage.OperationCRUD
	db *reservationTransactionDB
}

func (d *reservationTransactionDB) HCPClusters(sub, rg string) corecosmosstorage.HCPClusterCRUD {
	return &reservationClusterCRUD{HCPClusterCRUD: d.ResourcesDBClient.HCPClusters(sub, rg), db: d}
}

func (d *reservationTransactionDB) Operations(sub string) corecosmosstorage.OperationCRUD {
	return &reservationOperationCRUD{OperationCRUD: d.ResourcesDBClient.Operations(sub), db: d}
}

func (c *reservationOperationCRUD) AddCreateToTransaction(ctx context.Context, tx cosmosstorageutils.DBTransaction, op *coreapi.Operation, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	c.db.transaction, c.db.operation = tx, op.DeepCopy()
	return c.OperationCRUD.AddCreateToTransaction(ctx, tx, op, opts)
}

func (c *reservationClusterCRUD) AddReplaceToTransaction(ctx context.Context, tx cosmosstorageutils.DBTransaction, cluster *coreapi.Cluster, opts *azcosmos.TransactionalBatchItemOptions) (string, error) {
	t := c.db.t
	require.Same(t, c.db.transaction, tx, "operation and cluster must share the existing acceptance transaction")
	op := c.db.operation
	require.NotNil(t, op)
	require.Equal(t, op.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID.String(), cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID.String())
	require.True(t, op.StartTime.Equal(cluster.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt.Time))
	live, err := c.Get(ctx, cluster.ID.Name)
	require.NoError(t, err)
	require.NotEmpty(t, cluster.CosmosETag)
	require.Equal(t, live.CosmosETag, cluster.CosmosETag, "acceptance retains observed cluster ETag")
	require.Nil(t, live.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID, "reservation is not persisted before transaction execution")
	c.db.checked = true
	return c.HCPClusterCRUD.AddReplaceToTransaction(ctx, tx, cluster, opts)
}
