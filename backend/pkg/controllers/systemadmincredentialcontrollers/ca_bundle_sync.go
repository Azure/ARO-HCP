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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"time"

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

// servingCABundleDataKey is the key in the kube-apiserver-server-ca Secret
// that holds the PEM-encoded CA bundle.
const servingCABundleDataKey = "ca.crt"

type caBundleSync struct {
	cooldownChecker              controllerutil.CooldownChecker
	resourcesDBClient            database.ResourcesDBClient
	readDesireLister             dblisters.ReadDesireLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.CredentialRequestSyncer = (*caBundleSync)(nil)

// NewCABundleSyncController returns a CredentialRequestWatchingController that
// watches the serving CA ReadDesire (created by controller #10) and writes the
// CA bundle bytes onto ServiceProviderClusterStatus.ServingCABundle. It fires
// on credential request events so the CA bundle is synced as soon as any
// credential request appears for the cluster.
func NewCABundleSyncController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &caBundleSync{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:            resourcesDBClient,
		readDesireLister:             readDesireLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewCredentialRequestWatchingController(
		"SystemAdminCredentialCABundleSync",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *caBundleSync) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *caBundleSync) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Read the serving CA Secret from the ReadDesire cache.
	cachedSecret, err := maestrohelpers.GetCachedServingCASecretForCluster(
		ctx, c.readDesireLister,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
	)
	if err != nil {
		return utils.TrackError(err)
	}
	if cachedSecret == nil {
		// ReadDesire not yet created or kube-applier has not observed it yet.
		return nil
	}

	// Extract the CA bundle from the Secret data.
	caBundle, ok := cachedSecret.Data[servingCABundleDataKey]
	if !ok || len(caBundle) == 0 {
		logger.Info("serving CA Secret does not contain expected key", "key", servingCABundleDataKey)
		return nil
	}
	caBundlePEM := string(caBundle)

	// Read the current ServiceProviderCluster and check if the CA bundle
	// needs updating.
	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	if spc.Status.ServingCABundle == caBundlePEM {
		// No change needed.
		return nil
	}

	// Update the CA bundle.
	replacement := spc.DeepCopy()
	replacement.Status.ServingCABundle = caBundlePEM
	if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); err != nil {
		if database.IsPreconditionFailedError(err) {
			// Will be retriggered by the informer.
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to update ServingCABundle on ServiceProviderCluster: %w", err))
	}

	logger.Info("updated ServingCABundle on ServiceProviderCluster")
	return nil
}
