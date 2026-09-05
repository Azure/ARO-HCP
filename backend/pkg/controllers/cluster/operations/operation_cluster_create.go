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

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterCreate struct {
	clock                                 utilsclock.PassiveClock
	activeOperationLister                 corelisters.ActiveOperationLister
	clusterLister                         corelisters.ClusterLister
	serviceProviderClusterLister          corelisters.ServiceProviderClusterLister
	clusterManagementClusterContentLister corelisters.ManagementClusterContentLister
	readDesireLister                      kubeapplierlisters.ReadDesireLister
	resourcesDBClient                     corecosmosstorage.ResourcesDBClient
	clusterServiceClient                  ocm.ClusterServiceClientSpec
	notificationClient                    *http.Client
}

// NewOperationClusterCreateController returns a new Controller instance that
// follows an asynchronous cluster creation operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: Create
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationClusterCreateController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	informers coreinformers.BackendInformers,
	readDesireLister kubeapplierlisters.ReadDesireLister,
) controllerutils.Controller {
	_, activeOperationLister := informers.ActiveOperations()
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, clusterManagementClusterContentLister := informers.ManagementClusterContents()
	syncer := &operationClusterCreate{
		clock:                                 clock,
		activeOperationLister:                 activeOperationLister,
		clusterLister:                         clusterLister,
		serviceProviderClusterLister:          serviceProviderClusterLister,
		clusterManagementClusterContentLister: clusterManagementClusterContentLister,
		readDesireLister:                      readDesireLister,
		resourcesDBClient:                     resourcesDBClient,
		clusterServiceClient:                  clusterServiceClient,
		notificationClient:                    notificationClient,
	}

	controller := controllerutils.NewGenericOperationController(
		"OperationClusterCreate",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationClusterCreate) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestCreate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterCreate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.activeOperationLister.Get(ctx, key.SubscriptionID, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("cluster not found in cache, waiting")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster to resolve ClusterServiceID: %w", err))
	}
	if operation.OperationID.Name != cluster.ServiceProviderProperties.ActiveOperationID {
		logger.Info("cluster active operation id mismatch, returning early",
			"synchronizedActiveOperationID", operation.OperationID.Name,
			"clusterActiveOperationID", cluster.ServiceProviderProperties.ActiveOperationID)
		return nil
	}
	if !c.shouldReconcileOperationAndResourceStatus(cluster) {
		return nil
	}
	operationalState, err := c.determineOperationState(ctx, operation, cluster)
	if err != nil {
		return utils.TrackError(err)
	}

	var persistErr *coreapi.CloudErrorBody
	if operationalState.ProvisioningState == coreapi.ProvisioningStateFailed {
		persistErr = &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeForMessage(operationalState.Message, coreapi.CloudErrorCodeInternalServerError),
			Message: operationalState.Message,
		}
	}

	if !operationalState.ProvisioningState.IsTerminal() &&
		cluster.ServiceProviderProperties.CreateOperationCompletionDeadline != nil &&
		c.clock.Now().After(cluster.ServiceProviderProperties.CreateOperationCompletionDeadline.Time) {

		message := "cluster creation did not complete before the deadline"
		if len(operationalState.Message) > 0 {
			message = operationalState.Message
		}
		logger.Info("create operation deadline exceeded, marking as failed",
			"deadline", cluster.ServiceProviderProperties.CreateOperationCompletionDeadline.Time,
			"message", message)
		operationalState.ProvisioningState = coreapi.ProvisioningStateFailed
		persistErr = &coreapi.CloudErrorBody{
			Code:    coreapi.CloudErrorCodeForMessage(message, coreapi.CloudErrorCodeInternalServerError),
			Message: message,
		}
	}

	logger.Info("updating status")
	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, operationalState.ProvisioningState, persistErr, operationbase.PostAsyncNotificationFn(c.notificationClient))
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *operationClusterCreate) determineOperationState(ctx context.Context, operation *coreapi.Operation, cluster *coreapi.HCPOpenShiftCluster) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	errs := []error{}
	operationStates := []*operationbase.OperationState{}

	if currState, err := c.hostedClusterOperationStatus(ctx, operation); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, currState.WithSource("hypershiftHostedCluster"))
	}
	if currState, err := c.clusterOperationStatus(ctx, operation); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, currState.WithSource("cosmosCluster"))
	}
	if currState, err := c.clusterServiceCreateOperationState(ctx, operation, cluster); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, currState.WithSource("clusterServiceClusterStatus"))
	}
	if currState, err := c.servingCABundleOperationStatus(ctx, operation); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, currState.WithSource("servingCABundle"))
	}
	if currState, err := c.roleAssignmentsOperationStatus(ctx, operation); err != nil {
		errs = append(errs, utils.TrackError(err))
	} else {
		operationStates = append(operationStates, currState.WithSource("roleAssignments"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	// cheap and easy backup check for potential accidents in future code.
	if len(operationStates) == 0 {
		return nil, errors.New("no operation states")
	}
	slices.SortStableFunc(operationStates, operationbase.CompareOperationState)
	if operationStates[0] == nil {
		return nil, errors.New("nil operation state")
	}
	logger.Info("determined cluster create operation status", "operationStates", operationStates)

	picked, err := operationbase.PickWorstOperationState(operationStates)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("picked cluster create operation status", "provisioningState", picked.ProvisioningState, "message", picked.Message)
	return picked, nil
}

func (c *operationClusterCreate) clusterServiceCreateOperationState(ctx context.Context, operation *coreapi.Operation, cluster *coreapi.HCPOpenShiftCluster) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	// The Cluster Service resource is created asynchronously; until its ID is
	// populated there is nothing to query, so report the operation as still
	// provisioning rather than dereferencing a nil ClusterServiceID.
	if cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "cluster service has not been successfully created"), nil
	}

	clusterServiceID := *cluster.ServiceProviderProperties.ClusterServiceID

	clusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, clusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	newOperationStatus, opError, err := operationbase.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus, clusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	logger.Info("new status via cluster-service", "newStatus", newOperationStatus, "newOperationError", opError)
	msg := ""
	if opError != nil {
		msg = opError.Message
	}
	return operationbase.NewOperationState(newOperationStatus, msg), nil
}

