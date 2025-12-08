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
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type OperationRequest string

const (
	OperationRequestCreate OperationRequest = "Create"
	OperationRequestUpdate OperationRequest = "Update"
	OperationRequestDelete OperationRequest = "Delete"

	// These are for POST actions on resources.
	OperationRequestRequestCredential OperationRequest = "RequestCredential"
	OperationRequestRevokeCredentials OperationRequest = "RevokeCredentials"
)

// OperationResourceType is an artificial resource type for OperationDocuments
// in Cosmos DB. It omits the location segment from actual operation endpoints.
var OperationResourceType = azcorearm.NewResourceType(api.ProviderNamespace, api.OperationStatusResourceTypeName)

type OperationStatus struct {
	TypedDocument `json:",inline"`

	OperationProperties OperationDocument `json:"properties"`
}

// OperationDocument tracks an asynchronous operation.
type OperationDocument struct {
	// ResourceID must be serialized exactly here for the generic CRUD to work.
	// ResourceID here is NOT an ARM resourceID, it just parses like and one and is guarantee unique
	ResourceID *azcorearm.ResourceID `json:"resourceId"`

	// TenantID is the tenant ID of the client that requested the operation
	TenantID string `json:"tenantId,omitempty"`
	// ClientID is the object ID of the client that requested the operation
	ClientID string `json:"clientId,omitempty"`
	// Request is the type of asynchronous operation requested
	Request OperationRequest `json:"request,omitempty"`
	// ExternalID is the Azure resource ID of the cluster or node pool
	ExternalID *azcorearm.ResourceID `json:"externalId,omitempty"`
	// InternalID is the Cluster Service resource identifier in the form of a URL path
	InternalID ocm.InternalID `json:"internalId,omitempty"`
	// OperationID is the Azure resource ID of the operation status (may be nil if the
	// operation was implicit, such as deleting a child resource along with the parent)
	OperationID *azcorearm.ResourceID `json:"operationId,omitempty"`
	// ClientRequestID is provided by the "x-ms-client-request-id" request header
	ClientRequestID string `json:"clientRequestId,omitempty"`
	// CorrelationRequstID is provided by the "x-ms-correlation-request-id" request header
	CorrelationRequestID string `json:"correlationRequestId,omitempty"`
	// NotificationURI is provided by the Azure-AsyncNotificationUri header if the
	// Async Operation Callbacks ARM feature is enabled
	NotificationURI string `json:"notificationUri,omitempty"`

	// StartTime marks the start of the operation
	StartTime time.Time `json:"startTime,omitempty"`
	// LastTransitionTime marks the most recent state change
	LastTransitionTime time.Time `json:"lastTransitionTime,omitempty"`
	// Status is the current operation status, using the same set of values
	// as the resource's provisioning state
	Status arm.ProvisioningState `json:"status,omitempty"`
	// Error is an OData error, present when Status is "Failed" or "Canceled"
	Error *arm.CloudErrorBody `json:"error,omitempty"`
}

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

// GetValidTypes returns the valid resource types for an OperationDocument.
func (doc OperationDocument) GetValidTypes() []string {
	return []string{OperationResourceType.String()}
}

// ToStatus converts an OperationDocument to the ARM operation status format.
func (doc *OperationDocument) ToStatus() *arm.Operation {
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

// UpdateStatus conditionally updates the document if the status given differs
// from the status already present. If so, it sets the Status and Error fields
// to the values given, updates the LastTransitionTime, and returns true. This
// is intended to be used with DBClient.UpdateOperationDoc.
func (doc *OperationDocument) UpdateStatus(status arm.ProvisioningState, err *arm.CloudErrorBody) bool {
	if doc.Status != status {
		doc.LastTransitionTime = time.Now().UTC()
		doc.Status = status
		doc.Error = err
		return true
	}
	return false
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
