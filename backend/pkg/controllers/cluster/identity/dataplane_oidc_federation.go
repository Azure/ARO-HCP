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

package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/federatedidentitycredential"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// DataPlaneOIDCFederationControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const DataPlaneOIDCFederationControllerName = "DataPlaneOIDCFederation"

const (
	// dataPlaneOIDCFederationRecheckInterval is the base interval before
	// re-querying Azure for a federated identity credential that is already in
	// a terminal phase. Combined with dataPlaneOIDCFederationRecheckJitter via
	// wait.Jitter.
	dataPlaneOIDCFederationRecheckInterval = 12 * time.Hour
	dataPlaneOIDCFederationRecheckJitter   = 0.5

	// dataPlaneOIDCFederationAudience is the Azure AD token-exchange audience
	// required on federated identity credentials.
	dataPlaneOIDCFederationAudience = "api://AzureADTokenExchange"
)

// dataPlaneOIDCFederatedIdentityCredential is one Azure federated identity
// credential that should exist on a managed identity for a single Kubernetes
// service account of a data-plane operator.
type dataPlaneOIDCFederatedIdentityCredential struct {
	subject    string
	resourceID *azcorearm.ResourceID
}

// dataPlaneOIDCFederationSyncer creates and deletes Azure federated identity
// credentials for entries in
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneOIDCFederation.
//
// For PendingConfigure, each identity is mapped to the data-plane operators it
// is assigned to on the Cluster payload. For each of those operators, a
// federated identity credential is created for every Kubernetes service
// account listed in the cluster-scoped identities config. PendingDeconfigure
// deletes those credentials.
type dataPlaneOIDCFederationSyncer struct {
	clock                         utilsclock.PassiveClock
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	smiClientBuilder              azureclient.ServiceManagedIdentityClientBuilder
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig
	oidcIssuerBaseURL             string
}

var _ controllerutils.ClusterSyncer = (*dataPlaneOIDCFederationSyncer)(nil)

// NewDataPlaneOIDCFederationController creates a cluster-watching controller
// that configures and deconfigures data-plane OIDC federation on the cluster's
// managed identities.
func NewDataPlaneOIDCFederationController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig,
	oidcIssuerBaseURL string,
) controllerutils.Controller {

	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clock,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		resourcesDBClient:             resourcesDBClient,
		smiClientBuilder:              smiClientBuilder,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
		oidcIssuerBaseURL:             oidcIssuerBaseURL,
	}

	return controllerutils.NewClusterWatchingController(
		DataPlaneOIDCFederationControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *dataPlaneOIDCFederationSyncer) needsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	for _, status := range serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		if status == nil {
			continue
		}
		switch status.Phase {
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
			coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure:
			if status.EarliestRecheckTime != nil && s.clock.Now().Before(status.EarliestRecheckTime.Time) {
				continue
			}
			return true
		}
	}
	return false
}

func (s *dataPlaneOIDCFederationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	existingServiceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	if !s.needsWork(existingServiceProviderCluster) {
		return nil
	}

	smiResourceID := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	if smiResourceID == nil {
		return utils.TrackError(fmt.Errorf("cluster ServiceManagedIdentity is nil. Cannot configure data-plane OIDC federation"))
	}

	federatedIdentityCredentialsClient, err := s.smiClientBuilder.FederatedIdentityCredentialsClient(
		ctx,
		existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL,
		smiResourceID,
		existingCluster.ID.SubscriptionID,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Federated Identity Credentials Client: %w", err))
	}

	// TODO decide what to do when CSID is nil still. Probably early return.
	csClusterID := controllerutils.ClusterServiceIDForCluster(existingCluster)
	replacement := existingServiceProviderCluster.DeepCopy()
	if replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation == nil {
		replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = map[coreapi.ManagedIdentityDataplaneOIDCFederationKey]*coreapi.ManagedIdentityDataplaneOIDCFederationStatus{}
	}

	var errs []error
	for federationKey, status := range replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		if status == nil {
			continue
		}
		if status.EarliestRecheckTime != nil && s.clock.Now().Before(status.EarliestRecheckTime.Time) {
			continue
		}

		// TODO should we add an additional safety check to ignore and log the identity if it's a MSI-based one, as it shouldn't occur
		// that the identity in the ManagedIdentitiesWithDataPlaneOIDCFederation map is a MSI-based one?

		switch status.Phase {
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure:
			// TODO should we pass TenantID of the Identity or Tenant ID of the cluster? the OIDC issuer URL is generated from
			// the Tenant ID of the cluster always, so it should be the cluster's Tenant ID.
			issuerURL := s.generateClusterOIDCIssuerURL(federationKey.TenantID, csClusterID)
			if err := s.configureFederation(ctx, federatedIdentityCredentialsClient, existingCluster, federationKey, status, csClusterID, issuerURL); err != nil {
				errs = append(errs, err)
			}
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure:
			if err := s.deconfigureFederation(ctx, federatedIdentityCredentialsClient, existingCluster, federationKey, status, csClusterID); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if equality.Semantic.DeepEqual(replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, existingServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation) {
		return errors.Join(errs...)
	}

	_, err = s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return errors.Join(errs...)
	}
	if err != nil {
		return errors.Join(append(errs, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))...)
	}

	return errors.Join(errs...)
}

