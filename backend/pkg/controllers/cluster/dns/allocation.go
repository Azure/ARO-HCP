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
	"time"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
)

type action string

// The prefix is a customer vanity name scoped beneath an exclusive DNS slug;
// it requires no additional uniqueness when the customer does not choose one.
const defaultDomainPrefix = "cluster"

const (
	noWrite      action = "no-write"
	writeCluster action = "write-cluster"
	createClaim  action = "create-claim"
	deleteClaim  action = "delete-claim"
)

// A decision carries the complete replacement, including its observed ETag.
// The shell neither defaults fields nor infers additional operations.
type decision struct {
	Action       action
	Cluster      *coreapi.Cluster
	Claim        *fleetapi.DNSClaim
	Err          error
	RequeueAfter time.Duration
}

func (d decision) validate() {
	valid := false
	switch d.Action {
	case noWrite:
		valid = d.Cluster == nil && d.Claim == nil
	case writeCluster:
		valid = d.Cluster != nil && d.Claim == nil && d.Err == nil && d.RequeueAfter == 0
	case createClaim, deleteClaim:
		valid = d.Cluster == nil && d.Claim != nil && d.Err == nil && d.RequeueAfter == 0
	}
	if !valid {
		panic(fmt.Sprintf("invalid DNS decision: %+v", d))
	}
}

// DecideAllocation is deterministic for its inputs. Observations and random candidate results
// are injected, so ownership, payload construction and errors are tested together.
func decideAllocation(logger logr.Logger, cluster *coreapi.Cluster, parentZone string, now time.Time,
	getDNSClaim func(string) (*fleetapi.DNSClaim, error), generateDNSSlug func() string,
) decision {
	// Validate the naming inputs before narrowing the allocation state space.
	profile := cluster.ServiceProviderProperties.DNS
	if profile.BaseDomain != "" && profile.PendingBaseDomain != "" && profile.BaseDomain != profile.PendingBaseDomain {
		return decision{Action: noWrite, Err: fmt.Errorf("committed DNS base domain differs from allocation intent")}
	}
	if parentZone == "" {
		return decision{Action: noWrite, Err: fmt.Errorf("DNS allocation requires the selected shard's parent zone")}
	}
	owner := cluster.ServiceProviderProperties.ClusterUID
	domain := profile.BaseDomain
	if domain == "" {
		domain = profile.PendingBaseDomain
	}
	var claim *fleetapi.DNSClaim
	if domain != "" {
		var err error
		claim, err = getDNSClaim(domain)
		if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return decision{Action: noWrite, Err: err}
		}
	}
	if claim != nil && claim.Owner != owner && profile.BaseDomain != "" {
		err := fmt.Errorf("committed DNS base domain %q has a conflicting claim owned by %q", domain, claim.Owner)
		logger.Error(err, "Established DNS allocation has conflicting ownership", "clusterUID", owner)
		return decision{Action: noWrite, Err: err}
	}
	needsInitialSlug := domain == ""
	// Inputs are valid and any committed domain has no conflicting owner.
	candidateOwnedByAnotherCluster := claim != nil && claim.Owner != owner
	if needsInitialSlug || candidateOwnedByAnotherCluster {
		slug := generateDNSSlug()
		updated := cluster.DeepCopy()
		updated.ServiceProviderProperties.DNS = coreapi.ServiceProviderDNSProfile{PendingBaseDomain: slug + "." + strings.ToLower(parentZone)}
		if updated.CustomerProperties.DNS.BaseDomainPrefix == "" {
			updated.CustomerProperties.DNS.BaseDomainPrefix = defaultDomainPrefix
		}
		if claim != nil {
			logger.Info("DNS candidate belongs to another cluster; choosing a replacement", "occupiedDomain", domain, "owner", claim.Owner, "candidate", updated.ServiceProviderProperties.DNS.PendingBaseDomain)
		} else {
			logger.Info("Persisting initial DNS candidate", "candidate", updated.ServiceProviderProperties.DNS.PendingBaseDomain)
		}
		return decision{Action: writeCluster, Cluster: updated}
	}
	// A candidate (or committed domain) exists and has no conflicting owner.
	// Without an observed reservation, attempt to claim that exact name.
	if claim == nil {
		id, err := fleetapihelpers.DNSClaimResourceID(domain)
		if err != nil {
			return decision{Action: noWrite, Err: err}
		}
		logger.Info("Candidate has no observed reservation; attempting exclusive claim", "baseDomain", domain, "clusterUID", owner)
		return decision{Action: createClaim, Claim: &fleetapi.DNSClaim{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: id.Parent.Name},
			BaseDomain:     domain, Owner: owner, ClusterResourceID: cluster.ID.String(), CreatedAt: metav1.NewTime(now),
		}}
	}
	// The reservation is ours. Only an uncommitted candidate needs promotion.
	if profile.BaseDomain == "" {
		updated := cluster.DeepCopy()
		updated.ServiceProviderProperties.DNS = coreapi.ServiceProviderDNSProfile{BaseDomain: domain}
		logger.Info("Observed our DNS claim; committing cluster base domain", "baseDomain", domain, "clusterUID", owner)
		return decision{Action: writeCluster, Cluster: updated}
	}
	return decision{Action: noWrite}
}

