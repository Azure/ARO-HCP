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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// caBundleSyncSyncer reads the serving CA ReadDesire, extracts the
// CA bundle PEM from the mirrored Secret, and writes it onto
// ServiceProviderClusterStatus.ServingCABundle.
type caBundleSyncSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*caBundleSyncSyncer)(nil)

// NewCABundleSyncController wires the serving CA bundle sync
// controller.
func NewCABundleSyncController(
	resourcesDBClient database.ResourcesDBClient,
	readDesireLister dblisters.ReadDesireLister,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &caBundleSyncSyncer{
		cooldownChecker:   controllerutil.NewTimeBasedCooldownChecker(30 * time.Second),
		resourcesDBClient: resourcesDBClient,
		readDesireLister:  readDesireLister,
	}

	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialCABundleSync",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *caBundleSyncSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *caBundleSyncSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	// Read the serving CA ReadDesire
	readDesire, err := c.readDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, systemadmincredhelpers.ReadDesireNameServingCA)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get serving CA ReadDesire: %w", err))
	}

	if readDesire.Status.KubeContent == nil || len(readDesire.Status.KubeContent.Raw) == 0 {
		return nil
	}

	// Parse the mirrored Secret
	secret := &corev1.Secret{}
	if err := json.Unmarshal(readDesire.Status.KubeContent.Raw, secret); err != nil {
		logger.Error(err, "failed to unmarshal serving CA Secret from ReadDesire")
		return nil // don't crash or rewrite on malformed data
	}

	// Extract the CA bundle. HyperShift stores the serving CA in
	// different keys depending on version. Try common key names.
	var caBundle string
	for _, key := range []string{"ca.crt", "tls.crt", "ca-bundle.crt"} {
		if data, ok := secret.Data[key]; ok && len(data) > 0 {
			caBundle = string(data)
			break
		}
	}
	if len(caBundle) == 0 {
		logger.Info("serving CA Secret has no recognized CA key")
		return nil
	}

	// Write to ServiceProviderCluster if changed
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	if spc.Status.ServingCABundle == caBundle {
		return nil
	}

	logger.Info("updating serving CA bundle on ServiceProviderCluster")
	replacement := spc.DeepCopy()
	replacement.Status.ServingCABundle = caBundle
	spcCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if _, err := spcCRUD.Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}
