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

package api

import (
	"path"
	"strings"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
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

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Operation struct {
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
	InternalID InternalID `json:"internalId,omitempty"`
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

var _ CosmosPersistable = &Operation{}

func (o *Operation) ComputeLogicalResourceID() *azcorearm.ResourceID {
	return Must(azcorearm.ParseResourceID(
		strings.ToLower(
			path.Join(
				"/subscriptions",
				o.OperationID.SubscriptionID,
				"providers",
				OperationStatusResourceType.String(),
				o.OperationID.Name,
			))))
}

func (o *Operation) GetCosmosData() *CosmosData {
	return &CosmosData{
		ResourceID: o.ResourceID,
	}
}
