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
// The controller applies HostedCluster configuration and an optional HCP image
// annotation through separate desires. It purges both during cluster deletion.
package autonode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
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

	kubeApplierClient, err := s.kubeApplierDBClients.For(ctx, mcResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get kube-applier client for management cluster %s: %w", mcResourceID, err))
	}
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
	if clientID, ok := autoNodeKarpenterClientID(existingCluster, serviceProviderCluster); ok {
		target := controllerutils.HostedClusterTarget(s.hostedClusterNamespaceEnvIdentifier, csClusterID, csClusterDomainPrefix)
		desire, err := buildAutoNodeApplyDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, mcResourceID, target, clientID, s.imageOverrides)
		if err != nil {
			return err
		}
		if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyDesireCRUD, s.applyDesireLister, desire); err != nil {
			return err
		}
	} else {
		logger.Info("AutoNode enabled but no Karpenter Azure client ID resolved yet; waiting")
	}

	target := kubeapplierapi.ResourceReference{
		Group: "hypershift.openshift.io", Version: "v1beta1",
		Resource: "hostedcontrolplanes", Namespace: serviceProviderCluster.Status.ControlPlaneNamespace, Name: hostedCluster.Name,
	}
	if s.imageOverrides.KarpenterProviderAzureImage == "" {
		existing, err := applyDesireCRUD.Get(ctx, strings.ToLower(autoNodeOperandImageApplyDesireName))
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("get HCP operand image desire: %w", err))
		}
		if existing.Tags[kubeapplierapi.TagControllerName] != AutoNodeEnablerControllerName {
			return utils.TrackError(fmt.Errorf("HCP operand image desire has an unexpected controller owner"))
		}
		if (target.Namespace != "" && existing.Spec.TargetItem.Namespace != target.Namespace) ||
			(target.Name != "" && existing.Spec.TargetItem.Name != target.Name) {
			return utils.TrackError(fmt.Errorf("HCP operand image desire has an unexpected target"))
		}
		// Keep the stored target when status fields are no longer available.
		target = existing.Spec.TargetItem
	} else if target.Namespace == "" || target.Name == "" {
		return nil
	}
	desire, err := buildAutoNodeOperandImageApplyDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		mcResourceID, target, s.imageOverrides.KarpenterProviderAzureImage)
	if err != nil {
		return err
	}
	return kubeapplierhelpers.EnsureApplyDesire(ctx, applyDesireCRUD, s.applyDesireLister, desire)
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

// syncDeletion purges both desires without deleting their foreign-owned targets.
func (s *autoNodeSyncer) syncDeletion(ctx context.Context, key controllerutils.HCPClusterKey, serviceProviderCluster *coreapi.ServiceProviderCluster) error {
	kubeApplierClient, err := s.kubeApplierDBClients.For(ctx, serviceProviderCluster.Status.ManagementClusterResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get kube-applier client for management cluster %s: %w", serviceProviderCluster.Status.ManagementClusterResourceID, err))
	}
	if kubeApplierClient == nil {
		// The management cluster registry may still be catching up.
		return nil
	}
	applyDesireCRUD, err := kubeApplierClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}

	var errs []error
	for _, name := range []string{autoNodeApplyDesireName, autoNodeOperandImageApplyDesireName} {
		if err := kubeapplierhelpers.PurgeApplyDesire(ctx, name, applyDesireCRUD); err != nil {
			errs = append(errs, err)
			continue
		}
		utils.LoggerFromContext(ctx).Info("ensured AutoNode desire is absent for deleting cluster", "desire", name)
	}
	return errors.Join(errs...)
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