func (c *operationClusterCreate) clusterOperationStatus(ctx context.Context, operation *coreapi.Operation) (*operationbase.OperationState, error) {
	cluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		// if the cache doesn't have the cosmos cluster yet, we'll eventually recheck when we resync. Currently 10s for
		// active operations.  No need to fail and trigger an extra check.
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "cluster state not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	if len(cluster.ServiceProviderProperties.API.URL) == 0 {
		message := ".api.url is empty"
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

// minVersionsWithValidSuccessCondition maps from <major>.<micro> to the first z-stream version that includes the fix for
// control plane validation success.
var minVersionsWithValidSuccessCondition = map[string]semver.Version{
	"4.20": metadataapi.Must(semver.Parse("4.20.15")),
	"4.21": metadataapi.Must(semver.Parse("4.21.1")),
	"4.22": metadataapi.Must(semver.Parse("4.22.0")),
}

func (c *operationClusterCreate) hostedClusterOperationStatus(ctx context.Context, operation *coreapi.Operation) (*operationbase.OperationState, error) {
	logger := utils.LoggerFromContext(ctx)

	// Pull the HostedCluster directly from the per-cluster ReadDesire via
	// the union lister. The union lister hides per-MC routing so callers
	// don't need to know which management cluster the HostedCluster is on.
	readDesire, err := c.readDesireLister.GetForCluster(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name, kubeapplierhelpers.ReadDesireNameReadonlyHostedCluster)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "hosted cluster state not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if !meta.IsStatusConditionTrue(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful) {
		message := "ReadDesire has not yet successfully observed the target"
		if successfulCondition := meta.FindStatusCondition(readDesire.Status.Conditions, kubeapplierapi.ConditionTypeSuccessful); successfulCondition != nil {
			message = fmt.Sprintf("ReadDesire is not successful: %s: %s", successfulCondition.Reason, successfulCondition.Message)
		}
		logger.Info("ReadDesire is not successful", "readDesire.Status.Conditions", readDesire.Status.Conditions)
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, message), nil
	}

	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ReadDesire has no kube content"), nil
	}

	hostedCluster := &v1beta1.HostedCluster{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, hostedCluster); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to decode HostedCluster: %w", err))
	}

	anyVersionInstalled := false
	anyVersionWithValidSuccessCondition := false
	for _, historicalVersion := range hostedCluster.Status.ControlPlaneVersion.History {
		if historicalVersion.State == configv1.CompletedUpdate {
			anyVersionInstalled = true
		}

		currVersion, err := semver.Parse(historicalVersion.Version)
		if err != nil {
			logger.Info("failed to parse version", "version", historicalVersion.Version, "error", err)
			continue
		}
		currMajorMinor := fmt.Sprintf("%d.%d", currVersion.Major, currVersion.Minor)
		if minVersion, ok := minVersionsWithValidSuccessCondition[currMajorMinor]; ok && currVersion.LT(minVersion) {
			// if the current version is less than the min version where this takes effect.
			continue
		}
		anyVersionWithValidSuccessCondition = true
	}

	if anyVersionWithValidSuccessCondition {
		// can only check this when the success condition works, because this is unreliable otherwise
		if !meta.IsStatusConditionTrue(hostedCluster.Status.Conditions, string(v1beta1.HostedClusterAvailable)) {
			message := "hosted cluster is not available, condition missing"
			if availableCondition := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(v1beta1.HostedClusterAvailable)); availableCondition != nil {
				message = fmt.Sprintf("hosted cluster is not available: %s: %s", availableCondition.Reason, availableCondition.Message)
			}
			logger.Info("hosted cluster is not available", "hostedCluster.Status.Conditions", hostedCluster.Status.Conditions)
			return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, withDegradedSuffix(message, hostedCluster)), nil
		}

		if !anyVersionInstalled {
			// can only check this when the success condition works, because this is unreliable otherwise
			logger.Info("hosted cluster has not completed installing", "hostedCluster.Status.ControlPlaneVersion.History", hostedCluster.Status.ControlPlaneVersion.History)
			return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, withDegradedSuffix("hosted cluster has not completed installing", hostedCluster)), nil
		}
	}

	if len(hostedCluster.Status.ControlPlaneEndpoint.Host) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, withDegradedSuffix("hosted cluster has no control plane endpoint host", hostedCluster)), nil
	}
	if hostedCluster.Status.ControlPlaneEndpoint.Port == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, withDegradedSuffix("hosted cluster has no control plane endpoint port", hostedCluster)), nil
	}

	// if we got here,
	// 1. the hosted cluster is available via condition
	// 2. the hosted cluster has successfully installed at least one version
	// 3. the hosted cluster has a control plane endpoint host and port
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

