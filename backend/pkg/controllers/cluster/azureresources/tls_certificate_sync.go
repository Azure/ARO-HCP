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
	"net/url"
	"time"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// tlsCertificateField identifies which of ServiceProviderCluster's two TLS
// certificate references a controller instance observes: the kube-apiserver
// certificate or the ingress certificate. The two instances created by
// NewKubeAPIServerTLSCertificateController and NewIngressTLSCertificateController
// differ only in this field.
type tlsCertificateField struct {
	// label identifies the certificate, e.g. "kube-apiserver" or "ingress". It is used
	// both as the human-readable name in error messages and, via getCertificateName,
	// to build the Key Vault certificate/backing secret name, matching the naming
	// convention Cluster Service itself uses for each certificate.
	label string
	// get returns this field's TLS certificate reference (KubeAPIServerCertificate or
	// IngressCertificate) from the given AzureResources.
	get func(*coreapi.AzureResources) *coreapi.TLSCertificate
	// set stores this field's TLS certificate reference (KubeAPIServerCertificate or
	// IngressCertificate) onto the given AzureResources.
	set func(*coreapi.AzureResources, *coreapi.TLSCertificate)
}

// getCertificateName returns the Key Vault certificate/backing secret name for the
// given cluster, matching CS's utils.GetApiTlsCertName or utils.GetIngressTlsCertName.
func (field tlsCertificateField) getCertificateName(cluster *coreapi.Cluster) string {
	return field.label + "-tls-cert-" + cluster.ServiceProviderProperties.ClusterServiceID.ID()
}

// tlsCertificateSyncer is a read-only observer for a TLS certificate created by Cluster
// Service in Azure Key Vault. Which certificate an instance observes is determined
// entirely by field.
type tlsCertificateSyncer struct {
	field tlsCertificateField
	// certificatesClient builds a Key Vault certificates client for the given vault URL.
	certificatesClient           func(vaultURL string) (tlsCertificatesClient, error)
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	managementClusterLister      fleetlisters.ManagementClusterLister
}

var _ controllerutils.ClusterSyncer = (*tlsCertificateSyncer)(nil)

// newTLSCertificateController wires a tlsCertificateSyncer configured for one
// certificate field into a cluster watching controller under the given name.
func newTLSCertificateController(name string, field tlsCertificateField, resourcesDBClient corecosmosstorage.ResourcesDBClient, informers coreinformers.BackendInformers, managementClusterLister fleetlisters.ManagementClusterLister, clients *azureclient.BackendIdentityAzureClients) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	return controllerutils.NewClusterWatchingController(name, resourcesDBClient, informers, nil, 30*time.Second, &tlsCertificateSyncer{
		field:                        field,
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		managementClusterLister:      managementClusterLister,
		certificatesClient:           func(vaultURL string) (tlsCertificatesClient, error) { return clients.CertificatesClient(vaultURL) },
	})
}

func (syncer *tlsCertificateSyncer) needsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	// TODO: If the management cluster where the HCP is placed changes, remove the old certificate reference and create a new one for the new management cluster.
	current := syncer.field.get(&serviceProviderCluster.Status.AzureResources)
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return current != nil
	}
	return cluster.ServiceProviderProperties.ClusterServiceID != nil && (current == nil || current.AzureReference == nil)
}

func (syncer *tlsCertificateSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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
	if !syncer.needsWork(cluster, existing) {
		return nil
	}
	replacement := existing.DeepCopy()
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return syncer.syncDeletionObservationWork(ctx, key, existing, replacement)
	}
	return syncer.syncCreationObservationWork(ctx, key, cluster, existing, replacement)
}