// decideAllocationInputs owns eligibility and prerequisite handling. The shell may
// over-read cheap listers; irrelevant observation errors do not drive decisions.
func decideAllocationInputs(logger logr.Logger, cluster *coreapi.Cluster, provider *coreapi.ServiceProviderCluster, providerErr error, subscription *coreapi.Subscription, subscriptionErr error, now time.Time,
	getManagementCluster func(string) (*fleetapi.ManagementCluster, error), hasCSCluster func(string) (bool, error), getDNSClaim func(string) (*fleetapi.DNSClaim, error), generateDNSSlug func() string) decision {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil || cluster.ServiceProviderProperties.ClusterServiceID != nil || cluster.ServiceProviderProperties.ClusterUID == "" || cluster.ServiceProviderProperties.PendingClusterServiceID == nil {
		return decision{Action: noWrite}
	}
	if providerErr != nil && !cosmosstorageutils.IsNotFoundError(providerErr) {
		return decision{Action: noWrite, Err: providerErr}
	}
	if provider == nil || provider.Spec.ManagementClusterResourceID == nil {
		return decision{Action: noWrite}
	}
	if subscriptionErr != nil {
		return decision{Action: noWrite, Err: subscriptionErr}
	}
	// Basic shape validation precedes placement and adoption business decisions.
	if subscription == nil || subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return decision{Action: noWrite, Err: fmt.Errorf("subscription has no tenant ID")}
	}
	if provider.Spec.ManagementClusterResourceID.Parent == nil {
		return decision{Action: noWrite, Err: fmt.Errorf("invalid management cluster placement ID")}
	}
	exists, err := hasCSCluster(*subscription.Properties.TenantId)
	if err != nil {
		return decision{Action: noWrite, Err: err}
	}
	if exists {
		// ClusterClusterServiceCreate adopts before ClusterDNSBackfill observes actual names.
		return decision{Action: noWrite}
	}
	management, err := getManagementCluster(provider.Spec.ManagementClusterResourceID.Parent.Name)
	if err != nil {
		return decision{Action: noWrite, Err: err}
	}
	if management == nil || management.Status.PublicDNSZoneResourceID == nil {
		return decision{Action: noWrite, Err: fmt.Errorf("selected management cluster has no public DNS zone")}
	}
	return decideAllocation(logger, cluster, management.Status.PublicDNSZoneResourceID.Name, now, getDNSClaim, generateDNSSlug)
}

// execute performs the single mutation chosen by a DNS controller.
func execute(ctx context.Context, db corecosmosstorage.ResourcesDBClient, fleet fleetcosmosstorage.FleetDBClient, d decision) error {
	d.validate()
	var err error
	switch d.Action {
	case noWrite:
		return d.Err
	case writeCluster:
		_, err = db.HCPClusters(d.Cluster.ID.SubscriptionID, d.Cluster.ID.ResourceGroupName).Replace(ctx, d.Cluster, nil)
	case createClaim:
		_, err = fleet.DNSClaims(d.Claim.ResourceID.Parent.Name).Create(ctx, d.Claim, nil)
	case deleteClaim:
		err = fleet.DNSClaims(d.Claim.ResourceID.Parent.Name).Delete(ctx, d.Claim.ResourceID.Name, &azcosmos.ItemOptions{IfMatchEtag: &d.Claim.CosmosETag})
	}
	return err
}