func (s *dataPlaneOIDCFederationSyncer) configureFederation(
	ctx context.Context,
	ficClient azureclient.FederatedIdentityCredentialsClient,
	cluster *coreapi.HCPOpenShiftCluster,
	federationKey coreapi.ManagedIdentityDataplaneOIDCFederationKey,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
	issuerURL string,
) error {
	identityResourceID, err := s.parseManagedIdentityResourceID(federationKey)
	if err != nil {
		return err
	}

	credentials, err := s.federatedIdentityCredentialsForIdentity(cluster, identityResourceID, csClusterID)
	if err != nil {
		return err
	}

	status.PendingAzureResources = s.resourceIDsFromCredentials(credentials)

	for _, credential := range credentials {
		// TODO if this runs even if it doesn't change it would call CreateOrUpdate. We should probably not trigger it
		// if we don't detet changes.
		_, err = ficClient.CreateOrUpdate(
			ctx,
			identityResourceID.ResourceGroupName,
			identityResourceID.Name,
			credential.resourceID.Name,
			armmsi.FederatedIdentityCredential{
				Properties: &armmsi.FederatedIdentityCredentialProperties{
					Issuer:    ptr.To(issuerURL),
					Subject:   ptr.To(credential.subject),
					Audiences: []*string{ptr.To(dataPlaneOIDCFederationAudience)},
				},
			},
			nil,
		)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to create federated identity credential %s on identity %s: %w", credential.resourceID.Name, identityResourceID.String(), err))
		}
	}

	for _, credential := range credentials {
		_, err = ficClient.Get(ctx, identityResourceID.ResourceGroupName, identityResourceID.Name, credential.resourceID.Name, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to get federated identity credential %s on identity %s after create: %w", credential.resourceID.Name, identityResourceID.String(), err))
		}
	}

	status.PendingAzureResources = nil
	status.AzureResources = s.resourceIDsFromCredentials(credentials)
	status.Phase = coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(dataPlaneOIDCFederationRecheckInterval, dataPlaneOIDCFederationRecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	return nil
}

