package database

import "github.com/Azure/ARO-HCP/internal/api/arm"

// HCPOpenShiftClusterDocument represents an HCP OpenShift cluster document.
type HCPOpenShiftClusterDocument struct {
	ID           string `json:"id,omitempty"`
	Key          string `json:"key,omitempty"`
	PartitionKey string `json:"partitionKey,omitempty"`
	ClusterID    string `json:"clusterid,omitempty"`

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
