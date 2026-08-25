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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterDelete struct {
	clock                utilsclock.PassiveClock
	resourcesDBClient    corecosmosstorage.ResourcesDBClient
	billingDBClient      billingcosmosstorage.BillingDBClient
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
	readDesireLister     kubeapplierlisters.ReadDesireLister
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
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		"OperationClusterDelete",
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

	if cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline != nil &&
		c.clock.Now().After(cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline.Time) {

		message := c.buildDeletionTimeoutMessage(ctx, operation, cluster)
		logger.Info("delete operation deadline exceeded, marking as failed",
			"deadline", cluster.ServiceProviderProperties.DeleteOperationCompletionDeadline.Time,
			"message", message)
		persistErr := &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeInternalServerError,
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

	// Hold the delete operation non-terminal until every ApplyDesire for the cluster
	// has been removed, surfacing a per-controller breakdown on the operation status.
	// Placed after the deadline check above so the timeout-failure path still fires if
	// this cleanup stalls.
	applyDesiresState, err := c.applyDesiresDeletionStatus(ctx, cluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check remaining ApplyDesires: %w", err))
	}
	if applyDesiresState.ProvisioningState != coreapi.ProvisioningStateSucceeded {
		logger.Info("waiting for ApplyDesires to be deleted before completing delete operation", "remaining", applyDesiresState.Message)
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

func (c *operationClusterDelete) shouldReconcileOperationAndResourceStatus(cluster *coreapi.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID != nil
}

// applyDesiresDeletionStatus reports the delete-operation status contributed by the
// ApplyDesires still present for the cluster, mirroring hostedClusterDeletionStatus.
// It returns a Succeeded state when no ApplyDesires remain — or when they are
// unreachable (missing ServiceProviderCluster, nil ManagementClusterResourceID, or no
// kube-applier client), which are treated as "gone" — and a Deleting state carrying a
// per-controller breakdown while any remain.
func (c *operationClusterDelete) applyDesiresDeletionStatus(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) (*operationbase.OperationState, error) {
	spc, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get ServiceProviderCluster: %w", err)
	}
	if spc.Status.ManagementClusterResourceID == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, spc.Status.ManagementClusterResourceID)
	if kubeApplierDBClient == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	applyDesireCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get kube-applier CRUD for ApplyDesires: %w", err)
	}

	total, breakdown, err := applyDesireControllerCounts(ctx, applyDesireCRUD)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting,
		fmt.Sprintf("%d ApplyDesire(s) still exist: %s", total, breakdown)), nil
}

// unknownApplyDesireController is the bucket used for ApplyDesires that carry no
// kubeapplierapi.TagControllerName tag.
const unknownApplyDesireController = "unknown"

// applyDesireControllerCounts iterates every ApplyDesire reachable through the given
// CRUD and returns the total count plus a stable, human-readable breakdown grouped by
// the authoring controller recorded in Tags[kubeapplierapi.TagControllerName].
// ApplyDesires with no controller tag are bucketed under "unknown", e.g.
// "2 for controller SomeController, 1 for controller unknown".
func applyDesireControllerCounts(ctx context.Context, applyDesireCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]) (int, string, error) {
	applyDesireIterator, err := applyDesireCRUD.List(ctx, &cosmosstorageutils.DBClientListResourceDocsOptions{})
	if err != nil {
		return 0, "", fmt.Errorf("failed to list ApplyDesire documents: %w", err)
	}

	countsByController := map[string]int{}
	total := 0
	for _, desire := range applyDesireIterator.Items(ctx) {
		controllerName := desire.Tags[kubeapplierapi.TagControllerName]
		if controllerName == "" {
			controllerName = unknownApplyDesireController
		}
		countsByController[controllerName]++
		total++
	}
	if err := applyDesireIterator.GetError(); err != nil {
		return 0, "", fmt.Errorf("error iterating ApplyDesire documents: %w", err)
	}

	parts := make([]string, 0, len(countsByController))
	for controllerName, count := range countsByController {
		parts = append(parts, fmt.Sprintf("%d for controller %s", count, controllerName))
	}
	slices.Sort(parts)
	return total, strings.Join(parts, ", "), nil
}

func (c *operationClusterDelete) reconcileOperationAndResourceStatus(ctx context.Context, operation *coreapi.Operation, cluster *coreapi.HCPOpenShiftCluster) error {
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

	newOperationStatus, newOperationError, err := operationbase.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, csClusterStatus, *clusterCSID)
	if err != nil {
		return utils.TrackError(err)
	}

	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, newOperationStatus, newOperationError, operationbase.PostAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationClusterDelete) buildDeletionTimeoutMessage(ctx context.Context, _ *coreapi.Operation, cluster *coreapi.HCPOpenShiftCluster) string {
	logger := utils.LoggerFromContext(ctx)

	errs := []error{}
	states := []*operationbase.OperationState{}

	if currState, err := clusterServiceDeletionStatus(cluster); err != nil {
		errs = append(errs, err)
	} else {
		states = append(states, currState.WithSource("clusterServiceDeletion"))
	}

	if currState, err := c.clusterServiceStatusForDeletion(ctx, cluster); err != nil {
		errs = append(errs, err)
	} else {
		states = append(states, currState.WithSource("clusterServiceStatus"))
	}

	if currState, err := c.remainingDescendantResources(ctx, cluster); err != nil {
		errs = append(errs, err)
	} else {
		states = append(states, currState.WithSource("descendantResources"))
	}

	if currState, err := c.hostedClusterDeletionStatus(ctx, cluster); err != nil {
		errs = append(errs, err)
	} else {
		states = append(states, currState.WithSource("hostedCluster"))
	}

	if currState, err := c.applyDesiresDeletionStatus(ctx, cluster); err != nil {
		errs = append(errs, err)
	} else {
		states = append(states, currState.WithSource("applyDesires"))
	}

	if err := errors.Join(errs...); err != nil {
		logger.Error(err, "errors building deletion timeout message")
	}
	if len(states) == 0 {
		return "cluster deletion did not complete before the deadline"
	}
	slices.SortStableFunc(states, operationbase.CompareOperationState)

	picked, err := operationbase.PickWorstOperationState(states)
	if err != nil {
		logger.Error(err, "failed to pick worst deletion state")
		return "cluster deletion did not complete before the deadline"
	}

	message := picked.Message
	if len(message) == 0 {
		return "cluster deletion did not complete before the deadline"
	}
	return fmt.Sprintf("cluster deletion did not complete before the deadline; %s", message)
}

