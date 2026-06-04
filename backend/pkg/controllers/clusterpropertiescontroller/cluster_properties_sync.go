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
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
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
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*clusterPropertiesSyncer)(nil)

// NewClusterPropertiesSyncController creates a controller that synchronizes
// cluster properties from the HostedCluster ReadDesire mirror to Cosmos DB.
func NewClusterPropertiesSyncController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &clusterPropertiesSyncer{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
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

func (c *clusterPropertiesSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *clusterPropertiesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	hostedCluster, err := maestrohelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HostedCluster from ReadDesire: %w", err))
	}
	if hostedCluster == nil {
		return nil
	}

	originalCluster := existingCluster.DeepCopy()
	domainPrefix := existingCluster.CustomerProperties.DNS.BaseDomainPrefix

	existingCluster.ServiceProviderProperties.Console.URL = fmt.Sprintf("https://console-openshift-console.apps.%s", hostedCluster.Spec.DNS.BaseDomain)
	existingCluster.ServiceProviderProperties.DNS.BaseDomain = strings.TrimPrefix(
		hostedCluster.Spec.KubeAPIServerDNSName,
		fmt.Sprintf("api.%s.", domainPrefix),
	)
	if hostedCluster.Status.ControlPlaneEndpoint.Port != 0 {
		existingCluster.ServiceProviderProperties.API.URL = fmt.Sprintf("https://%s", net.JoinHostPort(hostedCluster.Spec.KubeAPIServerDNSName, strconv.Itoa(int(hostedCluster.Status.ControlPlaneEndpoint.Port))))
	}
	existingCluster.ServiceProviderProperties.Platform.IssuerURL = hostedCluster.Spec.IssuerURL

	if equality.Semantic.DeepEqual(originalCluster, existingCluster) {
		return nil
	}

	if _, err := clusterCRUD.Replace(ctx, existingCluster, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("synced cluster properties")
	return nil
}
