package database

import (
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// HCPOpenShiftClusterDocument represents an HCP OpenShift cluster document.
type HCPOpenShiftClusterDocument struct {
	ID           string          `json:"id,omitempty"`
	Key          string          `json:"key,omitempty"`
	PartitionKey string          `json:"partitionKey,omitempty"`
	InternalID   ocm.InternalID  `json:"internalId,omitempty"`
	SystemData   *arm.SystemData `json:"systemData,omitempty"`

	// Values provided by Cosmos after doc creation
	ResourceID  string `json:"_rid,omitempty"`
	Self        string `json:"_self,omitempty"`
	ETag        string `json:"_etag,omitempty"`
	Attachments string `json:"_attachments,omitempty"`
	Timestamp   int    `json:"_ts,omitempty"`
}

// NodePoolDocument represents an HCP OpenShift NodePool document.
type NodePoolDocument struct {
	ID           string          `json:"id,omitempty"`
	Key          string          `json:"key,omitempty"`
	PartitionKey string          `json:"partitionKey,omitempty"`
	InternalID   ocm.InternalID  `json:"internalId,omitempty"`
	SystemData   *arm.SystemData `json:"systemData,omitempty"`

	// Values provided by Cosmos after doc creation
	ResourceID  string `json:"_rid,omitempty"`
	Self        string `json:"_self,omitempty"`
	ETag        string `json:"_etag,omitempty"`
	Attachments string `json:"_attachments,omitempty"`
	Timestamp   int    `json:"_ts,omitempty"`
}

// OperationDocument tracks an asynchronous operation.
type OperationDocument struct {
	// ID is the operation ID as exposed by the "operationStatuses" endpoint
	ID string `json:"id,omitempty"`
	// Request is the type of operation requested; one of Create, Update or Delete
	Request string `json:"request,omitempty"`
	// ExternalID is the Azure resource ID of the cluster or node pool
	ExternalID string `json:"externalId,omitempty"`
	// InternalID is the Cluster Service resource identifier in the form of a URL path
	// "/cluster/{cluster_id}" or "/cluster/{cluster_id}/node_pools/{node_pool_id}"
	InternalID string `json:"internalId,omitempty"`
	// NotificationURI is provided by the Azure-AsyncNotificationUri header if the
	// Async Operation Callbacks ARM feature is enabled
	NotificationURI string `json:"notificationUri,omitempty"`

	// LastTransitionTime is the timestamp of the most recent state change
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
	// State is the cluster or node pool state as reported by Cluster Service
	State string `json:"state,omitempty"`
	// Details is a description or error message as reported by Cluster Service
	Details string `json:"details,omitempty"`
	// Terminal indicates if the operation has reached a terminal provisioning state
	Terminal bool `json:"terminal,omitempty"`

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
