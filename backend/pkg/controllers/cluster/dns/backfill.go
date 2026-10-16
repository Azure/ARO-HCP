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
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
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

type backfillController struct {
	db       corecosmosstorage.ResourcesDBClient
	fleet    fleetcosmosstorage.FleetDBClient
	clusters corelisters.ClusterLister
	claims   fleetlisters.DNSClaimLister
	reads    kubeapplierlisters.ReadDesireLister
	clock    clock.PassiveClock
}

func NewBackfillController(db corecosmosstorage.ResourcesDBClient, fleet fleetcosmosstorage.FleetDBClient, informers coreinformers.BackendInformers, fi fleetinformers.FleetInformers, ki *union.UnionKubeApplierInformers, clk clock.PassiveClock) controllerutils.Controller {
	_, clusters := informers.Clusters()
	claimInformer, claims := fi.DNSClaims()
	_, reads := ki.ReadDesires()
	syncer := &backfillController{db: db, fleet: fleet, clusters: clusters, claims: claims, reads: reads, clock: clk}
	controller := controllerutils.NewClusterWatchingController(BackfillControllerName, db, informers, ki, time.Minute, syncer)
	queue := controller.(controllerutils.Enqueuer)
	onClaim := func(obj any) {
		if key, ok := claimClusterKey(obj); ok {
			queue.Enqueue(key)
		}
	}
	if _, err := claimInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: onClaim, DeleteFunc: onClaim, UpdateFunc: func(old, new any) { onClaim(old); onClaim(new) }}); err != nil {
		panic(err)
	}
	return controller
}

func decideBackfill(logger logr.Logger, cluster *coreapi.Cluster, hc *hsv1beta1.HostedCluster, observationErr error, now time.Time, getDNSClaim func(string) (*fleetapi.DNSClaim, error)) decision {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil || cluster.ServiceProviderProperties.ClusterServiceID == nil || cluster.ServiceProviderProperties.ClusterUID == "" {
		return decision{Action: noWrite}
	}
	prefix := cluster.CustomerProperties.DNS.BaseDomainPrefix
	if prefix == "" {
		// ClusterBaseDomainPrefixSync must first recover the legacy CS prefix.
		return decision{Action: noWrite}
	}
	if observationErr != nil {
		return decision{Action: noWrite, Err: observationErr}
	}
	if hc == nil {
		return decision{Action: noWrite}
	}
	expectedPrefix := "api." + prefix + "."
	domain := strings.TrimPrefix(hc.Spec.KubeAPIServerDNSName, expectedPrefix)
	expectedIngress := "aro." + prefix + "." + domain
	persisted := cluster.ServiceProviderProperties.DNS.BaseDomain
	if !strings.HasPrefix(hc.Spec.KubeAPIServerDNSName, expectedPrefix) || hc.Spec.DNS.BaseDomain != expectedIngress || (persisted != "" && persisted != domain) {
		err := fmt.Errorf("DNS mismatch: prefix=%q persistedBaseDomain=%q observedAPI=%q observedIngress=%q", prefix, persisted, hc.Spec.KubeAPIServerDNSName, hc.Spec.DNS.BaseDomain)
		logger.Error(err, "Observed HyperShift names disagree with cluster DNS intent")
		return decision{Action: noWrite, Err: err}
	}
	if persisted == "" {
		updated := cluster.DeepCopy()
		updated.ServiceProviderProperties.DNS.BaseDomain = domain
		logger.Info("Backfilling observed legacy DNS domain", "baseDomain", domain)
		return decision{Action: writeCluster, Cluster: updated}
	}
	claim, err := getDNSClaim(domain)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return decision{Action: noWrite, Err: err}
	}
	if claim != nil {
		if claim.Owner != cluster.ServiceProviderProperties.ClusterUID {
			err := fmt.Errorf("DNS claim %q belongs to UID %q, expected %q", domain, claim.Owner, cluster.ServiceProviderProperties.ClusterUID)
			logger.Error(err, "Established DNS reservation belongs to a different cluster incarnation")
			return decision{Action: noWrite, Err: err}
		}
		return decision{Action: noWrite}
	}
	id, err := fleetapihelpers.DNSClaimResourceID(domain)
	if err != nil {
		return decision{Action: noWrite, Err: err}
	}
	logger.Info("Backfilling reservation for established cluster DNS domain", "baseDomain", domain, "clusterUID", cluster.ServiceProviderProperties.ClusterUID)
	return decision{Action: createClaim, Claim: &fleetapi.DNSClaim{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: id.Parent.Name}, BaseDomain: domain, Owner: cluster.ServiceProviderProperties.ClusterUID, ClusterResourceID: cluster.ID.String(), CreatedAt: metav1.NewTime(now)}}
}

func (c *backfillController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusters.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	hc, _, observationErr := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.reads, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	d := decideBackfill(utils.LoggerFromContext(ctx), cluster, hc, observationErr, c.clock.Now(), func(domain string) (*fleetapi.DNSClaim, error) { return c.claims.Get(ctx, domain) })
	return execute(ctx, c.db, c.fleet, d)
}
