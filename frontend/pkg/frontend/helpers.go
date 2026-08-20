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

package frontend

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func addOperationResponseHeaders(writer http.ResponseWriter, request *http.Request, notificationURI string, operationID *azcorearm.ResourceID) cosmosstorageutils.DBTransactionCallback {
	return func(result cosmosstorageutils.DBTransactionResult) {
		// If ARM passed a notification URI, acknowledge it.
		if len(notificationURI) > 0 {
			writer.Header().Set(coreapi.HeaderNameAsyncNotification, "Enabled")
		}

		// Add callback header(s) based on the request method.
		switch request.Method {
		case http.MethodDelete, http.MethodPatch, http.MethodPost:
			AddLocationHeader(writer, request, operationID)
			fallthrough
		case http.MethodPut:
			AddAsyncOperationHeader(writer, request, operationID)
		}
	}
}

// checkForProvisioningStateConflict returns a "409 Conflict" error response if the
// provisioning state of the resource is non-terminal, or any of its parent resources
// within the same provider namespace are in a "Provisioning" or "Deleting" state.
// TODO we will collapse onto this function entirely once we complete the migration.  Creating a separate method now to avoid having to have a big bang
func checkForProvisioningStateConflict(
	ctx context.Context,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	operationRequest cosmosstorageutils.OperationRequest,
	resourceID *azcorearm.ResourceID,
	provisioningState coreapi.ProvisioningState,
) error {

	switch operationRequest {
	case cosmosstorageutils.OperationRequestCreate:
		// Resource must already exist for there to be a conflict.
	case cosmosstorageutils.OperationRequestDelete:
		if provisioningState == coreapi.ProvisioningStateDeleting {
			return coreapi.NewConflictError(
				resourceID,
				"Resource is already deleting")
		}
	case cosmosstorageutils.OperationRequestUpdate:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return coreapi.NewConflictError(
				resourceID,
				"Cannot update resource while resource is %q",
				strings.ToLower(string(provisioningState)))
		}
	case cosmosstorageutils.OperationRequestSystemAdminCredentialRequest:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return coreapi.NewConflictError(
				resourceID,
				"Cannot request credential while resource is %q",
				strings.ToLower(string(provisioningState)))
		}
	case cosmosstorageutils.OperationRequestSystemAdminCredentialRevocation:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return coreapi.NewConflictError(
				resourceID,
				"Cannot revoke credentials while resource is %q",
				strings.ToLower(string(provisioningState)))
		}
	}

	// For nested resource types, check the provisioning state of the parent cluster.
	if strings.EqualFold(resourceID.ResourceType.String(), coreapi.NodePoolResourceType.String()) ||
		strings.EqualFold(resourceID.ResourceType.String(), coreapi.ExternalAuthResourceType.String()) {

		cluster, err := resourcesDBClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Parent.Name)
		if err != nil {
			return utils.TrackError(err)
		}

		// XXX There is still a small opportunity for nested resource requests to get
		//     through while the parent resource is in provisioning state "Accepted",
		//     which precedes "Provisioning". The problem is "Accepted" also precedes
		//     "Updating", which should NOT be blocked.
		//
		//     Cluster Service will catch and correctly reject such requests, so I'm
		//     leaving this gap open until Cluster Service is out of the picture and
		//     the RP has more direct control over resource provisioning.
		if cluster.ServiceProviderProperties.ProvisioningState == coreapi.ProvisioningStateProvisioning {
			return coreapi.NewConflictError(
				resourceID,
				"Cannot %s resource while parent resource is provisioning",
				strings.ToLower(string(operationRequest)))
		}

		if cluster.ServiceProviderProperties.ProvisioningState == coreapi.ProvisioningStateDeleting {
			return coreapi.NewConflictError(
				resourceID,
				"Cannot %s resource while parent resource is deleting",
				strings.ToLower(string(operationRequest)))
		}
	}

	return nil
}

func (f *Frontend) DeleteAllResourcesInSubscription(ctx context.Context, subscriptionID string) error {
	transaction := f.resourcesDBClient.NewTransaction(subscriptionID, "delete_subscription_resources")

	clusterIterator, err := f.resourcesDBClient.HCPClusters(subscriptionID, "").List(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, cluster := range clusterIterator.Items(ctx) {
		if cluster.ServiceProviderProperties.ProvisioningState == coreapi.ProvisioningStateDeleting {
			// don't try to delete already deleting clusters.  If we call the delete on them, the call will fail
			// on various problems from cluster-service. We trust the existing delete is doing good things.
			continue
		}
		if err := f.addDeleteClusterToTransaction(ctx, nil, nil, transaction, cluster); err != nil {
			return utils.TrackError(err)
		}
	}
	if err = clusterIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func nameResourceIDMismatch(resourceID *azcorearm.ResourceID, name string) error {
	return coreapi.CloudErrorFromFieldErrors(field.ErrorList{
		field.Invalid(field.NewPath("name"), name, fmt.Sprintf("name must match resourceID path: %v", resourceID)),
	})
}
