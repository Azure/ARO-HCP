// Copyright 2026 Microsoft Corporation
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
	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ManagementClusterConditionType represents the type of a management cluster condition.
//
// Condition lifecycle follows Kubernetes conventions:
//   - Conditions are added on first evaluation and never removed.
//   - Status is toggled between True/False/Unknown; absence means "not yet evaluated."
//   - Each condition type is owned by exactly one controller (see ownership below).
type ManagementClusterConditionType string

// ManagementClusterConditionReason represents the reason for a management cluster condition.
type ManagementClusterConditionReason string

const (
	// ManagementClusterConditionReady indicates the management cluster is
	// provisioned and operational.
	ManagementClusterConditionReady ManagementClusterConditionType = "Ready"

	// ManagementClusterConditionReasonProvisionShardActive indicates the CS provision
	// shard is active and the management cluster is ready for scheduling.
	ManagementClusterConditionReasonProvisionShardActive ManagementClusterConditionReason = "ProvisionShardActive"

	// ManagementClusterConditionReasonProvisionShardMaintenance indicates the CS provision
	// shard is in maintenance mode.
	ManagementClusterConditionReasonProvisionShardMaintenance ManagementClusterConditionReason = "ProvisionShardMaintenance"

	// ManagementClusterConditionReasonProvisionShardOffline indicates the CS provision
	// shard is offline.
	ManagementClusterConditionReasonProvisionShardOffline ManagementClusterConditionReason = "ProvisionShardOffline"

	// ManagementClusterConditionReasonProvisionShardStatusUnknown indicates the CS provision
	// shard has an unknown status.
	ManagementClusterConditionReasonProvisionShardStatusUnknown ManagementClusterConditionReason = "ProvisionShardStatusUnknown"

	// Future condition types to consider:
	// - "Upgrading": a provisioning/upgrade run is in progress (owner: provisioning controller)
	// - "ProvisioningFailed": last provisioning attempt failed (owner: provisioning controller)
	// - "UpgradeFailed": last upgrade attempt failed (owner: provisioning controller)
	// - "RegisteredInClusterService": provision shard registered in CS (owner: CS-push controller)
)

// ManagementClusterSchedulingPolicy controls whether new hosted control planes
// may be scheduled onto a management cluster. Follows the Kubernetes typed
// string enum pattern (like TaintEffect, RestartPolicy).
type ManagementClusterSchedulingPolicy string

const (
	// ManagementClusterSchedulingPolicySchedulable allows new HCPs to be
	// scheduled on the cluster (subject to Ready condition and capacity).
	ManagementClusterSchedulingPolicySchedulable ManagementClusterSchedulingPolicy = "Schedulable"

	// ManagementClusterSchedulingPolicyUnschedulable prevents new HCPs from
	// being scheduled regardless of capacity. Analogous to cordoning a
	// Kubernetes Node via kubectl cordon.
	ManagementClusterSchedulingPolicyUnschedulable ManagementClusterSchedulingPolicy = "Unschedulable"
)

// ValidManagementClusterSchedulingPolicies is the set of valid values for
// ManagementClusterSchedulingPolicy.
var ValidManagementClusterSchedulingPolicies = sets.New(
	ManagementClusterSchedulingPolicySchedulable,
	ManagementClusterSchedulingPolicyUnschedulable,
)

// ManagementCluster is a target for provisioning hosted control planes.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ManagementCluster struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	// it will be the ManagementClusterResourceTypeName
	CosmosMetadata `json:"cosmosMetadata"`

	// resourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries. That will be about a month from now.
	ResourceID *azcorearm.ResourceID `json:"resourceId,omitempty"`

	// Spec contains the desired state of the management cluster
	Spec ManagementClusterSpec `json:"spec"`

	// Status contains the observed state of the management cluster
	Status ManagementClusterStatus `json:"status"`
}