// syncDeletionObservationWork observes whether the certificate has been removed from
// Key Vault and clears its reference once confirmed. The GET below uses the
// reference's own stored Key Vault URL/name, not the management cluster's current
// vault, so deletion needs neither ClusterServiceID nor a resolvable management
// cluster.
func (syncer *tlsCertificateSyncer) syncDeletionObservationWork(ctx context.Context, key controllerutils.HCPClusterKey, existing, replacement *coreapi.ServiceProviderCluster) error {
	state := syncer.field.get(&replacement.Status.AzureResources)
	if state == nil {
		return nil
	}
	reference := state.AzureReference
	if reference == nil {
		reference = state.PendingReference
	}
	if reference == nil {
		syncer.field.set(&replacement.Status.AzureResources, nil)
		return syncer.persistIfChanged(ctx, key, existing, replacement)
	}
	client, err := syncer.certificatesClient(reference.KeyVaultURL)
	if err != nil {
		return errors.Join(fmt.Errorf("observe %s certificate deletion: create certificates client: %w", syncer.field.label, err), syncer.persistIfChanged(ctx, key, existing, replacement))
	}
	gone, err := observeTLSCertificateDeleted(ctx, client, reference.CertificateName)
	if err != nil {
		return errors.Join(fmt.Errorf("observe %s certificate deletion: %w", syncer.field.label, err), syncer.persistIfChanged(ctx, key, existing, replacement))
	}
	if gone {
		syncer.field.set(&replacement.Status.AzureResources, nil)
	}
	return syncer.persistIfChanged(ctx, key, existing, replacement)
}

func (syncer *tlsCertificateSyncer) syncCreationObservationWork(ctx context.Context, key controllerutils.HCPClusterKey, cluster *coreapi.Cluster, existing, replacement *coreapi.ServiceProviderCluster) error {
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
	if len(managementCluster.Status.HostedClustersSecretsKeyVaultURL) == 0 {
		return utils.TrackError(fmt.Errorf("management cluster has no hosted clusters secrets Key Vault URL"))
	}
	vaultURL, err := url.Parse(managementCluster.Status.HostedClustersSecretsKeyVaultURL)
	if err != nil {
		return utils.TrackError(fmt.Errorf("parse management cluster hosted clusters secrets Key Vault URL: %w", err))
	}
	if syncer.field.get(&replacement.Status.AzureResources) == nil {
		syncer.field.set(&replacement.Status.AzureResources, &coreapi.TLSCertificate{})
	}
	state := syncer.field.get(&replacement.Status.AzureResources)
	certificateName := syncer.field.getCertificateName(cluster)
	if state.AzureReference == nil {
		state.PendingReference = &coreapi.AzureTLSCertificateReference{KeyVaultURL: managementCluster.Status.HostedClustersSecretsKeyVaultURL, CertificateName: certificateName}
	}
	// A pending reference was just created or changed above; persist it now and return
	// early rather than observing a certificate name we haven't committed yet. This
	// also covers precondition (ETag) failure: persistIfChanged swallows it and
	// returns nil, so we still return early here and simply defer to the next
	// reconciliation once the informer provides the current resource.
	if controllerutil.NeedsUpdate(existing, replacement) {
		return syncer.persistIfChanged(ctx, key, existing, replacement)
	}
	client, err := syncer.certificatesClient(vaultURL.String())
	if err != nil {
		return utils.TrackError(fmt.Errorf("create certificates client: %w", err))
	}
	reference := state.PendingReference
	ready, err := observeTLSCertificate(ctx, client, reference.CertificateName)
	if err != nil {
		return errors.Join(utils.TrackError(fmt.Errorf("observe certificate %q: %w", certificateName, err)), syncer.persistIfChanged(ctx, key, existing, replacement))
	}
	if ready {
		state.AzureReference = reference
		state.PendingReference = nil
	}
	return syncer.persistIfChanged(ctx, key, existing, replacement)
}

// persistIfChanged writes replacement if it differs from existing, swallowing both the
// no-op case (nothing to write) and a precondition (ETag) conflict from a concurrent
// writer (another writer updated the document first; we'll be re-enqueued by the
// informer and retry against the fresh version).
func (syncer *tlsCertificateSyncer) persistIfChanged(ctx context.Context, key controllerutils.HCPClusterKey, existing, replacement *coreapi.ServiceProviderCluster) error {
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
