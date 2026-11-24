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

	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	operationbase "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterUpdate struct {
	statusCalculators               operationbase.OperationStatusCalculators[clusterUpdateOperationStatusInput]
	clock                           utilsclock.PassiveClock
	resourcesDBClient               corecosmosstorage.ResourcesDBClient
	clusterServiceClient            ocm.ClusterServiceClientSpec
	clusterLister                   corelisters.ClusterLister
	serviceProviderClusterLister    corelisters.ServiceProviderClusterLister
	readDesireLister                kubeapplierlisters.ReadDesireLister
	activeOperationsLister          corelisters.ActiveOperationLister
	notificationClient              *http.Client
	desiredVersionMismatchFirstSeen *lru.Cache
}

const OperationClusterUpdateControllerName = "OperationClusterUpdate"

// NewOperationClusterUpdateController returns a new Controller instance that
// follows an asynchronous cluster update operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters
//	     Request: Update
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationClusterUpdateController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, activeOperationsLister := backendInformers.ActiveOperations()

	desiredVersionMismatchFirstSeen := lru.New(100000)

	syncer := &operationClusterUpdate{
		clock:                           clock,
		resourcesDBClient:               resourcesDBClient,
		clusterServiceClient:            clusterServiceClient,
		clusterLister:                   clusterLister,
		serviceProviderClusterLister:    serviceProviderClusterLister,
		readDesireLister:                readDesireLister,
		activeOperationsLister:          activeOperationsLister,
		notificationClient:              notificationClient,
		desiredVersionMismatchFirstSeen: desiredVersionMismatchFirstSeen,
	}

	// The registry is fixed in code; invalid source registrations are startup errors.
	syncer.statusCalculators = metadataapi.Must(syncer.newStatusCalculators())

	controller := controllerutils.NewGenericOperationController(
		OperationClusterUpdateControllerName,
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

// newStatusCalculators builds the fixed registry of update-operation status
// sources from the syncer's dependencies. Production and tests share this single
// construction so the two cannot drift: a source added here is exercised by both
// the controller and every test that builds a registry.
func (c *operationClusterUpdate) newStatusCalculators() (operationbase.OperationStatusCalculators[clusterUpdateOperationStatusInput], error) {
	return operationbase.NewOperationStatusCalculators[clusterUpdateOperationStatusInput](
		&clusterUpdateValidationCheck{serviceProviderClusterLister: c.serviceProviderClusterLister, clock: c.clock},
		&clusterUpdateDesiredVersionCheck{clusterLister: c.clusterLister, serviceProviderClusterLister: c.serviceProviderClusterLister, clock: c.clock, desiredVersionMismatchFirstSeen: c.desiredVersionMismatchFirstSeen},
		&clusterUpdateClusterServiceStatusCheck{clusterLister: c.clusterLister, clusterServiceClient: c.clusterServiceClient},
		&clusterUpdateClusterServiceSpecCheck{clusterLister: c.clusterLister},
		&clusterUpdateHostedClusterCheck{clusterLister: c.clusterLister, serviceProviderClusterLister: c.serviceProviderClusterLister, readDesireLister: c.readDesireLister},
		&clusterUpdateAutoscalerCheck{serviceProviderClusterLister: c.serviceProviderClusterLister, readDesireLister: c.readDesireLister},
	)
}

func (c *operationClusterUpdate) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterUpdate) shouldReconcileOperationAndResourceStatus(cluster *coreapi.Cluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil
}

func (c *operationClusterUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.activeOperationsLister.Get(ctx, key.SubscriptionID, key.OperationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	existingCluster, err := c.clusterLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("cluster not found in cache, waiting")
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}

	if operation.ResourceID.Name != existingCluster.ServiceProviderProperties.ActiveOperationID {
		logger.Info("cluster active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.ResourceID.Name, "clusterActiveOperationID", existingCluster.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	if !c.shouldReconcileOperationAndResourceStatus(existingCluster) {
		return nil // no work to do
	}

	input := clusterUpdateOperationStatusInput{
		ClusterServiceOnceGetter: newClusterServiceClusterOnceGetter(c.clusterServiceClient),
	}

	operationalState, observationErr := c.statusCalculators.CalculateOperationStatus(ctx, operation, input)
	operationalState, err = operationbase.ApplyOperationDeadline(operationalState, observationErr, existingCluster.ServiceProviderProperties.UpdateOperationCompletionDeadline, c.clock.Now(), "cluster update did not complete before the deadline")
	if err != nil {
		return utils.TrackError(err)
	}

	var persistErr *coreapi.CloudErrorBody
	if operationalState.ProvisioningState == coreapi.ProvisioningStateFailed {
		persistErr = &coreapi.CloudErrorBody{
			Code:    operationalState.CloudErrorCode,
			Message: operationalState.Message,
		}
	}

	logger.Info("updating status")
	err = operationbase.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, operationalState.ProvisioningState, persistErr, operationbase.PostAsyncNotificationFn(c.notificationClient))
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// clusterUpdateOperationStatusInput shares one Cluster Service and provider-state
// observation across all update checks in a reconciliation.
type clusterUpdateOperationStatusInput struct {
	// ServiceProviderCluster is the service provider cluster resource from cache. It can be nil if it was not found in the cache
	// or it failed to be retrieved from the cache.
	ServiceProviderCluster *coreapi.ServiceProviderCluster

	// ClusterServiceOnceGetter returns a function that will retrieve the cluster service cluster resource from cluster service if it
	// has not been attempted before.
	// If the Cluster Service cluster was not found when retrieving it from Cluster Service, it will return nil and an error.
	// The function assumes that clusterServiceID is not nil.
	ClusterServiceOnceGetter func(ctx context.Context, clusterServiceID *metadataapi.InternalID) (*arohcpv1alpha1.Cluster, error)
}

// newClusterServiceClusterOnceGetter returns a function that will retrieve the cluster service cluster resource from cluster service if it
// has not been attempted before.
// It will return the response from Clusters Service which is the Cluster Service cluster or an error.
// If the Cluster Service cluster was not found when retrieving it from Cluster Service, it will return nil and an ocmerrors.Error with Status 404.
// The function assumes that clusterServiceID is not nil. This is intentional so this is a transparent lazy cache and callers should ensure that clusterServiceID is not nil before calling this function.
func newClusterServiceClusterOnceGetter(clusterServiceClient ocm.ClusterServiceClientSpec) func(ctx context.Context, clusterServiceID *metadataapi.InternalID) (*arohcpv1alpha1.Cluster, error) {
	var csResource *arohcpv1alpha1.Cluster
	var err error
	retrievedOnce := false
	return func(ctx context.Context, clusterServiceID *metadataapi.InternalID) (*arohcpv1alpha1.Cluster, error) {
		if retrievedOnce {
			return csResource, err
		}

		csCtx, cancel := context.WithTimeout(ctx, controllerutils.CSReadTimeout)
		defer cancel()
		csResource, err = clusterServiceClient.GetCluster(csCtx, *clusterServiceID)
		retrievedOnce = true
		return csResource, err
	}
}
