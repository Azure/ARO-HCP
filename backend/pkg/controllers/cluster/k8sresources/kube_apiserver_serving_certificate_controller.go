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

package k8sresources

import (
	"context"
	"fmt"
	"slices"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
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

const KubeAPIServerServingCertificateControllerName = "KubeAPIServerServingCertificateK8sResources"

type kubeAPIServerServingCertificateSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	managementClusterLister      fleetlisters.ManagementClusterLister
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	applyDesireLister            kubeapplierlisters.ApplyDesireLister
	readDesireLister             kubeapplierlisters.ReadDesireLister
	// serviceTenantID is the Azure AD tenant where the ARO-HCP service's First Party
	// Application (FPA) identity is registered. It is the tenantId used both to
	// retrieve the FPA credential that calls the Managed Identities Data Plane, and
	// as the Secrets Store CSI Driver's authentication tenant when delivering TLS
	// certificates from Key Vault onto a management cluster. It is never a customer's
	// tenant, and permissions granted here are not scoped to any individual
	// customer's HostedCluster or subscription.
	serviceTenantID string
	cloudName       string
}

var _ controllerutils.ClusterSyncer = (*kubeAPIServerServingCertificateSyncer)(nil)

func NewKubeAPIServerServingCertificateController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	managementClusterLister fleetlisters.ManagementClusterLister,
	serviceTenantID, cloudName string,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()
	controller := controllerutils.NewClusterWatchingController(
		KubeAPIServerServingCertificateControllerName, resourcesDBClient, informers, kubeApplierInformers, 30*time.Second,
		&kubeAPIServerServingCertificateSyncer{
			resourcesDBClient:            resourcesDBClient,
			clusterLister:                clusterLister,
			serviceProviderClusterLister: serviceProviderClusterLister,
			managementClusterLister:      managementClusterLister,
			kubeApplierDBClients:         kubeApplierDBClients,
			applyDesireLister:            applyDesireLister,
			readDesireLister:             readDesireLister,
			serviceTenantID:              serviceTenantID,
			cloudName:                    cloudName,
		},
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	for _, name := range []string{KubeAPIServerServingCertSecretProviderClassDesireName, KubeAPIServerServingCertSecretSyncDesireName} {
		controller.AddInterestingResource(kubeapplierapi.ClusterScopedApplyDesireResourceType, name)
		controller.AddInterestingResource(kubeapplierapi.ClusterScopedReadDesireResourceType, name)
	}
	return controller
}

func (syncer *kubeAPIServerServingCertificateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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
	if !syncer.needsWork(cluster, serviceProviderCluster) {
		return nil
	}
	managementClusterID := serviceProviderCluster.Status.ManagementClusterResourceID
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
		return syncer.ensureSecretSyncDesiresDeleted(ctx, applyCRUD, readCRUD)
	}
	return syncer.ensureSecretSyncDesiresCreated(ctx, key, cluster, serviceProviderCluster, managementClusterID, applyCRUD, readCRUD)
}

func (syncer *kubeAPIServerServingCertificateSyncer) ensureSecretSyncDesiresCreated(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	cluster *coreapi.Cluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	managementClusterID *azcorearm.ResourceID,
	applyCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	readCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
) error {
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
	if len(managementCluster.Status.HostedClustersSecretsKeyVaultURL) == 0 || len(managementCluster.Status.HostedClustersSecretsKeyVaultManagedIdentityClientID) == 0 {
		return utils.TrackError(fmt.Errorf("management cluster is missing hosted clusters secrets Key Vault URL or managed identity client ID"))
	}
	applyDesires, readDesires, err := buildKubeAPIServerServingCertificateDesires(
		key, managementCluster, serviceProviderCluster.Status.HostedClusterNamespace,
		syncer.serviceTenantID, syncer.cloudName, serviceProviderCluster.Status.AzureResources.KubeAPIServerCertificate.AzureReference,
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
	// ClusterPropertiesSync supplies the observed base domain, including the DNS
	// slug. Secret delivery can proceed while we wait for that observation.
	if cluster.CustomerProperties.DNS.BaseDomainPrefix == "" || cluster.ServiceProviderProperties.DNS.BaseDomain == "" {
		return nil
	}
	apiHostname := fmt.Sprintf("api.%s.%s", cluster.CustomerProperties.DNS.BaseDomainPrefix, cluster.ServiceProviderProperties.DNS.BaseDomain)
	replacement := serviceProviderCluster.DeepCopy()
	if replacement.Spec.DesiredHostedCluster == nil {
		replacement.Spec.DesiredHostedCluster = &hsv1beta1.HostedCluster{}
	}
	hostedCluster := replacement.Spec.DesiredHostedCluster
	if hostedCluster.Spec.Configuration == nil {
		hostedCluster.Spec.Configuration = &hsv1beta1.ClusterConfiguration{}
	}
	if hostedCluster.Spec.Configuration.APIServer == nil {
		hostedCluster.Spec.Configuration.APIServer = &configv1.APIServerSpec{}
	}
	servingCerts := &hostedCluster.Spec.Configuration.APIServer.ServingCerts
	// Own the service-provided certificate mapping, preserving any other named
	// certificates and unrelated API server configuration.
	servingCerts.NamedCertificates = slices.DeleteFunc(servingCerts.NamedCertificates, func(cert configv1.APIServerNamedServingCert) bool {
		return cert.ServingCertificate.Name == kubeAPIServerServingCertSecretName || slices.Contains(cert.Names, apiHostname)
	})
	servingCerts.NamedCertificates = append(servingCerts.NamedCertificates, configv1.APIServerNamedServingCert{
		Names:              []string{apiHostname},
		ServingCertificate: configv1.SecretNameReference{Name: kubeAPIServerServingCertSecretName},
	})
	return syncer.persistIfChanged(ctx, key, serviceProviderCluster, replacement)
}

func (syncer *kubeAPIServerServingCertificateSyncer) persistIfChanged(ctx context.Context, key controllerutils.HCPClusterKey, existing, replacement *coreapi.ServiceProviderCluster) error {
	if !controllerutil.NeedsUpdate(existing, replacement) {
		return nil
	}
	_, err := syncer.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("replace desired kube-apiserver serving certificate: %w", err))
	}
	return nil
}

func (syncer *kubeAPIServerServingCertificateSyncer) needsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if serviceProviderCluster.Status.ManagementClusterResourceID == nil {
		return false
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return true
	}
	return serviceProviderCluster.Status.AzureResources.KubeAPIServerCertificate != nil && serviceProviderCluster.Status.AzureResources.KubeAPIServerCertificate.AzureReference != nil && serviceProviderCluster.Status.HostedClusterNamespace != ""
}

// ensureSecretSyncDesiresDeleted ensures that the secret sync desires for the kube-apiserver serving certificate are purged from the database.
// No need to ensure that they are removed from the management cluster since this is done in cascade when the
// hosted cluster namespace is deleted
func (syncer *kubeAPIServerServingCertificateSyncer) ensureSecretSyncDesiresDeleted(
	ctx context.Context,
	applyCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire],
	readCRUD cosmosstorageutils.ResourceCRUD[kubeapplierapi.ReadDesire, *kubeapplierapi.ReadDesire],
) error {
	for _, name := range []string{KubeAPIServerServingCertSecretProviderClassDesireName, KubeAPIServerServingCertSecretSyncDesireName} {
		if err := applyCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete kube-apiserver serving certificate ApplyDesire %s: %w", name, err))
		}
		if err := readCRUD.Delete(ctx, name); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("delete kube-apiserver serving certificate ReadDesire %s: %w", name, err))
		}
	}
	return nil
}
