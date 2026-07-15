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
	"context"
	"fmt"
	"strings"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func schedulingPolicyToShardStatus(policy fleet.ManagementClusterSchedulingPolicy) (string, error) {
	switch policy {
	case fleet.ManagementClusterSchedulingPolicySchedulable:
		return ocm.CSProvisionShardStatusActive, nil
	case fleet.ManagementClusterSchedulingPolicyUnschedulable:
		return ocm.CSProvisionShardStatusMaintenance, nil
	default:
		return "", fmt.Errorf("unknown scheduling policy: %q", policy)
	}
}

func buildProvisionShardForCreate(managementCluster *fleet.ManagementCluster, region string) (*arohcpv1alpha1.ProvisionShardBuilder, error) {
	if managementCluster.Status.AKSResourceID == nil {
		return nil, fmt.Errorf("AKSResourceID is required")
	}
	if managementCluster.Status.PublicDNSZoneResourceID == nil {
		return nil, fmt.Errorf("PublicDNSZoneResourceID is required")
	}
	if len(managementCluster.Status.HostedClustersSecretsKeyVaultURL) == 0 {
		return nil, fmt.Errorf("HostedClustersSecretsKeyVaultURL is required")
	}
	if len(managementCluster.Status.HostedClustersManagedIdentitiesKeyVaultURL) == 0 {
		return nil, fmt.Errorf("HostedClustersManagedIdentitiesKeyVaultURL is required")
	}
	if len(managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID) == 0 {
		return nil, fmt.Errorf("HostedClustersSecretsKeyVaultManagedIdentityClientID is required")
	}
	if len(managementCluster.Status.MaestroConsumerName) == 0 {
		return nil, fmt.Errorf("MaestroConsumerName is required")
	}
	if len(managementCluster.Status.MaestroRESTAPIURL) == 0 {
		return nil, fmt.Errorf("MaestroRESTAPIURL is required")
	}
	if len(managementCluster.Status.MaestroGRPCTarget) == 0 {
		return nil, fmt.Errorf("MaestroGRPCTarget is required")
	}
	if len(region) == 0 {
		return nil, fmt.Errorf("region is required")
	}
	if _, err := schedulingPolicyToShardStatus(managementCluster.Spec.SchedulingPolicy); err != nil {
		return nil, err
	}

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
		Topology(ocm.CSProvisionShardTopologyShared), nil
}

// provisionShardStatusUpdateBuilder builds a patch that only sets the shard status.
// All other fields are immutable after create in CS.
// If the shard is already in the desired state, returns nil.
func provisionShardStatusUpdateBuilder(shard *arohcpv1alpha1.ProvisionShard, policy fleet.ManagementClusterSchedulingPolicy) (*arohcpv1alpha1.ProvisionShardBuilder, error) {
	newStatus, err := schedulingPolicyToShardStatus(policy)
	if err != nil {
		return nil, err
	}
	if shard.Status() == newStatus {
		return nil, nil
	}
	return arohcpv1alpha1.NewProvisionShard().Status(newStatus), nil
}

// searchByIdentityKeys scans all provision shards for a match on both AKS
// resource ID and consumer name. Both keys must point to the same shard. A
// partial match (only one key matches) or a duplicate is an error.
func searchByIdentityKeys(ctx context.Context, clustersServiceClient ProvisionShardClient, aksResourceID, consumerName string) (*api.InternalID, *arohcpv1alpha1.ProvisionShard, error) {
	var found *arohcpv1alpha1.ProvisionShard

	iter := clustersServiceClient.ListProvisionShards()
	for shard := range iter.Items(ctx) {
		matchAKS := strings.EqualFold(shard.AzureShard().AksManagementClusterResourceId(), aksResourceID)
		matchConsumer := shard.MaestroConfig().ConsumerName() == consumerName

		if !matchAKS && !matchConsumer {
			continue
		}
		if matchAKS != matchConsumer {
			return nil, nil, fmt.Errorf("shard %s partially matches: AKS=%v consumer=%v", shard.HREF(), matchAKS, matchConsumer)
		}
		if found != nil {
			return nil, nil, fmt.Errorf("multiple shards match AKS resource ID %q and consumer name %q", aksResourceID, consumerName)
		}
		found = shard
	}
	if err := iter.GetError(); err != nil {
		return nil, nil, err
	}
	if found == nil {
		return nil, nil, nil
	}
	shardID, err := api.NewInternalID(found.HREF())
	if err != nil {
		return nil, nil, fmt.Errorf("parsing provision shard HREF: %w", err)
	}
	return &shardID, found, nil
}
