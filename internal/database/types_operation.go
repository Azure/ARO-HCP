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
	"fmt"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

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

var _ ResourceProperties = &Operation{}

func (o *Operation) ValidateResourceType() error {
	switch o.ResourceType {
	case api.OperationStatusResourceType.String():
	default:
		return fmt.Errorf("invalid resource type: %s", o.ResourceType)
	}
	return nil
}

func (o *Operation) GetTypedDocument() *TypedDocument {
	return &o.TypedDocument
}

func (o *Operation) SetResourceID(_ *azcorearm.ResourceID) {
	// do nothing.  There is no real resource ID to set and we don't need to worry about conforming to ARM casing rules.
	// TODO, consider whether this should be done in the frontend and not in storage (likely)
}

type OperationDocument = api.Operation

func NewOperationDocument(request OperationRequest, externalID *azcorearm.ResourceID, internalID ocm.InternalID, correlationData *arm.CorrelationData) *OperationDocument {
	now := time.Now().UTC()

	doc := &OperationDocument{
		Request:            request,
		ExternalID:         externalID,
		InternalID:         internalID,
		StartTime:          now,
		LastTransitionTime: now,
		Status:             arm.ProvisioningStateAccepted,
	}

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

// OperationDocumentPatchOperations represents a patch request for an OperationDocument.
type OperationDocumentPatchOperations struct {
	azcosmos.PatchOperations
}

const (
	OperationDocumentJSONPathTenantID           = typedDocumentJSONPathProperties + "/tenantId"
	OperationDocumentJSONPathClientID           = typedDocumentJSONPathProperties + "/clientId"
	OperationDocumentJSONPathRequest            = typedDocumentJSONPathProperties + "/request"
	OperationDocumentJSONPathExternalID         = typedDocumentJSONPathProperties + "/externalId"
	OperationDocumentJSONPathInternalID         = typedDocumentJSONPathProperties + "/internalId"
	OperationDocumentJSONPathOperationID        = typedDocumentJSONPathProperties + "/operationId"
	OperationDocumentJSONPathNotificationURI    = typedDocumentJSONPathProperties + "/notificationUri"
	OperationDocumentJSONPathStartTime          = typedDocumentJSONPathProperties + "/startTime"
	OperationDocumentJSONPathLastTransitionTime = typedDocumentJSONPathProperties + "/lastTransitionTime"
	OperationDocumentJSONPathStatus             = typedDocumentJSONPathProperties + "/status"
	OperationDocumentJSONPathError              = typedDocumentJSONPathProperties + "/error"
)

// SetTenantID appends a set operation for the TenantID field.
func (p *OperationDocumentPatchOperations) SetTenantID(tenantID string) {
	p.AppendSet(OperationDocumentJSONPathTenantID, tenantID)
}

// SetClientID appends a set operation for the ClientID field.
func (p *OperationDocumentPatchOperations) SetClientID(clientID string) {
	p.AppendSet(OperationDocumentJSONPathClientID, clientID)
}

// SetOperationID appends a set or remove operation for the OperationID field,
// depending on whether operationID is nil.
//
// Be careful when appending a remove patch operation as it is NOT idempotent.
// If the field to remove is not present in the Cosmos DB document, the entire
// patch request will fail with a "400 Bad Request" status code.
func (p *OperationDocumentPatchOperations) SetOperationID(operationID *azcorearm.ResourceID) {
	if operationID != nil {
		p.AppendSet(OperationDocumentJSONPathOperationID, operationID)
	} else {
		p.AppendRemove(OperationDocumentJSONPathOperationID)
	}
}

// SetNotificationURI appends a set or remove operation for the NotificationURI field,
// depending on whether notificationURI is nil.
//
// Be careful when appending a remove patch operation as it is NOT idempotent.
// If the field to remove is not present in the Cosmos DB document, the entire
// patch request will fail with a "400 Bad Request" status code.
func (p *OperationDocumentPatchOperations) SetNotificationURI(notificationURI *string) {
	if notificationURI != nil {
		p.AppendSet(OperationDocumentJSONPathNotificationURI, *notificationURI)
	} else {
		p.AppendRemove(OperationDocumentJSONPathNotificationURI)
	}
}

// SetLastTransitionTime appends a set operation for the LastTransitionTime field.
func (p *OperationDocumentPatchOperations) SetLastTransitionTime(lastTransitionTime time.Time) {
	p.AppendSet(OperationDocumentJSONPathLastTransitionTime, lastTransitionTime)
}

// SetStatus appends a set operation for the Status field.
func (p *OperationDocumentPatchOperations) SetStatus(status arm.ProvisioningState) {
	p.AppendSet(OperationDocumentJSONPathStatus, status)
}

// SetError appends a set or remove operation for the Error field,
// depending on whether err is nil.
//
// Be careful when appending a remove patch operation as it is NOT idempotent.
// If the field to remove is not present in the Cosmos DB document, the entire
// patch request will fail with a "400 Bad Request" status code.
func (p *OperationDocumentPatchOperations) SetError(err *arm.CloudErrorBody) {
	if err != nil {
		p.AppendSet(OperationDocumentJSONPathError, err)
	} else {
		p.AppendRemove(OperationDocumentJSONPathError)
	}
}
