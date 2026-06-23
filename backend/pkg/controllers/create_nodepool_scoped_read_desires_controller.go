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
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// createNodePoolScopedReadDesiresSyncer ensures a ReadDesire exists per
// HCPNodePool pointing at the nodepool's Hypershift NodePool object in the
// management cluster. The kube-applier sidecar on the management cluster
// observes the NodePool via that ReadDesire and writes the observed state
// into ReadDesire.Status.KubeContent; consumers read it directly from there.
//
// Replaces createNodePoolScopedMaestroReadonlyBundlesSyncer.
type createNodePoolScopedReadDesiresSyncer struct {
	activeOperationLister listers.ActiveOperationLister

	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister

	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.NodePoolSyncer = (*createNodePoolScopedReadDesiresSyncer)(nil)

func NewCreateNodePoolScopedReadDesiresController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	serviceProviderClusterLister listers.ServiceProviderClusterLister,
	informers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &createNodePoolScopedReadDesiresSyncer{
		activeOperationLister:               activeOperationLister,
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		serviceProviderClusterLister:        serviceProviderClusterLister,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	return controllerutils.NewNodePoolWatchingController(
		"CreateNodePoolScopedReadDesires",
		resourcesDBClient,
		informers,
		nil, // do not fire on ReadDesires this controller itself creates
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
	if existingNodePool.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}
	if existingNodePool.ServiceProviderProperties.ClusterServiceID == nil ||
		len(existingNodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil
	}

	// Resolve the management cluster via the parent cluster's
	// ServiceProviderCluster. Skip if the placement-sync controller hasn't
	// populated it yet.
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it. NodePoolWatchingController
		// does not watch the ServiceProviderCluster informer (an SPC arrival
		// can't be walked down to a specific node pool), so the next attempt
		// happens on the controller's periodic resync or the next NodePool /
		// ServiceProviderNodePool event for this node pool.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	// Pull the parent cluster's domain prefix from cosmos rather than Cluster
	// Service: the cluster_base_domain_prefix_sync controller already mirrors CS
	// DomainPrefix into CustomerProperties.DNS.BaseDomainPrefix on the parent
	// HCPCluster. Skip until that sync has happened; we'll retrigger on relist.
	parentCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get parent Cluster: %w", err))
	}
	csClusterDomainPrefix := parentCluster.CustomerProperties.DNS.BaseDomainPrefix
	if len(csClusterDomainPrefix) == 0 {
		return nil
	}
	csClusterID := existingNodePool.ServiceProviderProperties.ClusterServiceID.ClusterID()

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
	crud, err := kaClient.ReadDesiresForNodePool(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}
	existing, err := getExistingReadDesire(ctx, crud, desired.ResourceID.Name)
	if err != nil {
		return err
	}
	if !readDesireNeedsWork(existing, desired) {
		return nil
	}
	if existing == nil {
		if _, err := crud.Create(ctx, desired, nil); err != nil {
			return utils.TrackError(fmt.Errorf("create ReadDesire: %w", err))
		}
		return nil
	}
	replacement := existing.DeepCopy()
	replacement.Spec = *desired.Spec.DeepCopy()
	if _, err := crud.Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(fmt.Errorf("replace ReadDesire: %w", err))
	}
	return nil
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
		Namespace: HostedClusterNamespace(envIdentifier, csClusterID),
		Name:      fmt.Sprintf("%s-%s", csClusterDomainPrefix, csNodePoolID),
	}
}
