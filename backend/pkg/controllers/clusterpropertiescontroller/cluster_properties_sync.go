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

package clusterpropertiescontroller

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterPropertiesSyncer synchronizes ServiceProviderProperties from the observed
// HostedCluster ReadDesire content to Cosmos DB, reconciling when values differ:
//   - ServiceProviderProperties.Console.URL
//   - ServiceProviderProperties.DNS.BaseDomain
//   - ServiceProviderProperties.API.URL
//   - ServiceProviderProperties.Platform.IssuerURL
type clusterPropertiesSyncer struct {
	clusterLister     listers.ClusterLister
	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*clusterPropertiesSyncer)(nil)

// NewClusterPropertiesSyncController creates a controller that synchronizes
// cluster properties from the HostedCluster ReadDesire mirror to Cosmos DB.
func NewClusterPropertiesSyncController(
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()

	syncer := &clusterPropertiesSyncer{
		clusterLister:     clusterLister,
		resourcesDBClient: resourcesDBClient,
		readDesireLister:  readDesireLister,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterPropertiesSync",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *clusterPropertiesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if len(existingCluster.CustomerProperties.DNS.BaseDomainPrefix) == 0 {
		return nil
	}

	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HostedCluster from ReadDesire: %w", err))
	}
	if hostedCluster == nil {
		return nil
	}

	replacement := existingCluster.DeepCopy()
	replacement.ServiceProviderProperties.Console.URL = fmt.Sprintf("https://console-openshift-console.apps.%s", hostedCluster.Spec.DNS.BaseDomain)
	kubeAPIServerDNSNamePrefix := fmt.Sprintf("api.%s.", replacement.CustomerProperties.DNS.BaseDomainPrefix)
	// A mismatch should not happen in normal operation; error so any regression is visible.
	if !strings.HasPrefix(hostedCluster.Spec.KubeAPIServerDNSName, kubeAPIServerDNSNamePrefix) {
		return utils.TrackError(fmt.Errorf(
			"failed to derive DNS base domain from kubeAPIServerDNSName %q: does not have expected prefix %q",
			hostedCluster.Spec.KubeAPIServerDNSName,
			kubeAPIServerDNSNamePrefix,
		))
	}
	replacement.ServiceProviderProperties.DNS.BaseDomain = strings.TrimPrefix(
		hostedCluster.Spec.KubeAPIServerDNSName,
		kubeAPIServerDNSNamePrefix,
	)
	if hostedCluster.Status.ControlPlaneEndpoint.Port != 0 {
		replacement.ServiceProviderProperties.API.URL = fmt.Sprintf("https://%s", net.JoinHostPort(hostedCluster.Spec.KubeAPIServerDNSName, strconv.Itoa(int(hostedCluster.Status.ControlPlaneEndpoint.Port))))
	}
	replacement.ServiceProviderProperties.Platform.IssuerURL = hostedCluster.Spec.IssuerURL

	if equality.Semantic.DeepEqual(existingCluster, replacement) {
		return nil
	}

	if _, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Replace(ctx, replacement, nil); database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		// there is no need to report an error since the retry will happen when the reflector sees the update and puts an Update into the informer.
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("synced cluster properties")
	return nil
}
