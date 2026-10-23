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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	union "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const BackfillControllerName = "ClusterDNSBackfill"

// TODO: Remove this controller in a follow-up after fleet-wide backfill completes.
// Run against the ServiceLogs entity group. This lists live clusters whose latest
// snapshot lacks a UID, base domain, or matching live reservation. An empty result
// is meaningful only after confirming fresh snapshot coverage for every region;
// clusters missing from the entire lookback window cannot appear in this query.
//
// let snapshots = macro-expand ServiceLogs as region (
//
//	region.cosmosResourceSnapshots
//	| where timestamp between (ago(30d) .. now())
//	| where (resourceType =~ 'Microsoft.RedHatOpenShift/hcpOpenShiftClusters' and cosmosContainer =~ 'resources')
//	    or (resourceType =~ 'Microsoft.RedHatOpenShift/dnsZones/dnsClaims' and cosmosContainer =~ 'fleet')
//	| extend normalizedID = tolower(resourceID)
//	| summarize arg_max(timestamp, *) by environment, cluster, cosmosContainer, normalizedID
//
// );
// let claims = snapshots
//
//	| where cosmosContainer =~ 'fleet' and isempty(content.deletionTimestamp)
//	| where isempty(content.properties.deletionTimestamp)
//	| project environment, cluster, owner = tostring(content.properties.clusterUUID), baseDomain = tostring(content.properties.baseDomain);
//
// snapshots
// | where cosmosContainer =~ 'resources' and isempty(content.deletionTimestamp)
// | where isempty(content.properties.serviceProviderProperties.deletionTimestamp)
// | project environment, cluster, resourceID, owner = tostring(content.properties.serviceProviderProperties.clusterUID),
//
//	baseDomain = tostring(content.properties.serviceProviderProperties.dns.baseDomain)
//
// | join kind=leftanti claims on environment, cluster, owner, baseDomain
type backfillController struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	fleetDBClient     fleetcosmosstorage.FleetDBClient
	clusterLister     corelisters.ClusterLister
	dnsClaimLister    fleetlisters.DNSClaimLister
	readDesireLister  kubeapplierlisters.ReadDesireLister
	clock             clock.PassiveClock
}

func NewBackfillController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	backendInformers coreinformers.BackendInformers,
	fleetInformers fleetinformers.FleetInformers,
	kubeApplierUnionInformers *union.UnionKubeApplierInformers,
	clock clock.PassiveClock,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	claimInformer, dnsClaimLister := fleetInformers.DNSClaims()
	_, readDesireLister := kubeApplierUnionInformers.ReadDesires()
	syncer := &backfillController{
		resourcesDBClient: resourcesDBClient,
		fleetDBClient:     fleetDBClient,
		clusterLister:     clusterLister,
		dnsClaimLister:    dnsClaimLister,
		readDesireLister:  readDesireLister,
		clock:             clock,
	}
	controller := controllerutils.NewClusterWatchingController(BackfillControllerName, resourcesDBClient, backendInformers, kubeApplierUnionInformers, time.Minute, syncer)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(kubeapplierapi.ClusterScopedReadDesireResourceType, kubeapplierapi.ReadDesireNameReadonlyHostedCluster)
	queue := controller.(controllerutils.Enqueuer)
	metadataapi.Must(claimInformer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    enqueueClusterForDNSClaim(queue),
			DeleteFunc: enqueueClusterForDNSClaim(queue),
			UpdateFunc: func(old, new any) {
				enqueueClusterForDNSClaim(queue)(old)
				enqueueClusterForDNSClaim(queue)(new)
			},
		}))

	return controller
}

func (c *backfillController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.needsWork(cluster) {
		return nil
	}
	hostedCluster, _, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	if hostedCluster == nil {
		return nil
	}
	prefix := cluster.CustomerProperties.DNS.BaseDomainPrefix
	expectedPrefix := "api." + prefix + "."
	domain := strings.TrimPrefix(hostedCluster.Spec.KubeAPIServerDNSName, expectedPrefix)
	expectedIngress := "aro." + prefix + "." + domain
	existingBaseDomain := cluster.ServiceProviderProperties.DNS.BaseDomain
	if !strings.HasPrefix(hostedCluster.Spec.KubeAPIServerDNSName, expectedPrefix) || hostedCluster.Spec.DNS.BaseDomain != expectedIngress || (len(existingBaseDomain) != 0 && existingBaseDomain != domain) {
		err := utils.TrackError(fmt.Errorf("DNS mismatch: prefix=%q persistedBaseDomain=%q observedAPI=%q observedIngress=%q", prefix, existingBaseDomain, hostedCluster.Spec.KubeAPIServerDNSName, hostedCluster.Spec.DNS.BaseDomain))
		logger.Error(err, "Observed HyperShift names disagree with cluster DNS intent")
		return err
	}
	if len(existingBaseDomain) == 0 {
		replacement := cluster.DeepCopy()
		replacement.ServiceProviderProperties.DNS.BaseDomain = domain
		logger.Info("Backfilling observed legacy DNS domain", "baseDomain", domain)
		_, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Replace(ctx, replacement, nil)
		return utils.TrackError(err)
	}
	claim, err := c.dnsClaimLister.Get(ctx, domain)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(err)
	}
	if claim != nil {
		if claim.ClusterUUID != cluster.ServiceProviderProperties.ClusterUID || claim.DeletionTimestamp != nil {
			err := utils.TrackError(fmt.Errorf("DNS claim %q is unavailable: belongs to UID %q, expected %q", domain, claim.ClusterUUID, cluster.ServiceProviderProperties.ClusterUID))
			logger.Error(err, "Established DNS reservation belongs to a different cluster incarnation")
			return err
		}
		return nil
	}
	id, err := fleetapihelpers.DNSClaimResourceID(domain)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info("Backfilling reservation for established cluster DNS domain", "baseDomain", domain, "clusterUID", cluster.ServiceProviderProperties.ClusterUID)
	_, err = c.fleetDBClient.DNSClaims(id.Parent.Name).Create(ctx, &fleetapi.DNSClaim{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   id,
			PartitionKey: id.Parent.Name,
		},
		BaseDomain:        domain,
		ClusterUUID:       cluster.ServiceProviderProperties.ClusterUID,
		ClusterResourceID: cluster.ID,
		CreatedAt:         metav1.NewTime(c.clock.Now()),
	}, nil)
	return utils.TrackError(err)
}

func (c *backfillController) needsWork(cluster *coreapi.Cluster) bool {
	// ClusterBaseDomainPrefixSync must first recover the legacy CS prefix.
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil &&
		cluster.ServiceProviderProperties.ClusterServiceID != nil &&
		len(cluster.CustomerProperties.DNS.BaseDomainPrefix) != 0
}
