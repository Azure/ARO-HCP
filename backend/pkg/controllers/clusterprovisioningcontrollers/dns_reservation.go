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
	"net/http"
	"time"

	"github.com/Azure/ARO-HCP/internal/api"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/lru"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type dnsReservationController struct {
	cosmosClient database.DBClient
}

// NewDataDumpController periodically lists all clusters and for each out when the cluster was created and its state.
func NewDNSReservationController(cosmosClient database.DBClient) controllerutils.ClusterSyncer {
	c := &dnsReservationController{
		cosmosClient: cosmosClient,
	}

	return c
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

	serviceProviderCluster, err := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Get(ctx, "default")
	if database.IsResponseError(err, http.StatusNotFound) {
		// create it
		serviceProviderCluster, err = c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Create(
			ctx,
			&api.ServiceProviderCluster{
				CosmosMetadata:              api.CosmosMetadata{},
				ResourceID:                  azcorearm.ResourceID{},
				LoadBalancerResourceID:      nil,
				KubeAPIServerDNSReservation: nil,
			},
			nil)
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create service provider cluster: %w", err))
	}

	if serviceProviderCluster.KubeAPIServerDNSReservation != nil {
		// no work to do
		return nil
	}

	// if we're here, we need to reserve a DNS name.  Just create a random one. if it succeeds, the name is free and use it.
	// if it fails, just return the error and the auto-retry will trigger us again soon.  That handles both the conflict case
	// and a general "it's down" case and we get free reporting.
	dnsReservation, err := c.cosmosClient.DNSReservations(key.SubscriptionID).Create(
		ctx,
		&api.DNSReservation{
			CosmosMetadata: api.CosmosMetadata{},
			ResourceID:     nil,
			MustBindByTime: metav1.Time{},
			OwningCluster:  nil,
		},
		nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to reserve DNS name: %w", err)
	}
	logger.Info("reserved DNS name", "kubeAPIServerDNSName", dnsReservation.ResourceID)

	serviceProviderCluster.KubeAPIServerDNSReservation = dnsReservation.ResourceID
	_, err = c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, serviceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update service provider cluster: %w", err))
	}

	// from here we get choices about granularity. I'd be fine to see this controller go on and create azure stuff.
	// I'd also be find to see another controller create the azure stuff.

	return nil
}
