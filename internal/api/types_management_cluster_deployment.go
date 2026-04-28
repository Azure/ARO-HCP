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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ManagementClusterDeploymentConditionType represents the type of a management cluster deployment condition.
type ManagementClusterDeploymentConditionType string

// ManagementClusterDeploymentConditionReason represents the reason for a management cluster deployment condition.
type ManagementClusterDeploymentConditionReason string

const (
	// ManagementClusterDeploymentConditionApproved indicates whether the deployment
	// has been approved for promotion to a ManagementCluster.
	// In non-production environments, this is set automatically.
	// In production, it requires explicit SRE action via the admin API / Geneva action.
	ManagementClusterDeploymentConditionApproved ManagementClusterDeploymentConditionType = "Approved"

	// ManagementClusterDeploymentConditionReasonAutoApproved indicates the deployment
	// was automatically approved.
	ManagementClusterDeploymentConditionReasonAutoApproved ManagementClusterDeploymentConditionReason = "AutoApproved"

	// ManagementClusterDeploymentConditionReasonManuallyApproved indicates the deployment
	// was approved by an SRE via the admin API.
	ManagementClusterDeploymentConditionReasonManuallyApproved ManagementClusterDeploymentConditionReason = "ManuallyApproved"

	// ManagementClusterDeploymentConditionReasonApprovalRevoked indicates the deployment's
	// approval was revoked via the admin API.
	ManagementClusterDeploymentConditionReasonApprovalRevoked ManagementClusterDeploymentConditionReason = "ApprovalRevoked"
)

// ManagementClusterDeployment represents the infrastructure definition of a management cluster,
// created by the management cluster pipeline at the end of provisioning.
// It is analogous to a CAPI Machine, where the ManagementCluster document is the Node.
// The ManagementClusterPromotionController watches deployments and, when approved and valid,
// creates or updates the corresponding ManagementCluster document.
type ManagementClusterDeployment struct {
	CosmosMetadata `json:"cosmosMetadata"`

	Spec ManagementClusterDeploymentSpec `json:"spec"`

	Status ManagementClusterDeploymentStatus `json:"status"`
}

// ManagementClusterDeploymentSpec contains the desired state of a management cluster deployment.
// Reserved for future provisioning intent (e.g., constraints, features, sizing, ...).
type ManagementClusterDeploymentSpec struct{}

// ManagementClusterDeploymentStatus contains the observed state of a management cluster deployment.
// Infrastructure fields are set by the provisioning pipeline after infrastructure is created.
// In a future phase, a provisioning controller will set these fields instead.
type ManagementClusterDeploymentStatus struct {
	// Conditions tracks the deployment's progression toward becoming a ManagementCluster.
	// Known condition types: Approved.
	//
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AKSResourceID is the Azure resource ID of the AKS management cluster.
	// Example: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/clustername"
	//
	// +required, validated as a well-formed ARM resource ID, immutable once set.
	AKSResourceID *azcorearm.ResourceID `json:"aksResourceID,omitempty"`

	// PublicDNSZoneResourceID is the Azure resource ID of the public DNS zone for the management cluster.
	// Example: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Network/dnszones/example.com"
	//
	// +required, validated as a well-formed ARM resource ID, immutable once set.
	PublicDNSZoneResourceID *azcorearm.ResourceID `json:"publicDNSZoneResourceID,omitempty"`

	// HostedClustersSecretsKeyVaultURL is the URL of the key vault containing secrets for hosted clusters.
	// Example: "https://kv-hc-secrets.vault.azure.net"
	//
	// +required, validated as a well-formed URL, immutable once set.
	HostedClustersSecretsKeyVaultURL string `json:"hostedClustersSecretsKeyVaultURL,omitempty"`

	// HostedClustersManagedIdentitiesKeyVaultURL is the URL of the key vault containing
	// managed identity backing certificates for hosted clusters.
	// Example: "https://kv-hc-mi.vault.azure.net"
	//
	// +required, validated as a well-formed URL, immutable once set.
	HostedClustersManagedIdentitiesKeyVaultURL string `json:"hostedClustersManagedIdentitiesKeyVaultURL,omitempty"`

	// HostedClustersSecretsKeyVaultManagedIdentityClientID is the client ID of the managed identity
	// used to access the hosted clusters secrets key vault.
	// Example: "12345678-1234-1234-1234-123456789012"
	//
	// +required, validated as a UUID, immutable once set.
	HostedClustersSecretsKeyVaultManagedIdentityClientID string `json:"hostedClustersSecretsKeyVaultManagedIdentityClientID,omitempty"`

	// MaestroConsumerName is the consumer name of the management cluster in Maestro.
	// Example: "hcp-underlay-westus3-mgmt-1"
	//
	// +required, immutable once set.
	MaestroConsumerName string `json:"maestroConsumerName,omitempty"`

	// MaestroRESTAPIURL is the URL of the Maestro REST API.
	// Example: "http://maestro.maestro.svc.cluster.local:8000"
	//
	// +required, validated as a well-formed URL, immutable once set.
	MaestroRESTAPIURL string `json:"maestroRESTAPIURL,omitempty"`

	// MaestroGRPCTarget is the gRPC dial target (host:port) of the Maestro GRPC API.
	// Example: "maestro-grpc.maestro.svc.cluster.local:8090"
	//
	// +required, validated as host:port, immutable once set.
	MaestroGRPCTarget string `json:"maestroGRPCTarget,omitempty"`

	// ManagementClusterID is set by the ManagementClusterPromotionController when the
	// deployment is promoted to a ManagementCluster. References the resulting
	// ManagementCluster document. Analogous to CAPI Machine's status.nodeRef.
	//
	// +optional, immutable once set.
	ManagementClusterID *azcorearm.ResourceID `json:"managementClusterID,omitempty"`
}