// ManagementClusterSpec contains the desired state of a management cluster.
type ManagementClusterSpec struct {
	// SchedulingPolicy controls whether new hosted control planes can be scheduled
	// on this management cluster.
	//
	// Valid values:
	//   - "Schedulable": management cluster accepts new HCPs (subject to Ready
	//     condition and capacity constraints)
	//   - "Unschedulable": management cluster rejects new HCPs regardless of capacity
	//     (analogous to cordoning a Kubernetes Node via kubectl cordon)
	//
	// Must be set explicitly. Empty string is not allowed.
	//
	// Ownership: currently synced from Cluster Service provision shard status
	// by ManagementClusterSyncController (temporary, during CS-to-Cosmos migration).
	// Will transition to being owned by the admin API via a Geneva Action for
	// SRE-initiated cordon/uncordon operations.
	SchedulingPolicy ManagementClusterSchedulingPolicy `json:"schedulingPolicy"`
}

// ManagementClusterStatus contains the observed state of a management cluster.
type ManagementClusterStatus struct {
	// AKSResourceID is the Azure resource ID of the AKS management cluster.
	// Format: /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.ContainerService/managedClusters/{name}
	AKSResourceID *azcorearm.ResourceID `json:"aksResourceID,omitempty"`

	// PublicDNSZoneResourceID is the Azure resource ID of the public DNS zone for the management cluster.
	// Format: /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/dnszones/{name}
	PublicDNSZoneResourceID *azcorearm.ResourceID `json:"publicDNSZoneResourceID,omitempty"`

	// CXSecretsKeyVaultURL is the URL of the key vault containing CX (customer related) secrets for the management cluster.
	CXSecretsKeyVaultURL string `json:"cxSecretsKeyVaultURL,omitempty"`

	// CXManagedIdentitiesKeyVaultURL is the URL of the key vault containing customer managed identity backing certificates
	CXManagedIdentitiesKeyVaultURL string `json:"cxManagedIdentitiesKeyVaultURL,omitempty"`

	// CXSecretsKeyVaultManagedIdentityClientID is the client ID of the managed identity for the management cluster.
	// Format: UUID
	CXSecretsKeyVaultManagedIdentityClientID string `json:"cxSecretsKeyVaultManagedIdentityClientID,omitempty"`

	// MaestroConfig contains the Maestro connectivity configuration for the management cluster.
	MaestroConfig MaestroConfig `json:"maestroConfig,omitempty"`

	// CSProvisionShardID is the Cluster Service provision shard ID for this management cluster.
	CSProvisionShardID string `json:"csProvisionShardID,omitempty"`

	// Conditions is a list of conditions tracking the lifecycle of the management cluster.
	// Known condition types are defined as ManagementClusterConditionType constants:
	// Ready.
	//
	// Conditions are added on first evaluation and never removed. Status is toggled
	// between True/False/Unknown. Absence of a condition means "not yet evaluated."
	// Each condition type is owned by exactly one controller to avoid write conflicts.
	Conditions []Condition `json:"conditions,omitempty"`
}

// MaestroConfig contains the Maestro configuration for the management cluster
// that includes the connectivity configuration and the consumer name.
type MaestroConfig struct {
	// RESTAPIConfig contains the connectivity configuration for the Maestro REST API
	RESTAPIConfig MaestroRESTAPIConfig `json:"restAPIConfig"`

	// GRPCAPIConfig contains the connectivity configuration for the Maestro GRPC API
	GRPCAPIConfig MaestroGRPCAPIConfig `json:"grpcAPIConfig"`

	// ConsumerName is the consumer name of the management cluster in Maestro.
	// Typically derived from the management cluster stamp identifier.
	// Example: "hcp-underlay-westus3-mgmt-1"
	ConsumerName string `json:"consumerName"`
}

// MaestroRESTAPIConfig contains the connectivity configuration for the Maestro REST API
type MaestroRESTAPIConfig struct {
	// URL is the URL of the Maestro REST API.
	// Example: "http://maestro.maestro.svc.cluster.local:8000"
	URL string `json:"url"`
}

// MaestroGRPCAPIConfig contains the connectivity configuration for the Maestro GRPC API
type MaestroGRPCAPIConfig struct {
	// URL is the URL of the Maestro GRPC API.
	// Example: "maestro-grpc.maestro.svc.cluster.local:8090"
	URL string `json:"url"`
}
