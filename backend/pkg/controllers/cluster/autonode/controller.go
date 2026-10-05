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

// Package autonode enables AutoNode on clusters that requested it at creation.
// The controller waits for the HostedCluster, then uses an ApplyDesire to
// apply spec.autoNode. It reconciles the desire to correct drift and purges it
// during cluster deletion so it cannot block cleanup.
package autonode

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const AutoNodeEnablerControllerName = "AutoNodeEnabler"

type autoNodeSyncer struct {
	clusterLister                       corelisters.ClusterLister
	serviceProviderClusterLister        corelisters.ServiceProviderClusterLister
	readDesireLister                    kubeapplierlisters.ReadDesireLister
	applyDesireLister                   kubeapplierlisters.ApplyDesireLister
	kubeApplierDBClients                kubeappliercosmosstorage.KubeApplierDBClients
	hostedClusterNamespaceEnvIdentifier string
	imageOverrides                      ImageOverrides
}

var _ controllerutils.ClusterSyncer = (*autoNodeSyncer)(nil)

func NewAutoNodeEnablerController(
	cosmosClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	hostedClusterNamespaceEnvIdentifier string,
	imageOverrides ImageOverrides,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, readDesireLister := kubeApplierInformers.ReadDesires()
	applyDesireInformer, applyDesireLister := kubeApplierInformers.ApplyDesires()

	syncer := &autoNodeSyncer{
		clusterLister:                       clusterLister,
		serviceProviderClusterLister:        serviceProviderClusterLister,
		readDesireLister:                    readDesireLister,
		applyDesireLister:                   applyDesireLister,
		kubeApplierDBClients:                kubeApplierDBClients,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
		imageOverrides:                      imageOverrides,
	}

	controller := controllerutils.NewClusterWatchingController(
		AutoNodeEnablerControllerName,
		cosmosClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)

	// Reconcile ApplyDesire changes immediately instead of waiting for resync.
	if err := controller.QueueForInformers(5*time.Minute, applyDesireInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *autoNodeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached Cluster: %w", err))
	}

	serviceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached ServiceProviderCluster: %w", err))
	}

	// Clean up first because fields required below may already be gone during
	// deletion. A leftover ApplyDesire would block cluster deletion.
	if needsDeletionWork(*existingCluster, *serviceProviderCluster) {
		return s.syncDeletion(ctx, key, serviceProviderCluster)
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		// No deletion work means no desire could have been delivered.
		return nil
	}

	// Existing clusters are unaffected unless they requested AutoNode at creation.
	autoNodeRequested := existingCluster.ServiceProviderProperties.ExperimentalFeatures.AutoNode == coreapi.AutoNode
	if !autoNodeRequested {
		return nil
	}

	// Use the cluster's federated AutoNode identity as the source of truth.
	clientID, ok := autoNodeKarpenterClientID(existingCluster, serviceProviderCluster)
	if !ok {
		// Azure AutoNode requires this ID. Wait instead of applying an invalid configuration.
		logger.Info("AutoNode enabled but no Karpenter Azure client ID resolved yet; waiting")
		return nil
	}

	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}
	csClusterID := existingCluster.ServiceProviderProperties.ClusterServiceID.ID()

	csClusterDomainPrefix := existingCluster.CustomerProperties.DNS.BaseDomainPrefix
	if len(csClusterDomainPrefix) == 0 {
		return nil
	}

	// Wait until the HostedCluster has been observed. A nil result means it is
	// not ready yet, whether the ReadDesire is absent or still unobserved.
	hostedCluster, _, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(
		ctx, s.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cached HostedCluster: %w", err))
	}
	if hostedCluster == nil {
		return nil
	}

	kubeApplierClient := s.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		// The management cluster registry may still be catching up.
		return nil
	}
	applyDesireCRUD, err := kubeApplierClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}

	// Hypershift propagates spec.autoNode on every reconcile, so applying it
	// after creation is supported. Cluster Service already sets the Azure
	// subnet and resource group required by Karpenter.
	target := controllerutils.HostedClusterTarget(s.hostedClusterNamespaceEnvIdentifier, csClusterID, csClusterDomainPrefix)
	desire, err := buildAutoNodeApplyDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, mcResourceID, target, clientID, s.imageOverrides)
	if err != nil {
		return err
	}

	if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyDesireCRUD, s.applyDesireLister, desire); err != nil {
		return err
	}

	return nil
}

// needsDeletionWork reports whether an AutoNode ApplyDesire may need cleanup.
func needsDeletionWork(existingCluster coreapi.Cluster, serviceProviderCluster coreapi.ServiceProviderCluster) bool {
	if existingCluster.ServiceProviderProperties.DeletionTimestamp == nil {
		return false
	}
	if existingCluster.ServiceProviderProperties.ExperimentalFeatures.AutoNode != coreapi.AutoNode {
		return false
	}
	return serviceProviderCluster.Status.ManagementClusterResourceID != nil
}

// syncDeletion purges the AutoNode ApplyDesire. Marking it for deletion would
// make kube-applier delete the HostedCluster, which is owned by Cluster Service.
func (s *autoNodeSyncer) syncDeletion(ctx context.Context, key controllerutils.HCPClusterKey, serviceProviderCluster *coreapi.ServiceProviderCluster) error {
	kubeApplierClient := s.kubeApplierDBClients.For(ctx, serviceProviderCluster.Status.ManagementClusterResourceID)
	if kubeApplierClient == nil {
		// The management cluster registry may still be catching up.
		return nil
	}
	applyDesireCRUD, err := kubeApplierClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}

	if err := kubeapplierhelpers.PurgeApplyDesire(ctx, autoNodeApplyDesireName, applyDesireCRUD); err != nil {
		return err
	}
	utils.LoggerFromContext(ctx).Info("ensured AutoNode ApplyDesire document is absent for deleting cluster", "desire", autoNodeApplyDesireName)
	return nil
}

// autoNodeKarpenterClientID returns the resolved Azure client ID for the
// cluster's AutoNode identity. The bool is false while the identity is
// missing, unresolved, or in an error state.
func autoNodeKarpenterClientID(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) (string, bool) {
	identityResourceID, ok := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[string(azure.ClusterOperatorIdentifierAutoNode)]
	if !ok || identityResourceID == nil {
		return "", false
	}
	key := strings.ToLower(identityResourceID.String())
	identity, ok := serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities[key]
	if !ok || identity == nil || identity.RetrievalError != nil || identity.ClientID == nil || len(*identity.ClientID) == 0 {
		return "", false
	}
	return *identity.ClientID, true
}
