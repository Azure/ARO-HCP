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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/tools/cache"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationNodePoolDelete struct {
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

// NewOperationNodePoolDeleteController returns a new Controller instance that
// follows an asynchronous node pool deletion operation to completion and updates
// the corresponding operation document in Cosmos DB.
//
// Operation documents relevant to this controller will have the following values:
//
//	ResourceType: Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools
//	     Request: Delete
//	      Status: any non-terminal value
//
// Note that "to completion" does not imply success. An operation is considered
// complete when its status field reaches what Azure defines as a terminal value;
// any of "Succeeded", "Failed", or "Canceled". Once the operation status reaches
// a terminal value, there will be no further updates to the operation document.
func NewOperationNodePoolDeleteController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationNodePoolDelete{
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	controller := NewGenericOperationController(
		"OperationNodePoolDelete",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)

	return controller
}

func (c *operationNodePoolDelete) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()) {
		return false
	}
	return true
}

func (c *operationNodePoolDelete) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	// TODO we are thinking on leeaving operationNodePoolDelete to just wait until deletion is requested and the Cosmos
	// NodePool document is deleted by another controller that takes care of that. Then at that point this controller
	// would set the operation to completed. On the frontend side we would then stop setting CSInternalID on delete.
	// However, right now this controller does the following:
	// - It periodically checks the NodePool status from CS side and updates the operation status based on that. This
	//   includes both terminal and non-terminal states and setting to a terminal state ends the controller handling it
	// - It performs deletion of direct descendent child cosmos resources (like NodePool-scoped ManagementClusterContents and ServiceProviderNodePool)
	//   Luckily it seems to list the direct descendent child cosmos resources it doesn't need the parent cosmos resource
	//   id to exists in cosmos, only the resourceid string is needed, and we can obtain it from operation.ExternalID.
	//  With the approach mentioned above that behavior changes and it's also not possible anymore to do do the first
	//  step of checking the NodePool status from CS side and updating the operation status based on that. If we just
	//  stop doing that part we would go from an operation in Accepted state (what the frontend currently sets when
	//  it creates a NodePool delete cosmos operation) to a Succeeded state. There wouldn't be changes inbetween, the
	//  operation wouldn't change between non terminal states and if CS nodepool deletion fails the operation would remain
	//  in Accepted state. We need to decide what do we want to do about this. Regarding
	//  the second step of deleting the direct descendent child cosmos resources, we could potentially move it outside
	//  here but I think we could do it gradually and keep it at first here to reduce the amount of changes needed. Also
	//  to discuss.
	nodePoolStatus, err := c.clusterServiceClient.GetNodePoolStatus(ctx, operation.InternalID)
	var ocmGetNodePoolError *ocmerrors.Error
	if err != nil && errors.As(err, &ocmGetNodePoolError) && ocmGetNodePoolError.Status() == http.StatusNotFound {
		logger.Info("node pool was deleted")

		err = SetDeleteOperationAsCompleted(ctx, c.resourcesDBClient, operation, postAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := convertNodePoolStatus(operation, nodePoolStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = UpdateOperationStatus(ctx, c.resourcesDBClient, operation, newOperationStatus, newOperationError, postAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
