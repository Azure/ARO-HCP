// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dns

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// allocate persists at most one step: naming intent, its exclusive claim, or
// the committed customer-visible name. Observing our claim precedes publication.
func (c *dnsAllocationController) allocate(ctx context.Context, cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster, parentZone string) error {
	logger := utils.LoggerFromContext(ctx)
	committedDomain := cluster.ServiceProviderProperties.DNS.BaseDomain
	domain := serviceProviderCluster.Spec.DesiredDNSBaseDomain
	if len(committedDomain) != 0 && len(domain) != 0 && committedDomain != domain {
		return utils.TrackError(fmt.Errorf("committed DNS base domain differs from allocation intent"))
	}
	if len(parentZone) == 0 {
		return utils.TrackError(fmt.Errorf("DNS allocation requires the selected shard's parent zone"))
	}
	// Committed names are immutable, including clusters that predate provider intent.
	if len(domain) == 0 {
		domain = committedDomain
	}
	var claim *fleetapi.DNSClaim
	if len(domain) != 0 {
		var err error
		claim, err = c.dnsClaimLister.Get(ctx, domain)
		if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(err)
		}
	}
	owner := cluster.ServiceProviderProperties.ClusterUID
	unavailable := claim != nil && (claim.ClusterUUID != owner || claim.DeletionTimestamp != nil)
	if unavailable && len(committedDomain) != 0 {
		return utils.TrackError(fmt.Errorf("committed DNS base domain %q has an unavailable claim owned by %q", domain, claim.ClusterUUID))
	}
	// Valid inputs with no established name: select or replace a candidate.
	if len(domain) == 0 || unavailable {
		replacement := serviceProviderCluster.DeepCopy()
		replacement.Spec.DesiredDNSBaseDomain = generateDNSSlug() + "." + strings.ToLower(parentZone)
		logger.Info("Persisting DNS allocation candidate", "previousCandidate", domain, "candidate", replacement.Spec.DesiredDNSBaseDomain)
		_, err := c.resourcesDBClient.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name).Replace(ctx, replacement, nil)
		return utils.TrackError(err)
	}
	// A candidate exists; exclusively claim it before publishing the base domain.
	if claim == nil {
		claimResourceID, err := fleetapihelpers.DNSClaimResourceID(domain)
		if err != nil {
			return utils.TrackError(err)
		}
		logger.Info("Attempting exclusive DNS claim", "baseDomain", domain, "clusterUID", owner)
		_, err = c.fleetDBClient.DNSClaims(claimResourceID.Parent.Name).Create(ctx, &fleetapi.DNSClaim{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: claimResourceID, PartitionKey: claimResourceID.Parent.Name},
			BaseDomain:     domain, ClusterUUID: owner, ClusterResourceID: cluster.ID, CreatedAt: metav1.NewTime(c.clock.Now()),
		}, nil)
		return utils.TrackError(err)
	}
	// Our reservation is observed. Publish the name without erasing provider intent.
	if len(committedDomain) == 0 || len(cluster.CustomerProperties.DNS.BaseDomainPrefix) == 0 {
		replacement := cluster.DeepCopy()
		replacement.ServiceProviderProperties.DNS.BaseDomain = domain
		if len(replacement.CustomerProperties.DNS.BaseDomainPrefix) == 0 {
			// Covers unallocated clusters accepted before admission defaulting was enabled.
			replacement.CustomerProperties.DNS.BaseDomainPrefix = "cluster"
		}
		logger.Info("Observed our DNS claim; committing cluster base domain", "baseDomain", domain, "clusterUID", owner)
		_, err := c.resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Replace(ctx, replacement, nil)
		return utils.TrackError(err)
	}
	return nil
}
