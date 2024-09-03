package database

import (
	"time"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// ResourceDocument captures the mapping of an Azure resource ID
// to an internal resource ID (the OCM API path), as well as any
// ARM-specific metadata for the resource.
type ResourceDocument struct {
	ID           string            `json:"id,omitempty"`
	Key          *arm.ResourceID   `json:"key,omitempty"`
	PartitionKey string            `json:"partitionKey,omitempty"`
	InternalID   ocm.InternalID    `json:"internalId,omitempty"`
	SystemData   *arm.SystemData   `json:"systemData,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`

	// Values provided by Cosmos after doc creation
	ResourceID  string `json:"_rid,omitempty"`
	Self        string `json:"_self,omitempty"`
	ETag        string `json:"_etag,omitempty"`
	Attachments string `json:"_attachments,omitempty"`
	Timestamp   int    `json:"_ts,omitempty"`
}

type OperationRequest string

const (
	OperationRequestCreate OperationRequest = "Create"
	OperationRequestUpdate OperationRequest = "Update"
	OperationRequestDelete OperationRequest = "Delete"
)

// OperationDocument tracks an asynchronous operation.
type OperationDocument struct {
	// ID is the operation's unique identifier
	ID string `json:"id,omitempty"`
	// TenantID is the tenant ID of the client that requested the operation
	TenantID string `json:"tenantId,omitempty"`
	// ClientID is the object ID of the client that requested the operation
	ClientID string `json:"clientId,omitempty"`
	// Request is the type of asynchronous operation requested
	Request OperationRequest `json:"request,omitempty"`
	// ExternalID is the Azure resource ID of the cluster or node pool
	ExternalID *arm.ResourceID `json:"externalId,omitempty"`
	// InternalID is the Cluster Service resource identifier in the form of a URL path
	InternalID ocm.InternalID `json:"internalId,omitempty"`
	// OperationID is the Azure resource ID of the operation's status
	OperationID *arm.ResourceID `json:"operationId,omitempty"`
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
	Error *arm.CloudError `json:"error,omitempty"`

	// Values provided by Cosmos after doc creation
	ResourceID  string `json:"_rid,omitempty"`
	Self        string `json:"_self,omitempty"`
	ETag        string `json:"_etag,omitempty"`
	Attachments string `json:"_attachments,omitempty"`
	Timestamp   int    `json:"_ts,omitempty"`
}

// SubscriptionDocument represents an Azure Subscription document.
type SubscriptionDocument struct {
	ID           string            `json:"id,omitempty"`
	PartitionKey string            `json:"partitionKey,omitempty"`
	Subscription *arm.Subscription `json:"subscription,omitempty"`

	// Values provided by Cosmos after doc creation
	ResourceID  string `json:"_rid,omitempty"`
	Self        string `json:"_self,omitempty"`
	ETag        string `json:"_etag,omitempty"`
	Attachments string `json:"_attachments,omitempty"`
	Timestamp   int    `json:"_ts,omitempty"`
}
