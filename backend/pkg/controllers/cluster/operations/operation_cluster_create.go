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
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
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
	statusCalculators                     operationbase.OperationStatusCalculators[struct{}]
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

const OperationClusterCreateControllerName = "OperationClusterCreate"

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

	// The registry is fixed in code; invalid source registrations are startup errors.
	syncer.statusCalculators = metadataapi.Must(operationbase.NewOperationStatusCalculators[struct{}](
		&clusterCreateValidationCheck{clock: clock, serviceProviderClusterLister: serviceProviderClusterLister},
		&clusterCreateHostedClusterCheck{readDesireLister: readDesireLister},
		&clusterCreateResourceCheck{clusterLister: clusterLister},
		&clusterCreateClusterServiceCheck{clusterLister: clusterLister, clusterServiceClient: clusterServiceClient},
		&clusterCreatePlacementCheck{clusterLister: clusterLister, clock: clock, serviceProviderClusterLister: serviceProviderClusterLister},
		&clusterCreateServingCACheck{serviceProviderClusterLister: serviceProviderClusterLister},
		&clusterCreateRoleAssignmentsCheck{serviceProviderClusterLister: serviceProviderClusterLister},
	))

	controller := controllerutils.NewGenericOperationController(
		OperationClusterCreateControllerName,
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
	operationalState, err := c.statusCalculators.CalculateOperationStatus(ctx, operation, struct{}{})
	if err != nil {
		return utils.TrackError(err)
	}

	persistErr := operationalState.Error
	if operationalState.ProvisioningState == coreapi.ProvisioningStateFailed && persistErr == nil {
		persistErr = &coreapi.CloudErrorBody{
			Code:    operationalState.CloudErrorCode,
			Message: operationalState.Message,
		}
	}

	if !operationalState.ProvisioningState.IsTerminal() &&
		cluster.ServiceProviderProperties.CreateOperationCompletionDeadline != nil &&
		c.clock.Now().After(cluster.ServiceProviderProperties.CreateOperationCompletionDeadline.Time) {

		message := operationbase.DeadlineExceededMessage(
			"cluster creation did not complete before the deadline",
			operationalState.Message,
		)
		logger.Info("create operation deadline exceeded, marking as failed",
			"deadline", cluster.ServiceProviderProperties.CreateOperationCompletionDeadline.Time,
			"message", message)
		operationalState.ProvisioningState = coreapi.ProvisioningStateFailed
		code := operationalState.CloudErrorCode
		if code == coreapi.CloudErrorCodeInternalServerError {
			code = coreapi.CloudErrorCodeDeadlineExceeded
		}
		persistErr = &coreapi.CloudErrorBody{
			Code:    code,
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

// minVersionsWithValidSuccessCondition maps from <major>.<micro> to the first z-stream version that includes the fix for
// control plane validation success.
var minVersionsWithValidSuccessCondition = map[string]semver.Version{
	"4.20": metadataapi.Must(semver.Parse("4.20.15")),
	"4.21": metadataapi.Must(semver.Parse("4.21.1")),
	"4.22": metadataapi.Must(semver.Parse("4.22.0")),
}

func (c *operationClusterCreate) shouldReconcileOperationAndResourceStatus(cluster *coreapi.Cluster) bool {
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

// describeVersionHistory produces a human-readable message explaining why no
// version in the HostedCluster's control plane version history has reached
// CompletedUpdate. It lists each version entry with its current state so that
// operators and agents can understand what the control plane is doing without
// reading backend source code.
func describeVersionHistory(history []v1beta1.ControlPlaneUpdateHistory) string {
	if len(history) == 0 {
		return "hosted cluster has no version history entries"
	}
	descriptions := make([]string, 0, len(history))
	for _, entry := range history {
		if entry.State == configv1.CompletedUpdate {
			continue // only describe versions still in flight
		}
		desc := fmt.Sprintf("version %s is %s (want %s)", entry.Version, entry.State, configv1.CompletedUpdate)
		if !entry.StartedTime.IsZero() {
			elapsed := time.Since(entry.StartedTime.Time).Truncate(time.Second)
			if elapsed < 0 {
				elapsed = 0
			}
			desc += fmt.Sprintf(", started %s ago", elapsed)
		}
		descriptions = append(descriptions, desc)
	}
	if len(descriptions) == 0 {
		return "hosted cluster control plane version history has no in-flight entries"
	}
	return fmt.Sprintf("hosted cluster control plane version not yet completed: %s", strings.Join(descriptions, "; "))
}
