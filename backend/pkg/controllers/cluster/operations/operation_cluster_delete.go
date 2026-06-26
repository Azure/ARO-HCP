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

package operations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	sharedops "github.com/Azure/ARO-HCP/backend/pkg/controllers/shared/operations"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterDelete struct {
	clock                utilsclock.PassiveClock
	resourcesDBClient    database.ResourcesDBClient
	billingDBClient      database.BillingDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

// NewOperationClusterDeleteController returns a new Controller instance that
// follows an asynchronous cluster deletion operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// The controller has the following responsibilities:
//   - While the Cluster Cosmos document is present, it reconciles the
//     operation and the cluster status.
//   - When the Cluster Cosmos document is deleted (by the clusterDeletionController),
//     it marks the operation as Succeeded. It also cleans up child
//     resources. Note: This last part is handled by other controllers too but
//     because the sharedops.SetDeleteOperationAsCompleted is still reused by other operations
//     that have not been migrated to asynchronous flow yet this remains.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: Delete
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationClusterDeleteController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	billingDBClient database.BillingDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationClusterDelete{
		clock:                clock,
		resourcesDBClient:    resourcesDBClient,
		billingDBClient:      billingDBClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	controller := sharedops.NewGenericOperationController(
		"OperationClusterDelete",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationClusterDelete) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterDelete) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}

	// TODO remove this once migration of cluster deletion from frontend to backend is fully completed.
	if !operation.UsesNewClusterDeletionApproach {
		return c.legacySynchronizeOperation(ctx, operation)
	}

	// From here, we know it uses the new deletion approach.

	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	clusterCRUD := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName)
	cluster, err := clusterCRUD.Get(ctx, operation.ExternalID.Name)
	if database.IsNotFoundError(err) {
		logger.Info("cluster document deleted - completing operation")
		err = sharedops.SetDeleteOperationAsCompleted(ctx, c.clock, c.resourcesDBClient, operation, sharedops.PostAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}

	if !c.shouldReconcileOperationAndResourceStatus(cluster) {
		return nil
	}
	err = c.reconcileOperationAndResourceStatus(ctx, operation, cluster)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationClusterDelete) shouldReconcileOperationAndResourceStatus(cluster *api.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationClusterDelete) reconcileOperationAndResourceStatus(ctx context.Context, operation *api.Operation, cluster *api.HCPOpenShiftCluster) error {
	logger := utils.LoggerFromContext(ctx)

	clusterCSID := cluster.ServiceProviderProperties.ClusterServiceID

	csClusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, *clusterCSID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if !errors.As(err, &ocmError) || ocmError.Status() != http.StatusNotFound {
			return utils.TrackError(fmt.Errorf("failed to get cluster-service Cluster status: %w", err))
		}
		// 404 - CS has finished deleting. clusterClusterServiceIDClearer will clear the ID.
		logger.Info("cluster-service Cluster gone - skipping operation update", "clusterServiceID", clusterCSID.String())
		return nil
	}

	// If the cluster is in the Ready state from CS side, we wait until the Cosmos Cluster document is deleted, which
	// will be picked up by a next reconciliation of this controller and we will update the operation to Succeeded.
	if csClusterStatus.State() == arohcpv1alpha1.ClusterStateReady {
		logger.Info("cluster-service Cluster in Ready state. Waiting until Cosmos Cluster document is deleted.")
		return nil
	}

	newOperationStatus, newOperationError, err := sharedops.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, csClusterStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = sharedops.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, newOperationStatus, newOperationError, sharedops.PostAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
