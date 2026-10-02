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

package azureresources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const TLSCertificatesControllerName = "TLSCertificates"

type tlsCertificatesSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	managementClusterLister      fleetlisters.ManagementClusterLister
	observe                      func(context.Context, string, string) (bool, error)
}

var _ controllerutils.ClusterSyncer = (*tlsCertificatesSyncer)(nil)

func NewTLSCertificatesController(resourcesDBClient corecosmosstorage.ResourcesDBClient, informers coreinformers.BackendInformers, kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers, managementClusterLister fleetlisters.ManagementClusterLister, clients *azureclient.BackendIdentityAzureClients) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	return controllerutils.NewClusterWatchingController(TLSCertificatesControllerName, resourcesDBClient, informers, kubeApplierInformers, 30*time.Second, &tlsCertificatesSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		managementClusterLister:      managementClusterLister,
		observe: func(ctx context.Context, vaultURL, name string) (bool, error) {
			client, err := clients.CertificatesClient(vaultURL)
			if err != nil {
				return false, err
			}
			return observeTLSCertificate(ctx, client, name)
		},
	})
}

type tlsCertificatesClient interface {
	GetCertificate(context.Context, string, string, *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error)
	GetCertificateOperation(context.Context, string, *azcertificates.GetCertificateOperationOptions) (azcertificates.GetCertificateOperationResponse, error)
}

func observeTLSCertificate(ctx context.Context, client tlsCertificatesClient, name string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := client.GetCertificate(ctx, name, "", nil)
	var responseError *azcore.ResponseError
	if errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	operation, err := client.GetCertificateOperation(ctx, name, nil)
	if err != nil {
		return false, err
	}
	if operation.Status == nil {
		return false, fmt.Errorf("certificate %q operation has no status", name)
	}
	switch *operation.Status {
	case "completed":
		return true, nil
	case "inProgress":
		return false, nil
	case "failed", "cancelled":
		return false, fmt.Errorf("certificate %q operation %s", name, *operation.Status)
	default:
		return false, fmt.Errorf("certificate %q has unknown operation status %q", name, *operation.Status)
	}
}

func (syncer *tlsCertificatesSyncer) NeedsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	// TODO: If the management cluster where the HCP is placed changes, remove the old certificate references and create new ones for the new management cluster.
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return serviceProviderCluster.Status.AzureResources.KubeAPIServerCertificate != nil || serviceProviderCluster.Status.AzureResources.IngressCertificate != nil
	}
	return cluster.ServiceProviderProperties.ClusterServiceID != nil &&
		(serviceProviderCluster.Status.AzureResources.KubeAPIServerCertificate == nil || serviceProviderCluster.Status.AzureResources.KubeAPIServerCertificate.AzureReference == nil || serviceProviderCluster.Status.AzureResources.IngressCertificate == nil || serviceProviderCluster.Status.AzureResources.IngressCertificate.AzureReference == nil)
}

func (syncer *tlsCertificatesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := syncer.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	existing, err := syncer.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if !syncer.NeedsWork(cluster, existing) {
		return nil
	}
	replacement := existing.DeepCopy()
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		replacement.Status.AzureResources.KubeAPIServerCertificate = nil
		replacement.Status.AzureResources.IngressCertificate = nil
		return syncer.persist(ctx, key, existing, replacement)
	}
	managementClusterID := existing.Status.ManagementClusterResourceID
	if managementClusterID == nil {
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
	if managementCluster.Status.HostedClustersSecretsKeyVaultURL == "" {
		return utils.TrackError(fmt.Errorf("management cluster has no hosted clusters secrets Key Vault URL"))
	}
	vaultURL, err := url.Parse(managementCluster.Status.HostedClustersSecretsKeyVaultURL)
	if err != nil || vaultURL.Scheme != "https" || vaultURL.Hostname() == "" {
		return utils.TrackError(fmt.Errorf("management cluster has invalid hosted clusters secrets Key Vault URL"))
	}
	if replacement.Status.AzureResources.KubeAPIServerCertificate == nil {
		replacement.Status.AzureResources.KubeAPIServerCertificate = &coreapi.TLSCertificate{}
	}
	if replacement.Status.AzureResources.IngressCertificate == nil {
		replacement.Status.AzureResources.IngressCertificate = &coreapi.TLSCertificate{}
	}
	clusterServiceID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	certificates := []struct {
		name  string
		state *coreapi.TLSCertificate
	}{
		{"kube-apiserver-tls-cert-" + clusterServiceID, replacement.Status.AzureResources.KubeAPIServerCertificate},
		{"ingress-tls-cert-" + clusterServiceID, replacement.Status.AzureResources.IngressCertificate},
	}
	for _, certificate := range certificates {
		if certificate.state.AzureReference == nil {
			certificate.state.PendingReference = &coreapi.AzureTLSCertificateReference{KeyVaultURL: vaultURL, CertificateName: certificate.name}
		}
	}
	if controllerutil.NeedsUpdate(existing, replacement) {
		return syncer.persist(ctx, key, existing, replacement)
	}
	var observationErrors []error
	for _, certificate := range certificates {
		if certificate.state.AzureReference != nil {
			continue
		}
		reference := certificate.state.PendingReference
		ready, err := syncer.observe(ctx, reference.KeyVaultURL.String(), reference.CertificateName)
		if err != nil {
			observationErrors = append(observationErrors, fmt.Errorf("observe certificate %q: %w", certificate.name, err))
			continue
		}
		if ready {
			certificate.state.AzureReference = reference
			certificate.state.PendingReference = nil
		}
	}
	return errors.Join(append(observationErrors, syncer.persist(ctx, key, existing, replacement))...)
}

func (syncer *tlsCertificatesSyncer) persist(ctx context.Context, key controllerutils.HCPClusterKey, existing, replacement *coreapi.ServiceProviderCluster) error {
	if !controllerutil.NeedsUpdate(existing, replacement) {
		return nil
	}
	_, err := syncer.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("replace certificate observation status: %w", err))
	}
	return nil
}
