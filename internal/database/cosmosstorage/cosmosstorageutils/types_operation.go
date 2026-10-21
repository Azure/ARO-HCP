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

package cosmosstorageutils

import (
	"path"
	"time"

	"github.com/google/uuid"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type OperationRequest = coreapi.OperationRequest

const (
	OperationRequestCreate OperationRequest = "Create"
	OperationRequestUpdate OperationRequest = "Update"
	OperationRequestDelete OperationRequest = "Delete"

	// These are for POST actions on resources.
	OperationRequestSystemAdminCredentialRequest    OperationRequest = "RequestCredential"
	OperationRequestSystemAdminCredentialRevocation OperationRequest = "RevokeCredentials"
)

func NewOperation(
	request OperationRequest,
	externalID *azcorearm.ResourceID,
	internalID ocm.InternalID,
	location, tenantID, clientID, notificationURI string,
	correlationData *coreapi.CorrelationData,
) *coreapi.Operation {

	now := time.Now().UTC()

	operation := &coreapi.Operation{
		Request:            request,
		ExternalID:         externalID,
		InternalID:         internalID,
		TenantID:           tenantID,
		ClientID:           clientID,
		NotificationURI:    notificationURI,
		StartTime:          now,
		LastTransitionTime: now,
		Status:             coreapi.ProvisioningStateAccepted,
	}
	operation.OperationID = metadataapi.Must(azcorearm.ParseResourceID(path.Join("/",
		"subscriptions", operation.ExternalID.SubscriptionID,
		"providers", coreapi.ProviderNamespace,
		"locations", location,
		coreapi.OperationStatusResourceTypeName,
		uuid.New().String())))

	// this ID does not include the location because doing so changes the resulting azcorearm.ParseResourceID().ResourceType to be
	// Microsoft.RedHatOpenShift/locations/hcpOperationStatuses.  This type is not compatible with the current cosmos storage and
	// nests in a way that doesn't match other types. Since our operationID.Name is a UID, this is still a globally unique
	// resourceID.
	operation.ResourceID = metadataapi.Must(azcorearm.ParseResourceID(path.Join("/",
		"subscriptions", operation.ExternalID.SubscriptionID,
		"providers", coreapi.ProviderNamespace,
		coreapi.OperationStatusResourceTypeName, operation.OperationID.Name,
	)))
	operation.SetPartitionKey(operation.ExternalID.SubscriptionID)

	if correlationData != nil {
		operation.ClientRequestID = correlationData.ClientRequestID
		operation.CorrelationRequestID = correlationData.CorrelationRequestID
	}

	// When deleting, set Status directly to ProvisioningStateDeleting
	// so any further deletion requests are rejected with 409 Conflict.
	if request == OperationRequestDelete {
		operation.Status = coreapi.ProvisioningStateDeleting
	}

	return operation
}

// ToStatus converts an OperationDocument to the ARM operation status format.
func ToStatus(doc *coreapi.Operation) *coreapi.OperationStatus {
	operation := &coreapi.OperationStatus{
		ID:        doc.OperationID,
		Name:      doc.OperationID.Name,
		Status:    doc.Status,
		StartTime: &doc.StartTime,
		Error:     doc.Error,
	}

	if doc.Status.IsTerminal() {
		operation.EndTime = &doc.LastTransitionTime
	}

	return operation
}
