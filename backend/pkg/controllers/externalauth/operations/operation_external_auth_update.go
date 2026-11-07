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

type operationExternalAuthUpdate struct {
	clock                  utilsclock.PassiveClock
	resourcesDBClient      corecosmosstorage.ResourcesDBClient
	clusterServiceClient   ocm.ClusterServiceClientSpec
	externalAuthLister     corelisters.ExternalAuthLister
	readDesireLister       kubeapplierlisters.ReadDesireLister
	activeOperationsLister corelisters.ActiveOperationLister
	notificationClient     *http.Client
	statusCalculators      operationbase.OperationStatusCalculators[*externalAuthUpdateOperationStatusInput]
}

const OperationExternalAuthUpdateControllerName = "OperationExternalAuthUpdate"

// NewOperationExternalAuthUpdateController returns a new Controller instance that
// follows an asynchronous external auth update operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters/externalAuths
//	     Request: Update
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationExternalAuthUpdateController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, externalAuthLister := backendInformers.ExternalAuths()
	_, activeOperationsLister := backendInformers.ActiveOperations()

	syncer := &operationExternalAuthUpdate{
		clock:                  clock,
		resourcesDBClient:      resourcesDBClient,
		clusterServiceClient:   clusterServiceClient,
		externalAuthLister:     externalAuthLister,
		readDesireLister:       readDesireLister,
		activeOperationsLister: activeOperationsLister,
		notificationClient:     notificationClient,
	}

	syncer.statusCalculators = metadataapi.Must(syncer.newStatusCalculators())

	controller := controllerutils.NewGenericOperationController(
		OperationExternalAuthUpdateControllerName,
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
func (c *operationExternalAuthUpdate) newStatusCalculators() (operationbase.OperationStatusCalculators[*externalAuthUpdateOperationStatusInput], error) {
	return operationbase.NewOperationStatusCalculators[*externalAuthUpdateOperationStatusInput](
		&externalAuthUpdateClusterServiceSpecCheck{externalAuthLister: c.externalAuthLister},
		&externalAuthUpdateHypershiftHostedClusterCheck{externalAuthLister: c.externalAuthLister, readDesireLister: c.readDesireLister},
	)
}

func (c *operationExternalAuthUpdate) ShouldProcess(ctx context.Context, operation *coreapi.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != cosmosstorageutils.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), coreapi.ExternalAuthResourceType.String()) {
		return false
	}
	return true
}

func (c *operationExternalAuthUpdate) shouldReconcileOperationAndResourceStatus(ea *coreapi.ExternalAuth) bool {
	return ea.ServiceProviderProperties.DeletionTimestamp == nil
}

func (c *operationExternalAuthUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	existingExternalAuth, err := c.externalAuthLister.Get(ctx, operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName, operation.ExternalID.Parent.Name, operation.ExternalID.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("external auth not found in cache, waiting")
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get external auth: %w", err))
	}

	if operation.ResourceID.Name != existingExternalAuth.ServiceProviderProperties.ActiveOperationID {
		logger.Info("external auth active operation id mismatch, returning early", "synchronizedActiveOperationID", operation.ResourceID.Name, "externalAuthActiveOperationID", existingExternalAuth.ServiceProviderProperties.ActiveOperationID)
		return nil
	}

	if !c.shouldReconcileOperationAndResourceStatus(existingExternalAuth) {
		return nil // no work to do
	}

	input := &externalAuthUpdateOperationStatusInput{
		ClusterServiceExternalAuthOnceGetter: newClusterServiceExternalAuthOnceGetter(c.clusterServiceClient),
	}
	operationalState, observationErr := c.statusCalculators.CalculateOperationStatus(ctx, operation, input)
	operationalState, err = operationbase.ApplyOperationDeadline(operationalState, observationErr, existingExternalAuth.ServiceProviderProperties.UpdateOperationCompletionDeadline, c.clock.Now(), "External auth update did not complete within the allowed time.")
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

type externalAuthUpdateOperationStatusInput struct {
	ClusterServiceExternalAuthOnceGetter func(ctx context.Context, clusterServiceID *metadataapi.InternalID) (*arohcpv1alpha1.ExternalAuth, error)
}