func (c *operationClusterCreate) servingCABundleOperationStatus(ctx context.Context, operation *coreapi.Operation) (*operationbase.OperationState, error) {
	// The control-plane serving CA is mirrored into the service cluster (and
	// thus ServiceProviderCluster.Status.ServingCABundle is populated) for every
	// cluster that has a control-plane namespace, regardless of OpenShift
	// version. The create operation blocks until that bundle has been populated.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ServiceProviderCluster not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if len(serviceProviderCluster.Status.ServingCABundle) == 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ServingCABundle not yet populated"), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

// roleAssignmentsOperationStatus blocks cluster creation until the managed
// resource group scoped role assignments that Cluster Service creates for the
// cluster's control-plane and data-plane operator managed identities have all been
// observed as present. The ObserveRoleAssignments controller reflects them onto
// ServiceProviderCluster.Status.AzureResources.RoleAssignments; creation is
// considered complete for this source once at least one role assignment is confirmed
// and none remain pending.
func (c *operationClusterCreate) roleAssignmentsOperationStatus(ctx context.Context, operation *coreapi.Operation) (*operationbase.OperationState, error) {
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "ServiceProviderCluster not cached yet"), nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	roleAssignments := serviceProviderCluster.Status.AzureResources.RoleAssignments
	if len(roleAssignments.AzureResources) == 0 || len(roleAssignments.PendingAzureResources) != 0 {
		return operationbase.NewOperationState(coreapi.ProvisioningStateProvisioning, "role assignments not yet confirmed"), nil
	}
	return operationbase.NewOperationState(coreapi.ProvisioningStateSucceeded, ""), nil
}

func (c *operationClusterCreate) shouldReconcileOperationAndResourceStatus(cluster *coreapi.HCPOpenShiftCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil
}

// withDegradedSuffix appends the HostedCluster Degraded condition's reason and
// message to the given non-success operation message when the condition is True,
// so downstream consumers see the underlying degradation alongside the immediate
// provisioning blocker.
func withDegradedSuffix(message string, hostedCluster *v1beta1.HostedCluster) string {
	degraded := meta.FindStatusCondition(hostedCluster.Status.Conditions, string(v1beta1.HostedClusterDegraded))
	if degraded == nil || degraded.Status != metav1.ConditionTrue {
		return message
	}
	return fmt.Sprintf("%s; hosted cluster degraded: %s: %s", message, degraded.Reason, degraded.Message)
}
