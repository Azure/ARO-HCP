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

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterDelete struct {
	statusCalculators    operationbase.OperationStatusCalculators[clusterDeleteOperationStatusInput]
	clock                utilsclock.PassiveClock
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	billingDBClient      billingcosmosstorage.BillingDBClient
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
	readDesireLister     kubeapplierlisters.ReadDesireLister
	applyDesireLister    kubeapplierlisters.ApplyDesireLister
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

const OperationClusterDeleteControllerName = "OperationClusterDelete"

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
//     because the operationbase.SetDeleteOperationAsCompleted is still reused by other operations
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
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	billingDBClient billingcosmosstorage.BillingDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	applyDesireLister kubeapplierlisters.ApplyDesireLister,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationClusterDelete{
		clock:                clock,
		resourcesDBClient:    resourcesDBClient,
		billingDBClient:      billingDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
		readDesireLister:     readDesireLister,
		applyDesireLister:    applyDesireLister,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	// The registry is fixed in code. Invalid source registrations are startup errors.
	syncer.statusCalculators = metadataapi.Must(syncer.newStatusCalculators())

	controller := controllerutils.NewGenericOperationController(
		OperationClusterDeleteControllerName,
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationClusterDelete) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterDelete) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}

	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	clusterCRUD := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName)
	cluster, err := clusterCRUD.Get(ctx, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("cluster document deleted - completing operation")
		err = operationbase.SetDeleteOperationAsCompleted(ctx, c.clock, c.resourcesDBClient, operation, operationbase.PostAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}

	if operation.OperationID.Name != cluster.ServiceProviderProperties.ActiveOperationID {
		logger.Info("cluster active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.OperationID.Name, "clusterActiveOperationID", cluster.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	if cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline != nil &&
		c.clock.Now().After(cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline.Time) {

		message := c.buildDeletionTimeoutMessage(ctx, operation, cluster)
		logger.Info("delete operation deadline exceeded, marking as failed",
			"deadline", cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline.Time,
			"message", message)
		persistErr := &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeDeadlineExceeded,
			Message: message,
		}
		err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, coreapi.ProvisioningStateFailed, persistErr, operationbase.PostAsyncNotificationFn(c.notificationClient))
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
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

func (c *operationClusterDelete) shouldReconcileOperationAndResourceStatus(cluster *coreapi.Cluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID != nil
}

func (c *operationClusterDelete) reconcileOperationAndResourceStatus(ctx context.Context, operation *coreapi.Operation, cluster *coreapi.Cluster) error {
	logger := utils.LoggerFromContext(ctx)

	clusterCSID := cluster.ServiceProviderProperties.ClusterServiceID

	csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
	defer cancel()

	csClusterStatus, err := c.clusterServiceClient.GetClusterStatus(csCtx, *clusterCSID)
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

	newOperationStatus, newOperationError, err := operationbase.ConvertClusterStatus(csCtx, c.clusterServiceClient, operation, csClusterStatus, *clusterCSID)
	if err != nil {
		return utils.TrackError(err)
	}

	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, newOperationStatus, newOperationError, operationbase.PostAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// newStatusCalculators builds the fixed registry of delete-operation status
// sources from the syncer's dependencies. Production and tests share this single
// construction so the two cannot drift: a source added here is exercised by both
// the controller and every test that builds a registry. The checks observe the
// Cluster document that the controller already holds, which is passed as the
// shared input.
func (c *operationClusterDelete) newStatusCalculators() (operationbase.OperationStatusCalculators[clusterDeleteOperationStatusInput], error) {
	return operationbase.NewOperationStatusCalculators[clusterDeleteOperationStatusInput](
		&clusterDeleteClusterServiceDeletionCheck{},
		&clusterDeleteClusterServiceStatusCheck{clusterServiceClient: c.clusterServiceClient},
		&clusterDeleteDescendantResourcesCheck{resourcesDBClient: c.resourcesDBClient, kubeApplierDBClients: c.kubeApplierDBClients},
		&clusterDeleteHostedClusterCheck{readDesireLister: c.readDesireLister},
		&clusterDeleteResourceTeardownCheck{resourcesDBClient: c.resourcesDBClient, applyDesireLister: c.applyDesireLister},
	)
}

// buildDeletionTimeoutMessage summarizes why deletion did not finish before the
// deadline by evaluating the shared status-calculator registry over the Cluster
// document and merging the worst state's message onto the deadline sentence.
func (c *operationClusterDelete) buildDeletionTimeoutMessage(ctx context.Context, operation *coreapi.Operation, cluster *coreapi.Cluster) string {
	logger := utils.LoggerFromContext(ctx)

	const deadlineSentence = "cluster deletion did not complete before the deadline"

	state, err := c.statusCalculators.CalculateOperationStatus(ctx, operation, clusterDeleteOperationStatusInput{Cluster: cluster})
	if err != nil {
		logger.Error(err, "errors building deletion timeout message")
	}
	if state == nil || len(state.Message) == 0 {
		return deadlineSentence
	}
	return operationbase.DeadlineExceededMessage(deadlineSentence, state.Message)
}
