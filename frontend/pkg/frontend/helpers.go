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
	"net/http"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func addOperationResponseHeaders(writer http.ResponseWriter, request *http.Request, notificationURI string, operationID *azcorearm.ResourceID) database.DBTransactionCallback {
	return func(result database.DBTransactionResult) {
		// If ARM passed a notification URI, acknowledge it.
		if len(notificationURI) > 0 {
			writer.Header().Set(arm.HeaderNameAsyncNotification, "Enabled")
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
	cosmosClient database.DBClient,
	operationRequest database.OperationRequest,
	resourceID *azcorearm.ResourceID,
	provisioningState arm.ProvisioningState,
) error {

	switch operationRequest {
	case database.OperationRequestCreate:
		// Resource must already exist for there to be a conflict.
	case database.OperationRequestDelete:
		if provisioningState == arm.ProvisioningStateDeleting {
			return arm.NewConflictError(
				resourceID,
				"Resource is already deleting")
		}
	case database.OperationRequestUpdate:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return arm.NewConflictError(
				resourceID,
				"Cannot update resource while resource is %q",
				strings.ToLower(string(provisioningState)))
		}
	case database.OperationRequestRequestCredential:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return arm.NewConflictError(
				resourceID,
				"Cannot request credential while resource is %q",
				strings.ToLower(string(provisioningState)))
		}
	case database.OperationRequestRevokeCredentials:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return arm.NewConflictError(
				resourceID,
				"Cannot revoke credentials while resource is %q",
				strings.ToLower(string(provisioningState)))
		}
	}

	parent := resourceID.Parent

	// ResourceType casing is preserved for parents in the same namespace.
	// TODO if I understand this correctly, this is ONLY the Cluster itself, in which case these calls could change.
	for parent.ResourceType.Namespace == resourceID.ResourceType.Namespace {
		_, parentDoc, err := cosmosClient.GetResourceDoc(ctx, parent)
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
		if parentDoc.ProvisioningState == arm.ProvisioningStateProvisioning {
			return arm.NewConflictError(
				resourceID,
				"Cannot %s resource while parent resource is provisioning",
				strings.ToLower(string(operationRequest)))
		}

		if parentDoc.ProvisioningState == arm.ProvisioningStateDeleting {
			return arm.NewConflictError(
				resourceID,
				"Cannot %s resource while parent resource is deleting",
				strings.ToLower(string(operationRequest)))
		}

		parent = parent.Parent
	}

	return nil
}

// CheckForProvisioningStateConflict returns a "409 Conflict" error response if the
// provisioning state of the resource is non-terminal, or any of its parent resources
// within the same provider namespace are in a "Provisioning" or "Deleting" state.
func (f *Frontend) CheckForProvisioningStateConflict(ctx context.Context, operationRequest database.OperationRequest, doc *database.ResourceDocument) error {
	return checkForProvisioningStateConflict(ctx, f.dbClient, operationRequest, doc.ResourceID, doc.ProvisioningState)
}

func (f *Frontend) DeleteAllResources(ctx context.Context, writer http.ResponseWriter, request *http.Request, subscriptionID string) error {
	transaction := f.dbClient.NewTransaction(subscriptionID)

	clusterIterator, err := f.dbClient.HCPClusters(subscriptionID, "").List(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, cluster := range clusterIterator.Items(ctx) {
		if err := f.addDeleteClusterToTransaction(ctx, writer, request, transaction, cluster); err != nil {
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
