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
	"time"

	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	internalcontroller "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const CleanupControllerName = "DNSClaimCleanup"
const deletedClaimRetention = 7 * 24 * time.Hour

type claimKey struct {
	BaseDomain string
}

func (k claimKey) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("baseDomain", k.BaseDomain)
}

type cleanupController struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	fleetDBClient     fleetcosmosstorage.FleetDBClient
	dnsClaimLister    fleetlisters.DNSClaimLister
	clusterLister     corelisters.ClusterLister
	clock             clock.PassiveClock
}

// NewCleanupController retains names for a week after confirming owner disappearance.
func NewCleanupController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	fleetInformers fleetinformers.FleetInformers,
	backendInformers coreinformers.BackendInformers,
	clock clock.PassiveClock,
) controllerutils.Controller {
	claimInformer, dnsClaimLister := fleetInformers.DNSClaims()
	_, clusterLister := backendInformers.Clusters()
	syncer := &cleanupController{
		resourcesDBClient: resourcesDBClient,
		fleetDBClient:     fleetDBClient,
		dnsClaimLister:    dnsClaimLister,
		clusterLister:     clusterLister,
		clock:             clock,
	}
	controller := internalcontroller.NewGenericWatchingController(CleanupControllerName, fleetapi.DNSClaimResourceType, syncer, controllerutils.ReconcileTotal)
	controller.AddInterestingResource(fleetapi.DNSClaimResourceType, "*")
	utilruntime.Must(controller.QueueForInformersWithMaxDepth(time.Hour, 0, claimInformer))
	return controller
}

func (c *cleanupController) MakeKey(id *azcorearm.ResourceID) claimKey {
	return claimKey{BaseDomain: id.Name + "." + id.Parent.Name}
}

// CooldownChecker satisfies GenericSyncer; the informer resync drives delayed cleanup.
func (c *cleanupController) CooldownChecker() internalcontroller.CooldownChecker {
	return nil
}

func (c *cleanupController) needsWork(claim *fleetapi.DNSClaim) bool {
	return claim.DeletionTimestamp == nil || !c.clock.Now().Before(claim.DeletionTimestamp.Add(deletedClaimRetention))
}

func (c *cleanupController) SyncOnce(ctx context.Context, key claimKey) error {
	logger := utils.LoggerFromContext(ctx)
	claim, err := c.dnsClaimLister.Get(ctx, key.BaseDomain)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !c.needsWork(claim) {
		return nil
	}
	ownerID := claim.ClusterResourceID
	if ownerID == nil {
		return utils.TrackError(fmt.Errorf("DNS claim has no owning cluster resource ID"))
	}
	cluster, err := c.clusterLister.Get(ctx, ownerID.SubscriptionID, ownerID.ResourceGroupName, ownerID.Name)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(err)
	}
	if cluster != nil && cluster.ServiceProviderProperties.ClusterUID == claim.ClusterUUID {
		return nil
	}
	// A stale cache cannot establish orphanhood. Confirm the owning incarnation
	// is gone before starting the hold and again before the eventual deletion.
	cluster, err = c.resourcesDBClient.HCPClusters(ownerID.SubscriptionID, ownerID.ResourceGroupName).Get(ctx, ownerID.Name)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(err)
	}
	if cluster != nil && cluster.ServiceProviderProperties.ClusterUID == claim.ClusterUUID {
		return nil
	}
	if claim.DeletionTimestamp == nil {
		replacement := claim.DeepCopy()
		now := metav1.NewTime(c.clock.Now())
		replacement.DeletionTimestamp = &now
		logger.Info("DNS owner is gone; retaining reservation for seven days", "baseDomain", claim.BaseDomain, "clusterUID", claim.ClusterUUID)
		_, err = c.fleetDBClient.DNSClaims(claim.ResourceID.Parent.Name).Replace(ctx, replacement, nil)
	} else {
		logger.Info("DNS reservation retention expired; releasing name", "baseDomain", claim.BaseDomain, "clusterUID", claim.ClusterUUID)
		err = c.fleetDBClient.DNSClaims(claim.ResourceID.Parent.Name).DeleteWithETag(ctx, claim.ResourceID.Name, claim.CosmosETag)
	}
	// Another reconciliation changed this claim. Its event or the periodic resync
	// will reconsider it; never replace the observed ETag with a newer owner's.
	if cosmosstorageutils.IsPreconditionFailedError(err) || cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	return utils.TrackError(err)
}
