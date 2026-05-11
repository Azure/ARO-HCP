// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// createNodePoolScopedReadDesiresSyncer ensures a ReadDesire exists per
// HCPNodePool pointing at the nodepool's Hypershift NodePool object in the
// management cluster. The kube-applier sidecar on the management cluster
// observes the NodePool via that ReadDesire and writes the observed state
// into ReadDesire.Status.KubeContent. The "read+persist" controller mirrors
// that into ManagementClusterContent.
//
// Replaces createNodePoolScopedMaestroReadonlyBundlesSyncer.
type createNodePoolScopedReadDesiresSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients

	clusterServiceClient ocm.ClusterServiceClientSpec

	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.NodePoolSyncer = (*createNodePoolScopedReadDesiresSyncer)(nil)

func NewCreateNodePoolScopedReadDesiresController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	informers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &createNodePoolScopedReadDesiresSyncer{
		cooldownChecker:                     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		activeOperationLister:               activeOperationLister,
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		clusterServiceClient:                clusterServiceClient,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	return controllerutils.NewNodePoolWatchingController(
		"CreateNodePoolScopedReadDesires",
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *createNodePoolScopedReadDesiresSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existingNodePool, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}
	if len(existingNodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil
	}

	csClusterID := existingNodePool.ServiceProviderProperties.ClusterServiceID.ClusterID()
	csClusterHREF := ocm.GenerateAROHCPClusterHREF(csClusterID)
	csClusterInternalID := api.Must(api.NewInternalID(csClusterHREF))

	// Resolve the management cluster via the parent cluster's
	// ServiceProviderCluster. Skip if the placement-sync controller hasn't
	// populated it yet.
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    key.SubscriptionID,
		ResourceGroupName: key.ResourceGroupName,
		HCPClusterName:    key.HCPClusterName,
	}
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterKey.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	csCluster, err := c.clusterServiceClient.GetCluster(ctx, csClusterInternalID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from Cluster Service: %w", err))
	}
	csClusterDomainPrefix := csCluster.DomainPrefix()

	target := nodePoolTarget(c.hostedClusterNamespaceEnvIdentifier, csClusterID, csClusterDomainPrefix, existingNodePool.ID.Name)
	desired := buildReadDesire(
		kubeapplier.ToNodePoolScopedReadDesireResourceIDString(
			key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName,
			readDesireNameReadonlyNodePool,
		),
		mcResourceID,
		target,
	)

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}
	parent := database.ResourceParent{
		SubscriptionID:    key.SubscriptionID,
		ResourceGroupName: key.ResourceGroupName,
		ClusterName:       key.HCPClusterName,
		NodePoolName:      key.HCPNodePoolName,
	}
	crud, err := kaClient.ReadDesires(parent)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}
	return ensureReadDesire(ctx, crud, desired)
}

func (c *createNodePoolScopedReadDesiresSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// readDesireNameReadonlyNodePool is the well-known ReadDesire name the
// backend uses for the NodePool mirror. Lowercase form of the existing
// MaestroBundleInternalName so the downstream ManagementClusterContent
// document path stays stable.
var readDesireNameReadonlyNodePool = strings.ToLower(string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))

// nodePoolTarget builds the ResourceReference that points at the
// nodepool's NodePool object in the management cluster. The naming rules
// (namespace = "ocm-<env>-<csClusterID>", name =
// "<csClusterDomainPrefix>-<csNodePoolID>") match what CS itself uses;
// see createNodePoolScopedMaestroReadonlyBundlesSyncer for the original
// derivation.
func nodePoolTarget(envIdentifier, csClusterID, csClusterDomainPrefix, csNodePoolID string) kubeapplier.ResourceReference {
	return kubeapplier.ResourceReference{
		Group:     hsv1beta1.SchemeGroupVersion.Group,
		Version:   hsv1beta1.SchemeGroupVersion.Version,
		Resource:  "nodepools",
		Namespace: fmt.Sprintf("ocm-%s-%s", envIdentifier, csClusterID),
		Name:      fmt.Sprintf("%s-%s", csClusterDomainPrefix, csNodePoolID),
	}
}