// TODO scale this out better. We likely want something we can aggregate, e.g. a UserFacingDeleteProgress condition.
func clusterServiceDeletionStatus(cluster *coreapi.HCPOpenShiftCluster) (*operationbase.OperationState, error) {
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	if cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, "ClusterService deletion not yet dispatched"), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, fmt.Sprintf("ClusterService cluster %s still exists (deletion dispatched at %s)",
		cluster.ServiceProviderProperties.ClusterServiceID,
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Format(time.RFC3339))), nil
}

// TODO scale this out better. We likely want something we can aggregate, e.g. a UserFacingDeleteProgress condition.
func (c *operationClusterDelete) clusterServiceStatusForDeletion(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) (*operationbase.OperationState, error) {
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	csClusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, *cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		var ocmError *ocmerrors.Error
		if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
			return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
		}
		return nil, fmt.Errorf("failed to get ClusterService status: %w", err)
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, fmt.Sprintf("ClusterService state is %q", csClusterStatus.State())), nil
}

func (c *operationClusterDelete) shouldCountChild(child *cosmosstorageutils.TypedDocument) bool {
	lowered := strings.ToLower(child.ResourceType)
	if strings.Contains(lowered, strings.ToLower(coreapi.ControllerResourceTypeName)) {
		return false
	}
	if strings.Contains(lowered, strings.ToLower(coreapi.ManagementClusterContentResourceTypeName)) {
		return false
	}
	if strings.Contains(lowered, strings.ToLower(kubeapplierapi.ReadDesireResourceTypeName)) {
		return false
	}
	if strings.Contains(lowered, strings.ToLower(kubeapplierapi.ApplyDesireResourceTypeName)) {
		var partial struct {
			Spec struct {
				Type kubeapplierapi.ApplyDesireType `json:"type"`
			} `json:"spec"`
		}
		if json.Unmarshal(child.Properties, &partial) == nil && partial.Spec.Type == kubeapplierapi.ApplyDesireTypeDelete {
			return false
		}
	}
	return true
}

func (c *operationClusterDelete) remainingDescendantResources(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)
	typeCounts := map[string]int{}

	resourcesCRUD, err := c.resourcesDBClient.UntypedCRUD(*cluster.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create untyped CRUD: %w", err)
	}
	if err := countDescendants(ctx, resourcesCRUD, c.shouldCountChild, typeCounts); err != nil {
		return nil, err
	}

	spc, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		logger.Error(err, "failed to get ServiceProviderCluster for kube-applier resource count")
	}
	if spc != nil && spc.Status.ManagementClusterResourceID != nil {
		kaClient := c.kubeApplierDBClients.For(ctx, spc.Status.ManagementClusterResourceID)
		kaCRUD, kaErr := kaClient.UntypedCRUD(*cluster.ID)
		if kaErr != nil {
			logger.Error(kaErr, "failed to create kube-applier untyped CRUD")
		} else if err := countDescendants(ctx, kaCRUD, c.shouldCountChild, typeCounts); err != nil {
			logger.Error(err, "failed to count kube-applier descendant resources")
		}
	}

	if len(typeCounts) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}

	var parts []string
	for resourceType, count := range typeCounts {
		parts = append(parts, fmt.Sprintf("%d %s", count, resourceType))
	}
	slices.Sort(parts)
	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, fmt.Sprintf("remaining resources: %s", strings.Join(parts, ", "))), nil
}

func countDescendants(ctx context.Context, crud cosmosstorageutils.UntypedResourceCRUD, shouldCount func(*cosmosstorageutils.TypedDocument) bool, typeCounts map[string]int) error {
	childIterator, err := crud.ListRecursive(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list descendant resources: %w", err)
	}
	for _, child := range childIterator.Items(ctx) {
		if !shouldCount(child) {
			continue
		}
		typeCounts[child.ResourceType]++
	}
	if err := childIterator.GetError(); err != nil {
		return fmt.Errorf("error iterating descendant resources: %w", err)
	}
	return nil
}

func (c *operationClusterDelete) hostedClusterDeletionStatus(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) (*operationbase.OperationState, error) {
	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get cached HostedCluster: %w", err)
	}
	if hostedCluster == nil {
		return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateDeleting, "HostedCluster still exists"), nil
}
