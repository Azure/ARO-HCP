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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// ResourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries. That will be about a month from now.
	// Example: "/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/rg/providers/microsoft.redhatopenshiftmanagement/hcpmanagementclusters/pers-westus3-mgmt-1"
	//
	// +required, immutable once set.
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
	// Conditions is a list of conditions tracking the lifecycle of the management cluster.
	// Known condition types are defined as ManagementClusterConditionType constants:
	// Ready.
	//
	// Conditions are added on first evaluation and never removed. Status is toggled
	// between True/False/Unknown. Absence of a condition means "not yet evaluated."
	// Each condition type is owned by exactly one controller to avoid write conflicts.
	//
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AKSResourceID is the Azure resource ID of the AKS management cluster.
	// Example: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/clustername"
	//
	// +optional, validated as a well-formed ARM resource ID, immutable once set.
	// Required when Ready condition is True.
	AKSResourceID *azcorearm.ResourceID `json:"aksResourceID,omitempty"`

	// PublicDNSZoneResourceID is the Azure resource ID of the public DNS zone for the management cluster.
	// Example: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/dns-rg/providers/Microsoft.Network/dnszones/example.com"
	//
	// +optional, validated as a well-formed ARM resource ID, immutable once set.
	// Required when Ready condition is True.
	PublicDNSZoneResourceID *azcorearm.ResourceID `json:"publicDNSZoneResourceID,omitempty"`

	// HostedClustersSecretsKeyVaultURL is the URL of the key vault containing secrets for hosted clusters on this management cluster.
	// Example: "https://kv-hc-secrets.vault.azure.net"
	//
	// +optional, validated as a well-formed URL, immutable once set.
	// Required when Ready condition is True.
	HostedClustersSecretsKeyVaultURL string `json:"hostedClustersSecretsKeyVaultURL,omitempty"`

	// HostedClustersManagedIdentitiesKeyVaultURL is the URL of the key vault containing managed identity backing certificates for hosted clusters.
	// Example: "https://kv-hc-mi.vault.azure.net"
	//
	// +optional, validated as a well-formed URL, immutable once set.
	// Required when Ready condition is True.
	HostedClustersManagedIdentitiesKeyVaultURL string `json:"hostedClustersManagedIdentitiesKeyVaultURL,omitempty"`

	// HostedClustersSecretsKeyVaultManagedIdentityClientID is the client ID of the managed identity
	// used to access the hosted clusters secrets key vault.
	// Example: "12345678-1234-1234-1234-123456789012"
	//
	// +optional, validated as a UUID, immutable once set.
	// Required when Ready condition is True.
	HostedClustersSecretsKeyVaultManagedIdentityClientID string `json:"hostedClustersSecretsKeyVaultManagedIdentityClientID,omitempty"`

	// MaestroConsumerName is the consumer name of the management cluster in Maestro.
	// Typically derived from the management cluster stamp identifier.
	// Example: "hcp-underlay-westus3-mgmt-1"
	//
	// +optional, immutable once set.
	// Required when Ready condition is True.
	MaestroConsumerName string `json:"maestroConsumerName,omitempty"`

	// MaestroRESTAPIURL is the URL of the Maestro REST API.
	// Example: "http://maestro.maestro.svc.cluster.local:8000"
	//
	// +optional, validated as a well-formed URL, immutable once set.
	// Required when Ready condition is True.
	MaestroRESTAPIURL string `json:"maestroRESTAPIURL,omitempty"`

	// MaestroGRPCTarget is the gRPC dial target (host:port) of the Maestro GRPC API.
	// Example: "maestro-grpc.maestro.svc.cluster.local:8090"
	//
	// +optional, immutable once set.
	// Required when Ready condition is True.
	MaestroGRPCTarget string `json:"maestroGRPCTarget,omitempty"`

	// ClusterServiceProvisionShardID is the Cluster Service provision shard HREF for this management cluster.
	// Example: "/api/aro_hcp/v1alpha1/provision_shards/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	//
	// +optional, immutable once set.
	// Required when Ready condition is True.
	ClusterServiceProvisionShardID *InternalID `json:"clusterServiceProvisionShardID,omitempty"`
}
