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

const CertificateObservationControllerName = "ObserveCertificates"

type certificateObservationSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	managementClusterLister      fleetlisters.ManagementClusterLister
	observe                      func(context.Context, string, string) (bool, error)
}

var _ controllerutils.ClusterSyncer = (*certificateObservationSyncer)(nil)

func NewCertificateObservationController(resourcesDBClient corecosmosstorage.ResourcesDBClient, informers coreinformers.BackendInformers, kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers, managementClusterLister fleetlisters.ManagementClusterLister, clients *azureclient.BackendIdentityAzureClients) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	return controllerutils.NewClusterWatchingController(CertificateObservationControllerName, resourcesDBClient, informers, kubeApplierInformers, 30*time.Second, &certificateObservationSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		managementClusterLister:      managementClusterLister,
		observe: func(ctx context.Context, vaultURL, name string) (bool, error) {
			client, err := clients.CertificatesClient(vaultURL)
			if err != nil {
				return false, err
			}
			certificate, err := client.GetCertificate(ctx, name, "", nil)
			var responseError *azcore.ResponseError
			if errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return certificateReady(certificate.Certificate, time.Now()), nil
		},
	})
}

func certificateReady(certificate azcertificates.Certificate, now time.Time) bool {
	attributes := certificate.Attributes
	return len(certificate.CER) > 0 && certificate.SID != nil && len(*certificate.SID) > 0 && attributes != nil && attributes.Enabled != nil && *attributes.Enabled &&
		(attributes.NotBefore == nil || !now.Before(*attributes.NotBefore)) && (attributes.Expires == nil || now.Before(*attributes.Expires))
}

func (syncer *certificateObservationSyncer) NeedsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	return cluster.ServiceProviderProperties.DeletionTimestamp == nil && cluster.ServiceProviderProperties.ClusterServiceID != nil &&
		(serviceProviderCluster.Status.KubeAPIServerCertificate != coreapi.CertificateObservationDone || serviceProviderCluster.Status.IngressCertificate != coreapi.CertificateObservationDone)
}

func (syncer *certificateObservationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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
	for _, state := range []*coreapi.CertificateObservationState{&replacement.Status.KubeAPIServerCertificate, &replacement.Status.IngressCertificate} {
		if *state != coreapi.CertificateObservationDone {
			*state = coreapi.CertificateObservationPending
		}
	}
	if controllerutil.NeedsUpdate(existing, replacement) {
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
	vaultURL := managementCluster.Status.HostedClustersSecretsKeyVaultURL
	if vaultURL == "" {
		return nil
	}
	clusterServiceID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	var observationErrors []error
	for _, certificate := range []struct {
		name  string
		state *coreapi.CertificateObservationState
	}{
		{"kube-apiserver-tls-cert-" + clusterServiceID, &replacement.Status.KubeAPIServerCertificate},
		{"ingress-tls-cert-" + clusterServiceID, &replacement.Status.IngressCertificate},
	} {
		if *certificate.state == coreapi.CertificateObservationDone {
			continue
		}
		ready, err := syncer.observe(ctx, vaultURL, certificate.name)
		if err != nil {
			observationErrors = append(observationErrors, fmt.Errorf("observe certificate %q: %w", certificate.name, err))
			continue
		}
		if ready {
			*certificate.state = coreapi.CertificateObservationDone
		}
	}
	return errors.Join(append(observationErrors, syncer.persist(ctx, key, existing, replacement))...)
}

func (syncer *certificateObservationSyncer) persist(ctx context.Context, key controllerutils.HCPClusterKey, existing, replacement *coreapi.ServiceProviderCluster) error {
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
