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

package database

import (
	"path"
	"time"

	"github.com/google/uuid"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type OperationRequest = api.OperationRequest

const (
	OperationRequestCreate OperationRequest = "Create"
	OperationRequestUpdate OperationRequest = "Update"
	OperationRequestDelete OperationRequest = "Delete"

	// These are for POST actions on resources.
	OperationRequestRequestCredential OperationRequest = "RequestCredential"
	OperationRequestRevokeCredentials OperationRequest = "RevokeCredentials"
)

type Operation struct {
	TypedDocument `json:",inline"`

	OperationProperties OperationDocument `json:"properties"`
}

func (o *Operation) GetTypedDocument() *TypedDocument {
	return &o.TypedDocument
}

func (o *Operation) SetResourceID(_ *azcorearm.ResourceID) {
	// do nothing.  There is no real resource ID to set and we don't need to worry about conforming to ARM casing rules.
	// TODO, consider whether this should be done in the frontend and not in storage (likely)
}

type OperationDocument = api.Operation

func NewOperationDocument(
	request OperationRequest,
	externalID *azcorearm.ResourceID,
	internalID ocm.InternalID,
	location, tenantID, clientID, notificationURI string,
	correlationData *arm.CorrelationData,
) *OperationDocument {

	now := time.Now().UTC()

	doc := &OperationDocument{
		Request:            request,
		ExternalID:         externalID,
		InternalID:         internalID,
		TenantID:           tenantID,
		ClientID:           clientID,
		NotificationURI:    notificationURI,
		StartTime:          now,
		LastTransitionTime: now,
		Status:             arm.ProvisioningStateAccepted,
	}
	doc.OperationID = api.Must(azcorearm.ParseResourceID(path.Join("/",
		"subscriptions", doc.ExternalID.SubscriptionID,
		"providers", api.ProviderNamespace,
		"locations", location,
		api.OperationStatusResourceTypeName,
		uuid.New().String())))

	// this ID does not include the location because doing so changes the resulting azcorearm.ParseResourceID().ResourceType to be
	// Microsoft.RedHatOpenShift/locations/hcpOperationStatuses.  This type is not compatible with the current cosmos storage and
	// nests in a way that doesn't match other types. Since our operationID.Name is a UID, this is still a globally unique
	// resourceID.
	doc.ResourceID = api.Must(azcorearm.ParseResourceID(path.Join("/",
		"subscriptions", doc.ExternalID.SubscriptionID,
		"providers", api.ProviderNamespace,
		api.OperationStatusResourceTypeName, doc.OperationID.Name,
	)))

	if correlationData != nil {
		doc.ClientRequestID = correlationData.ClientRequestID
		doc.CorrelationRequestID = correlationData.CorrelationRequestID
	}

	// When deleting, set Status directly to ProvisioningStateDeleting
	// so any further deletion requests are rejected with 409 Conflict.
	if request == OperationRequestDelete {
		doc.Status = arm.ProvisioningStateDeleting
	}

	return doc
}

// ToStatus converts an OperationDocument to the ARM operation status format.
func ToStatus(doc *OperationDocument) *arm.Operation {
	operation := &arm.Operation{
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
