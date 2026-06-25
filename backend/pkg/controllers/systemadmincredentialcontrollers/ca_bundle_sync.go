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
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ServingCAReadDesireName is the well-known ReadDesire name controller
// #10 (ServingCAReadDesireCreator) writes for the per-cluster
// kube-apiserver serving CA mirror. The Secret it points at lives in
// the cluster's HCP namespace.
const ServingCAReadDesireName = "readonlyhypershiftservingca"

// servingCASecretCABundleKey is the data key inside the mirrored
// kube-apiserver serving CA Secret. HyperShift writes the PEM bundle
// under this key.
const servingCASecretCABundleKey = "ca.crt"

// caBundleSync is controller #8. Watches the per-cluster CA ReadDesire,
// extracts the CA bytes from the mirrored Secret, writes them onto
// ServiceProviderCluster.Status.ServingCABundle. The frontend's
// OperationResult handler reads that field when assembling the admin
// kubeconfig.
type caBundleSync struct {
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient
	clusterLister     listers.ClusterLister
	readDesireLister  dblisters.ReadDesireLister
}

func NewCABundleSyncController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	clusterLister listers.ClusterLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &caBundleSync{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient: resourcesDBClient,
		clusterLister:     clusterLister,
		readDesireLister:  readDesireLister,
	}
	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialCABundleSync",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *caBundleSync) CooldownChecker() controllerutil.CooldownChecker { return c.cooldownChecker }

func (c *caBundleSync) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	if _, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName); err != nil {
		if database.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(fmt.Errorf("get cluster: %w", err))
	}

	clusterRID, err := api.ToClusterResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("derive cluster RID: %w", err))
	}
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterRID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get/create SPC: %w", err))
	}

	rd, err := c.readDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, ServingCAReadDesireName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get CA ReadDesire: %w", err))
	}
	if rd.Status.KubeContent == nil || len(rd.Status.KubeContent.Raw) == 0 {
		return nil
	}

	secret := &corev1.Secret{}
	if err := json.Unmarshal(rd.Status.KubeContent.Raw, secret); err != nil {
		return utils.TrackError(fmt.Errorf("unmarshal serving CA Secret: %w", err))
	}
	bundle := secret.Data[servingCASecretCABundleKey]
	if len(bundle) == 0 {
		logger.Info("serving CA Secret has no ca.crt; skipping")
		return nil
	}

	if spc.Status.ServingCABundle == string(bundle) {
		return nil
	}
	replacement := spc.DeepCopy()
	replacement.Status.ServingCABundle = string(bundle)
	if _, err := c.resourcesDBClient.ServiceProviderClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name).Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(fmt.Errorf("persist ServingCABundle: %w", err))
	}
	logger.Info("updated ServiceProviderCluster ServingCABundle from mirrored Secret")
	return nil
}
