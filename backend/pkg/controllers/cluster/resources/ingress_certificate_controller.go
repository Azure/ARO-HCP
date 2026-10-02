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

package resources

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const IngressCertificateControllerName = "IngressCertificate"

type ingressCertificateSyncer struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	managementClusterLister      fleetlisters.ManagementClusterLister
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	applyDesireLister            kubeapplierlisters.ApplyDesireLister
	readDesireLister             kubeapplierlisters.ReadDesireLister
	serviceTenantID              string
}

var _ controllerutils.ClusterSyncer = (*ingressCertificateSyncer)(nil)

func NewIngressCertificateController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	managementClusterLister fleetlisters.ManagementClusterLister,
	serviceTenantID string,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()
	return controllerutils.NewClusterWatchingController(
		IngressCertificateControllerName, resourcesDBClient, informers, kubeApplierInformers, 30*time.Second,
		&ingressCertificateSyncer{
			clusterLister:                clusterLister,
			serviceProviderClusterLister: serviceProviderClusterLister,
			managementClusterLister:      managementClusterLister,
			kubeApplierDBClients:         kubeApplierDBClients,
			applyDesireLister:            applyDesireLister,
			readDesireLister:             readDesireLister,
			serviceTenantID:              serviceTenantID,
		},
	)
}

func (syncer *ingressCertificateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := syncer.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	serviceProviderCluster, err := syncer.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp == nil && !syncer.NeedsWork(serviceProviderCluster) {
		return nil
	}
	managementClusterID := serviceProviderCluster.Status.ManagementClusterResourceID
	if managementClusterID == nil {
		return nil
	}
	client := syncer.kubeApplierDBClients.For(ctx, managementClusterID)
	if client == nil {
		return nil
	}
	applyCRUD, err := client.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	readCRUD, err := client.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return syncer.teardown(ctx, applyCRUD, readCRUD)
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}
	if managementClusterID.Parent == nil {
		return utils.TrackError(fmt.Errorf("management cluster resource ID has no parent stamp"))
	}
	managementCluster, err := syncer.managementClusterLister.Get(ctx, managementClusterID.Parent.Name)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if managementCluster.Status.HostedClustersSecretsKeyVaultURL == "" || managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID == "" {
		return utils.TrackError(fmt.Errorf("management cluster is missing hosted clusters secrets Key Vault URL or managed identity client ID"))
	}
	applyDesires, readDesires, err := buildIngressCertificateDesires(
		key, managementCluster, serviceProviderCluster.Status.HostedClusterNamespace,
		cluster.ServiceProviderProperties.ClusterServiceID.ID(), syncer.serviceTenantID, serviceProviderCluster.Status.AzureResources.IngressCertificate.AzureReference,
	)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, desire := range readDesires {
		if err := kubeapplierhelpers.EnsureReadDesire(ctx, readCRUD, syncer.readDesireLister, desire); err != nil {
			return utils.TrackError(err)
		}
	}
	for _, desire := range applyDesires {
		if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyCRUD, syncer.applyDesireLister, desire); err != nil {
			return utils.TrackError(err)
		}
	}
	return nil
}

func (syncer *ingressCertificateSyncer) NeedsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	return serviceProviderCluster.Status.AzureResources.IngressCertificate.AzureReference != (coreapi.AzureTLSCertificateReference{}) && serviceProviderCluster.Status.HostedClusterNamespace != ""
}

func (syncer *ingressCertificateSyncer) teardown(
	ctx context.Context,
	applyCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	readCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
) error {
	for _, name := range []string{ingressSecretProviderClassDesireName, ingressSecretSyncDesireName} {
		if err := applyCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete ingress certificate ApplyDesire %s: %w", name, err))
		}
		if err := readCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete ingress certificate ReadDesire %s: %w", name, err))
		}
	}
	return nil
}
