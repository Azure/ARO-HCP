// Copyright 2025 Microsoft Corporation
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

package clusterprovisioningcontrollers

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/tzvatot/go-clean-lang/pkg/cleanlang"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dnsReservationController struct {
	clock           utilsclock.PassiveClock
	cooldownChecker controllerutils.CooldownChecker
	cosmosClient    database.DBClient

	rand                   *rand.Rand
	cleanLanguageValidator cleanlang.Validator
}

// NewDataDumpController periodically lists all clusters and for each out when the cluster was created and its state.
func NewDNSReservationController(activeOperationLister listers.ActiveOperationLister, cosmosClient database.DBClient, informers informers.BackendInformers) controllerutils.Controller {
	syncer := &dnsReservationController{
		clock:                  utilsclock.RealClock{},
		cooldownChecker:        controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:           cosmosClient,
		rand:                   rand.New(rand.NewSource(time.Now().UnixNano())),
		cleanLanguageValidator: cleanlang.NewValidator(),
	}

	controller := controllerutils.NewClusterWatchingController(
		"DNSReservationController",
		cosmosClient,
		informers,
		10*time.Minute,
		syncer,
	)

	return controller
}

const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

func (c *dnsReservationController) randomDNSPart() string {
	for {
		b := make([]byte, 4)
		for i := range b {
			b[i] = charset[c.rand.Intn(len(charset))] //
		}
		ret := string(b)
		if c.cleanLanguageValidator.IsClean(ret) {
			return ret
		}
	}
}

func (c *dnsReservationController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	customerDesiredCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HCP cluster: %w", err))
	}

	if len(customerDesiredCluster.CustomerProperties.DNS.BaseDomainPrefix) == 0 {
		// nothing to do yet
		return nil
	}

	serviceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create service provider cluster: %w", err))
	}
	if serviceProviderCluster.Status.KubeAPIServerDNSReservation != nil {
		// no work to do
		return nil
	}

	dnsName := customerDesiredCluster.CustomerProperties.DNS.BaseDomainPrefix + "." + c.randomDNSPart()
	dnsReservationResourceID, err := api.ToDNSReservationResourceID(customerDesiredCluster.GetCosmosData().ResourceID.SubscriptionID, dnsName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create DNS reservation resource ID: %w", err))
	}
	// if we're here, we need to reserve a DNS name.  Just create a random one. if it succeeds, the name is free and use it.
	// if it fails, just return the error and the auto-retry will trigger us again soon.  That handles both the conflict case
	// and a general "it's down" case and we get free reporting.
	dnsReservation, err := c.cosmosClient.DNSReservations(key.SubscriptionID).Create(
		ctx,
		&api.DNSReservation{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: dnsReservationResourceID,
			},
			ResourceID:     dnsReservationResourceID,
			MustBindByTime: &metav1.Time{Time: c.clock.Now().Add(61 * time.Minute)},
			OwningCluster:  customerDesiredCluster.GetCosmosData().ResourceID,
			BindingState:   api.BindingStatePending,
		},
		nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to reserve DNS name: %w", err))
	}
	logger.Info("reserved DNS name", "kubeAPIServerDNSName", dnsReservation.ResourceID)

	serviceProviderCluster.Status.KubeAPIServerDNSReservation = dnsReservation.ResourceID
	_, err = c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, serviceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update service provider cluster: %w", err))
	}

	// from here we get choices about granularity. I'd be fine to see this controller go on and create azure stuff.
	// I'd also be find to see another controller create the azure stuff.
	logger.Info("created DNS reservation", "dnsName", dnsReservation.ResourceID)

	// Best effort marking of binding state.  Another controller will try again if this fails since the binding is complete
	dnsReservation.BindingState = api.BindingStateBound
	dnsReservation.MustBindByTime = nil
	if _, err := c.cosmosClient.DNSReservations(key.SubscriptionID).Replace(ctx, dnsReservation, nil); err != nil {
		logger.Error(err, "failed to mark DNS reservation as bound", "kubeAPIServerDNSName", dnsReservation.ResourceID)
	}

	return nil
}

func (c *dnsReservationController) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
