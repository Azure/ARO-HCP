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

package clustersserviceregistration

import (
	"fmt"

	arohcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func baseProvisionShardBuilder(managementCluster *fleet.ManagementCluster, region string) *arohcpv1alpha1.ProvisionShardBuilder {
	return arohcpv1alpha1.NewProvisionShard().
		CloudProvider(arohcpv1alpha1.NewCloudProvider().ID(ocm.CSCloudProvider)).
		Region(arohcpv1alpha1.NewCloudRegion().ID(region)).
		AzureShard(arohcpv1alpha1.NewAzureShard().
			AksManagementClusterResourceId(managementCluster.Status.AKSResourceID.String()).
			PublicDnsZoneResourceId(managementCluster.Status.PublicDNSZoneResourceID.String()).
			CxSecretsKeyVaultUrl(managementCluster.Status.HostedClustersSecretsKeyVaultURL).
			CxManagedIdentitiesKeyVaultUrl(managementCluster.Status.HostedClustersManagedIdentitiesKeyVaultURL).
			CxSecretsKeyVaultManagedIdentityClientId(managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID),
		).
		MaestroConfig(arohcpv1alpha1.NewProvisionShardMaestroConfig().
			ConsumerName(managementCluster.Status.MaestroConsumerName).
			RestApiConfig(arohcpv1alpha1.NewProvisionShardMaestroRestApiConfig().
				Url(managementCluster.Status.MaestroRESTAPIURL),
			).
			GrpcApiConfig(arohcpv1alpha1.NewProvisionShardMaestroGrpcApiConfig().
				Url(managementCluster.Status.MaestroGRPCTarget),
			),
		).
		Topology(ocm.CSProvisionShardTopologyShared)
}

func schedulingPolicyToShardStatus(policy fleet.ManagementClusterSchedulingPolicy) string {
	if policy == fleet.ManagementClusterSchedulingPolicyUnschedulable {
		return ocm.CSProvisionShardStatusMaintenance
	}
	return ocm.CSProvisionShardStatusActive
}

func buildProvisionShardForCreate(managementCluster *fleet.ManagementCluster, region string) *arohcpv1alpha1.ProvisionShardBuilder {
	builder := baseProvisionShardBuilder(managementCluster, region)
	if managementCluster.Spec.SchedulingPolicy == fleet.ManagementClusterSchedulingPolicyUnschedulable {
		builder.Status(ocm.CSProvisionShardStatusMaintenance)
	}
	return builder
}

func buildProvisionShardForUpdate(managementCluster *fleet.ManagementCluster) *arohcpv1alpha1.ProvisionShardBuilder {
	return arohcpv1alpha1.NewProvisionShard().
		Topology(ocm.CSProvisionShardTopologyShared).
		Status(schedulingPolicyToShardStatus(managementCluster.Spec.SchedulingPolicy))
}

func shardConditionForPolicy(policy fleet.ManagementClusterSchedulingPolicy) (fleet.ManagementClusterConditionReason, string) {
	switch policy {
	case fleet.ManagementClusterSchedulingPolicySchedulable:
		return fleet.ManagementClusterConditionReasonProvisionShardActive, "Provision shard is active"
	case fleet.ManagementClusterSchedulingPolicyUnschedulable:
		return fleet.ManagementClusterConditionReasonProvisionShardMaintenance, "Provision shard is in maintenance"
	default:
		return fleet.ManagementClusterConditionReasonProvisionShardStatusUnknown, fmt.Sprintf("Unknown scheduling policy %q", policy)
	}
}