func (s *dataPlaneOIDCFederationSyncer) deconfigureFederation(
	ctx context.Context,
	ficClient azureclient.FederatedIdentityCredentialsClient,
	cluster *coreapi.HCPOpenShiftCluster,
	federationKey coreapi.ManagedIdentityDataplaneOIDCFederationKey,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
) error {
	identityResourceID, err := s.parseManagedIdentityResourceID(federationKey)
	if err != nil {
		return err
	}

	var expected []dataPlaneOIDCFederatedIdentityCredential
	if len(csClusterID) > 0 {
		expected, err = s.federatedIdentityCredentialsForIdentity(cluster, identityResourceID, csClusterID)
		if err != nil {
			return err
		}
	}

	toDelete := s.uniqueSortedResourceIDs(append(append(s.resourceIDsFromCredentials(expected), status.AzureResources...), status.PendingAzureResources...))
	status.PendingAzureResources = toDelete

	for _, ficResourceID := range toDelete {
		_, err = ficClient.Delete(ctx, identityResourceID.ResourceGroupName, identityResourceID.Name, ficResourceID.Name, nil)
		if err != nil && !azureclient.IsResourceNotFoundErr(err) {
			return utils.TrackError(fmt.Errorf("failed to delete federated identity credential %s on identity %s: %w", ficResourceID.Name, identityResourceID.String(), err))
		}
	}

	status.PendingAzureResources = nil
	status.AzureResources = nil
	status.Phase = coreapi.ManagedIdentityDataplaneOIDCFederationPhaseDeconfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(dataPlaneOIDCFederationRecheckInterval, dataPlaneOIDCFederationRecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	return nil
}

// federatedIdentityCredentialsForIdentity returns the federated identity
// credentials that should exist on identityResourceID. It looks up every
// data-plane operator on the Cluster payload assigned to that identity, then
// expands each operator into the Kubernetes service accounts from the
// cluster-scoped identities config.
func (s *dataPlaneOIDCFederationSyncer) federatedIdentityCredentialsForIdentity(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceID *azcorearm.ResourceID,
	csClusterID string,
) ([]dataPlaneOIDCFederatedIdentityCredential, error) {
	if s.clusterScopedIdentitiesConfig == nil {
		return nil, nil
	}

	identityKey := strings.ToLower(identityResourceID.String())
	var credentials []dataPlaneOIDCFederatedIdentityCredential
	for operatorName, assignedIdentity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		if strings.ToLower(assignedIdentity.String()) != identityKey {
			continue
		}

		operatorIdentity, ok := s.clusterScopedIdentitiesConfig.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
		if !ok || operatorIdentity == nil {
			continue
		}

		for _, serviceAccount := range operatorIdentity.KubernetesServiceAccounts {
			if serviceAccount == nil || len(serviceAccount.Name) == 0 || len(serviceAccount.Namespace) == 0 {
				continue
			}

			ficResourceID, err := federatedidentitycredential.GenerateFederatedIdentityCredentialResourceID(
				identityResourceID,
				csClusterID,
				operatorName,
				serviceAccount.Namespace,
				serviceAccount.Name,
			)
			if err != nil {
				return nil, utils.TrackError(fmt.Errorf("failed to generate federated identity credential ResourceID for identity %s operator %q: %w", identityResourceID.String(), operatorName, err))
			}
			credentials = append(credentials, dataPlaneOIDCFederatedIdentityCredential{
				subject:    serviceAccount.AsOIDCSubject(),
				resourceID: ficResourceID,
			})
		}
	}

	slices.SortFunc(credentials, func(a, b dataPlaneOIDCFederatedIdentityCredential) int {
		return strings.Compare(a.resourceID.Name, b.resourceID.Name)
	})
	return credentials, nil
}

func (s *dataPlaneOIDCFederationSyncer) parseManagedIdentityResourceID(federationKey coreapi.ManagedIdentityDataplaneOIDCFederationKey) (*azcorearm.ResourceID, error) {
	identityResourceID, err := azcorearm.ParseResourceID(federationKey.ResourceID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse managed identity ResourceID %s: %w", federationKey.ResourceID, err))
	}
	return identityResourceID, nil
}

func (s *dataPlaneOIDCFederationSyncer) resourceIDsFromCredentials(credentials []dataPlaneOIDCFederatedIdentityCredential) []*azcorearm.ResourceID {
	ids := make([]*azcorearm.ResourceID, 0, len(credentials))
	for _, credential := range credentials {
		ids = append(ids, credential.resourceID)
	}
	return ids
}

func (s *dataPlaneOIDCFederationSyncer) uniqueSortedResourceIDs(ids []*azcorearm.ResourceID) []*azcorearm.ResourceID {
	seen := map[string]struct{}{}
	var out []*azcorearm.ResourceID
	for _, id := range ids {
		if id == nil {
			continue
		}
		key := strings.ToLower(id.String())
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b *azcorearm.ResourceID) int {
		return strings.Compare(strings.ToLower(a.String()), strings.ToLower(b.String()))
	})
	return out
}

// generateClusterOIDCIssuerURL builds the OIDC issuer URL as
// <oidcIssuerBaseURL><clusterTenantID>/<csClusterID>. If <oidcIssuerBaseURL> does not end with a slash, it is added to
// the URL.
func (s *dataPlaneOIDCFederationSyncer) generateClusterOIDCIssuerURL(clusterTenantID, csClusterID string) string {
	return fmt.Sprintf("%s%s/%s", s.ensureTrailingSlash(s.oidcIssuerBaseURL), clusterTenantID, csClusterID)
}

func (s *dataPlaneOIDCFederationSyncer) ensureTrailingSlash(value string) string {
	if strings.HasSuffix(value, "/") {
		return value
	}
	return fmt.Sprintf("%s/", value)
}
