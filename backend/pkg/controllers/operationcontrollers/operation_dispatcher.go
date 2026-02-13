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

package operationcontrollers

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationDispatcher struct {
	cosmosClient             database.DBClient
	clusterServiceClient     ocm.ClusterServiceClientSpec
	initialClusterProperties map[string]string
}

func NewOperationDispatcher(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	initialClusterProperties map[string]string,
) OperationSynchronizer {
	return &operationDispatcher{
		cosmosClient:             cosmosClient,
		clusterServiceClient:     clusterServiceClient,
		initialClusterProperties: initialClusterProperties,
	}
}

func (d *operationDispatcher) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Dispatched {
		return false
	}
	return true
}

func (d *operationDispatcher) dispatchCreateCluster(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	cluster, err := cosmosClusterClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	csClusterBuilder, csAutoscalerBuilder, err := ocm.BuildCSCluster(operation.ExternalID, cluster, maps.Clone(d.initialClusterProperties), nil)
	if err != nil {
		return utils.TrackError(err)
	}

	csCluster, err := d.clusterServiceClient.PostCluster(ctx, csClusterBuilder, csAutoscalerBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	csClusterID, err := api.NewInternalID(csCluster.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	operation.InternalID = csClusterID
	cluster.ServiceProviderProperties.ClusterServiceID = csClusterID

	_, err = cosmosClusterClient.AddReplaceToTransaction(ctx, transaction, cluster, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchCreateNodePool(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	clusterName := operation.ExternalID.Parent.Name

	cosmosNodePoolClient := cosmosClusterClient.NodePools(clusterName)

	cluster, err := cosmosClusterClient.Get(ctx, clusterName)
	if err != nil {
		return utils.TrackError(err)
	}

	nodePool, err := cosmosNodePoolClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, nodePool, false)
	if err != nil {
		return utils.TrackError(err)
	}

	csNodePool, err := d.clusterServiceClient.PostNodePool(ctx, cluster.ServiceProviderProperties.ClusterServiceID, csNodePoolBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	csNodePoolID, err := api.NewInternalID(csNodePool.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	operation.InternalID = csNodePoolID
	nodePool.ServiceProviderProperties.ClusterServiceID = csNodePoolID

	_, err = cosmosNodePoolClient.AddReplaceToTransaction(ctx, transaction, nodePool, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchCreateExternalAuth(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	clusterName := operation.ExternalID.Parent.Name

	cosmosExternalAuthClient := cosmosClusterClient.ExternalAuth(clusterName)

	cluster, err := cosmosClusterClient.Get(ctx, clusterName)
	if err != nil {
		return utils.TrackError(err)
	}

	externalAuth, err := cosmosExternalAuthClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, externalAuth, false)
	if err != nil {
		return utils.TrackError(err)
	}

	csExternalAuth, err := d.clusterServiceClient.PostExternalAuth(ctx, cluster.ServiceProviderProperties.ClusterServiceID, csExternalAuthBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	csExternalAuthID, err := api.NewInternalID(csExternalAuth.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	operation.InternalID = csExternalAuthID
	externalAuth.ServiceProviderProperties.ClusterServiceID = csExternalAuthID

	_, err = cosmosExternalAuthClient.AddReplaceToTransaction(ctx, transaction, externalAuth, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchCreateResource(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	switch {
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()):
		return d.dispatchCreateCluster(ctx, operation, transaction)
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()):
		return d.dispatchCreateNodePool(ctx, operation, transaction)
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		return d.dispatchCreateExternalAuth(ctx, operation, transaction)
	}

	return fmt.Errorf("unhandled resource type: %s", operation.ExternalID.ResourceType)
}

func (d *operationDispatcher) dispatchUpdateCluster(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	cluster, err := cosmosClusterClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	oldClusterServiceCluster, err := d.clusterServiceClient.GetCluster(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	csClusterBuilder, csAutoscalerBuilder, err := ocm.BuildCSCluster(operation.ExternalID, cluster, nil, oldClusterServiceCluster)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = d.clusterServiceClient.UpdateClusterAutoscaler(ctx, operation.InternalID, csAutoscalerBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = d.clusterServiceClient.UpdateCluster(ctx, operation.InternalID, csClusterBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchUpdateNodePool(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	clusterName := operation.ExternalID.Parent.Name

	cosmosNodePoolClient := cosmosClusterClient.NodePools(clusterName)

	nodePool, err := cosmosNodePoolClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, nodePool, true)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = d.clusterServiceClient.UpdateNodePool(ctx, operation.InternalID, csNodePoolBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchUpdateExternalAuth(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	clusterName := operation.ExternalID.Parent.Name

	cosmosExternalAuthClient := cosmosClusterClient.ExternalAuth(clusterName)

	externalAuth, err := cosmosExternalAuthClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, externalAuth, true)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = d.clusterServiceClient.UpdateExternalAuth(ctx, operation.InternalID, csExternalAuthBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchUpdateResource(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	switch {
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()):
		return d.dispatchUpdateCluster(ctx, operation, transaction)
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()):
		return d.dispatchUpdateNodePool(ctx, operation, transaction)
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		return d.dispatchUpdateExternalAuth(ctx, operation, transaction)
	}

	return fmt.Errorf("unhandled resource type: %s", operation.ExternalID.ResourceType)
}

func (d *operationDispatcher) dispatchDeleteCluster(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	cluster, err := cosmosClusterClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	err = DeleteCluster(ctx, d.cosmosClient, d.clusterServiceClient, cluster, transaction)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchDeleteNodePool(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	clusterName := operation.ExternalID.Parent.Name

	nodePool, err := cosmosClusterClient.NodePools(clusterName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	err = DeleteNodePool(ctx, d.cosmosClient, d.clusterServiceClient, nodePool, transaction)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchDeleteExternalAuth(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	clusterName := operation.ExternalID.Parent.Name

	externalAuth, err := cosmosClusterClient.ExternalAuth(clusterName).Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	err = DeleteExternalAuth(ctx, d.cosmosClient, d.clusterServiceClient, externalAuth, transaction)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) dispatchDeleteResource(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	switch {
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()):
		return d.dispatchDeleteCluster(ctx, operation, transaction)
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()):
		return d.dispatchDeleteNodePool(ctx, operation, transaction)
	case strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		return d.dispatchDeleteExternalAuth(ctx, operation, transaction)
	}

	return fmt.Errorf("unhandled resource type: %s", operation.ExternalID.ResourceType)
}

func (d *operationDispatcher) dispatchRequestCredential(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	cluster, err := cosmosClusterClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	csCredential, err := d.clusterServiceClient.PostBreakGlassCredential(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(err)
	}

	csCredentialID, err := api.NewInternalID(csCredential.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	operation.InternalID = csCredentialID

	return nil
}

func (d *operationDispatcher) dispatchRevokeCredentials(ctx context.Context, operation *api.Operation, transaction database.DBTransaction) error {
	cosmosClusterClient := d.cosmosClient.HCPClusters(
		operation.ExternalID.SubscriptionID,
		operation.ExternalID.ResourceGroupName)

	cluster, err := cosmosClusterClient.Get(ctx, operation.ExternalID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	err = d.clusterServiceClient.DeleteBreakGlassCredentials(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(err)
	}

	// Just as deleting an ARM resource cancels any other operations on the resource,
	// revoking credentials cancels any credential requests in progress.
	err = database.CancelActiveOperations(ctx, d.cosmosClient, transaction, &database.DBClientListActiveOperationDocsOptions{
		Request:    api.Ptr(database.OperationRequestRequestCredential),
		ExternalID: operation.ExternalID,
	})
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (d *operationDispatcher) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := d.cosmosClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !d.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	transaction := d.cosmosClient.NewTransaction(operation.ExternalID.SubscriptionID)

	switch operation.Request {
	case api.OperationRequestCreate:
		err = d.dispatchCreateResource(ctx, operation, transaction)
	case api.OperationRequestUpdate:
		err = d.dispatchUpdateResource(ctx, operation, transaction)
	case api.OperationRequestDelete:
		err = d.dispatchDeleteResource(ctx, operation, transaction)
	case api.OperationRequestRequestCredential:
		err = d.dispatchRequestCredential(ctx, operation, transaction)
	case api.OperationRequestRevokeCredentials:
		err = d.dispatchRevokeCredentials(ctx, operation, transaction)
	default:
		err = fmt.Errorf("unhandled operation request type: %s", operation.Request)
	}

	if err != nil {
		return err
	}

	operation.Dispatched = true
	operation.LastTransitionTime = localClock.Now()

	// Henceforth, an error could leave Cosmos DB and Clusters Service in an inconsistent
	// state. If the only change is the Dispatched flag, this controller will do its best
	// to avoid a second dispatch on the operation. But if the failed replace includes a
	// new InternalID value for a create operation, then we'll have to rely on "mismatch"
	// controllers to clean up the orphaned resource.

	_, err = d.cosmosClient.Operations(operation.ExternalID.SubscriptionID).AddReplaceToTransaction(ctx, transaction, operation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
