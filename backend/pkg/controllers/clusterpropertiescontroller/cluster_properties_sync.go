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
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterPropertiesSyncer is a Cluster syncer that synchronizes cluster properties
// to Cosmos DB. CustomerProperties.DNS.BaseDomainPrefix is synced from Cluster Service
// first so downstream ReadDesires can target the HostedCluster on the management cluster.
// The remaining fields are synced from the observed HostedCluster ReadDesire content:
//   - ServiceProviderProperties.Console.URL
//   - ServiceProviderProperties.DNS.BaseDomain
//   - ServiceProviderProperties.API.URL
//   - ServiceProviderProperties.Platform.IssuerURL
type clusterPropertiesSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	readDesireLister     dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*clusterPropertiesSyncer)(nil)

// NewClusterPropertiesSyncController creates a new controller that synchronizes
// cluster properties from Cluster Service and HostedCluster to Cosmos DB.
// It periodically checks each cluster and populates the Console.URL, DNS.BaseDomain,
// API.URL, and Platform.IssuerURL fields from CS and HostedCluster and updates Cosmos.
func NewClusterPropertiesSyncController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &clusterPropertiesSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
		readDesireLister:     readDesireLister,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ClusterPropertiesSync",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute, // Check every 5 minutes
		syncer,
	)

	return controller
}

func (c *clusterPropertiesSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce performs a single reconciliation of cluster properties.
// It checks if the Console.URL, DNS.BaseDomain, API.URL,
// Platform.IssuerURL, or DNS.BaseDomainPrefix fields are unset, and if so, fetches the
// values from CS and HostedCluster and updates Cosmos.
func (c *clusterPropertiesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Get the cluster from Cosmos
	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// Check if any of the properties need to be synced
	needsConsoleURL := len(existingCluster.ServiceProviderProperties.Console.URL) == 0
	needsBaseDomain := len(existingCluster.ServiceProviderProperties.DNS.BaseDomain) == 0
	needsAPIURL := len(existingCluster.ServiceProviderProperties.API.URL) == 0
	needsIssuerURL := len(existingCluster.ServiceProviderProperties.Platform.IssuerURL) == 0
	needsBaseDomainPrefix := len(existingCluster.CustomerProperties.DNS.BaseDomainPrefix) == 0

	if !needsConsoleURL && !needsBaseDomain && !needsBaseDomainPrefix && !needsAPIURL && !needsIssuerURL {
		return nil
	}

	originalCluster := existingCluster.DeepCopy()

	// Sync domain prefix from CS first; ReadDesire targeting needs it before HostedCluster content is observed.
	if needsBaseDomainPrefix {
		// Check if we have a cluster service ID to query
		if existingCluster.ServiceProviderProperties.ClusterServiceID == nil ||
			len(existingCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
			return nil
		}
		csCluster, err := c.clusterServiceClient.GetCluster(ctx, *existingCluster.ServiceProviderProperties.ClusterServiceID)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
		}
		existingCluster.CustomerProperties.DNS.BaseDomainPrefix = csCluster.DomainPrefix()
	}

	hostedCluster, err := maestrohelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HostedCluster from ReadDesire: %w", err))
	}
	if hostedCluster != nil {
		domainPrefix := existingCluster.CustomerProperties.DNS.BaseDomainPrefix
		if needsConsoleURL {
			existingCluster.ServiceProviderProperties.Console.URL = fmt.Sprintf("https://console-openshift-console.apps.%s", hostedCluster.Spec.DNS.BaseDomain)
		}
		if needsBaseDomain {
			existingCluster.ServiceProviderProperties.DNS.BaseDomain = strings.TrimPrefix(
				hostedCluster.Spec.KubeAPIServerDNSName,
				fmt.Sprintf("api.%s.", domainPrefix),
			)
		}
		if needsAPIURL {
			existingCluster.ServiceProviderProperties.API.URL = fmt.Sprintf("https://%s", net.JoinHostPort(hostedCluster.Spec.KubeAPIServerDNSName, strconv.Itoa(int(hostedCluster.Status.ControlPlaneEndpoint.Port))))
		}
		if needsIssuerURL {
			existingCluster.ServiceProviderProperties.Platform.IssuerURL = hostedCluster.Spec.IssuerURL
		}
	}

	// Only write back if something actually changed
	if equality.Semantic.DeepEqual(originalCluster, existingCluster) {
		return nil
	}

	// Write the updated cluster back to Cosmos
	if _, err := clusterCRUD.Replace(ctx, existingCluster, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("synced cluster properties")
	return nil
}
