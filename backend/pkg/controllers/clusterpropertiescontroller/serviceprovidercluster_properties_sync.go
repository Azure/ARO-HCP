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

// serviceProviderClusterPropertiesSyncer synchronizes namespace fields on the
// ServiceProviderCluster from the observed HostedCluster ReadDesire content:
//   - Status.HostedClusterNamespace
//   - Status.ControlPlaneNamespace
type serviceProviderClusterPropertiesSyncer struct {
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	resourcesDBClient            database.ResourcesDBClient
	readDesireLister             dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*serviceProviderClusterPropertiesSyncer)(nil)

const ServiceProviderClusterPropertiesSyncControllerName = "ServiceProviderClusterPropertiesSync"

// NewServiceProviderClusterPropertiesSyncController creates a controller that
// synchronizes HostedClusterNamespace and ControlPlaneNamespace from the
// HostedCluster ReadDesire mirror to the ServiceProviderCluster in Cosmos DB.
func NewServiceProviderClusterPropertiesSyncController(
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()

	syncer := &serviceProviderClusterPropertiesSyncer{
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		readDesireLister:             readDesireLister,
	}

	return controllerutils.NewClusterWatchingController(
		ServiceProviderClusterPropertiesSyncControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *serviceProviderClusterPropertiesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existing, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}

	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HostedCluster from ReadDesire: %w", err))
	}
	if hostedCluster == nil {
		return nil
	}

	hostedClusterNamespace := hostedCluster.Namespace
	hostedClusterName := hostedCluster.Name
	if len(hostedClusterNamespace) == 0 || len(hostedClusterName) == 0 {
		return nil
	}

	controlPlaneNamespace := fmt.Sprintf("%s-%s", hostedClusterNamespace, strings.ReplaceAll(hostedClusterName, ".", "-"))

	replacement := existing.DeepCopy()
	replacement.Status.HostedClusterNamespace = hostedClusterNamespace
	replacement.Status.ControlPlaneNamespace = controlPlaneNamespace

	servingCABundle, err := c.resolveServingCABundle(ctx, key)
	if err != nil {
		return err
	}
	if len(servingCABundle) > 0 {
		replacement.Status.ServingCABundle = servingCABundle
	}

	if equality.Semantic.DeepEqual(existing, replacement) {
		return nil
	}

	if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); database.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	logger.Info("synced service provider cluster properties",
		"hostedClusterNamespace", hostedClusterNamespace,
		"controlPlaneNamespace", controlPlaneNamespace,
	)
	return nil
}

const servingCATLSCertKey = "tls.crt"

func (c *serviceProviderClusterPropertiesSyncer) resolveServingCABundle(ctx context.Context, key controllerutils.HCPClusterKey) (string, error) {
	cachedSecret, err := kubeapplierhelpers.GetCachedServingCASecretForCluster(
		ctx, c.readDesireLister,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
	)
	if err != nil {
		return "", utils.TrackError(err)
	}
	if cachedSecret == nil {
		return "", nil
	}
	return string(cachedSecret.Data[servingCATLSCertKey]), nil
}
