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

package dataplaneworkloads

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/federatedidentitycredential"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/azure"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
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

	// dataPlaneOIDCFederationAudience is the Azure AD token-exchange audience required on federated identity credentials.
	dataPlaneOIDCFederationAudience = "openshift"
)

// dataPlaneOIDCFederatedIdentityCredential is one Azure federated identity
// credential that should exist on a managed identity for a single Kubernetes
// service account of a data-plane operator.
type dataPlaneOIDCFederatedIdentityCredential struct {
	resourceID *azcorearm.ResourceID
	subject    string
}

// dataPlaneOIDCFederationSyncer creates and deletes Azure federated identity
// credentials for entries in
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneOIDCFederation.
//
// For PendingConfigure, each identity is mapped to the data-plane operators it
// is assigned to on the Cluster payload. For each of those operators, a
// federated identity credential is created for every Kubernetes service
// account listed in the cluster-scoped identities config. Configured entries
// are rechecked on EarliestRecheckTime (or immediately when the desired FIC
// set changes): Get each desired FIC and CreateOrUpdate only if it is missing
// or Issuer/Subject/Audiences drifted, and delete tracked FICs that are no
// longer desired while the identity stays Configured. See ensureFederation
// for the cases that shrink the desired set.
// PendingDeconfigure deletes the credentials already tracked
// on the status (AzureResources and PendingAzureResources). New FIC resource
// IDs are persisted to PendingAzureResources before CreateOrUpdate, so a crash
// cannot lose the tracked set. Azure errors on one FIC do not skip the rest:
// remaining creates, updates, and deletes still run, confirmed IDs move to
// AzureResources, and unfinished IDs stay pending so a partial update can be
// persisted. Configure needs a cluster service ID to
// generate credential names. Deconfigure skips Azure deletes
// if Managed Identities Data Plane reports the ServiceManagedIdentity is gone
// (missing ClientID, ClientSecret, TenantID, or AuthenticationEndpoint), and still marks the entry Deconfigured.
type dataPlaneOIDCFederationSyncer struct {
	clock                         utilsclock.PassiveClock
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	smiClientBuilder              azureclient.ServiceManagedIdentityClientBuilder
	fpaMIdataplaneClientBuilder   azureclient.FPAMIDataplaneClientBuilder
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
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
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
		fpaMIdataplaneClientBuilder:   fpaMIdataplaneClientBuilder,
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

func (s *dataPlaneOIDCFederationSyncer) needsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	csClusterID := controllerutils.ClusterServiceIDForCluster(cluster)
	now := s.clock.Now()
	for federationKey, status := range serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		switch status.Phase {
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
			coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure:
			return true
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured:
			if len(csClusterID) > 0 && s.desiredFICSetDiffers(cluster, federationKey, status, csClusterID) {
				return true
			}
			if status.EarliestRecheckTime != nil && now.Before(status.EarliestRecheckTime.Time) {
				continue
			}
			return true
		}
	}
	return false
}

func (s *dataPlaneOIDCFederationSyncer) desiredFICSetDiffers(
	cluster *coreapi.HCPOpenShiftCluster,
	federationKey coreapi.ManagedIdentityDataplaneOIDCFederationKey,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
) bool {
	desired, err := s.pendingConfigureResourceIDs(cluster, federationKey, csClusterID)
	if err != nil {
		return true
	}
	return controllerutil.NeedsUpdate(s.uniqueSortedResourceIDs(status.AzureResources), s.uniqueSortedResourceIDs(desired))
}

func (s *dataPlaneOIDCFederationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
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

	if !s.needsWork(existingCluster, existingServiceProviderCluster) {
		return nil
	}

	smiResourceID := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	if smiResourceID == nil {
		return utils.TrackError(fmt.Errorf("cluster ServiceManagedIdentity is nil. Cannot configure data-plane OIDC federation"))
	}

	csClusterID := controllerutils.ClusterServiceIDForCluster(existingCluster)

	replacement := existingServiceProviderCluster.DeepCopy()

	// We store the current time in-memory and use this value so all the logic within a reconcile pass
	// sees the same value for the current time.
	timeNow := s.clock.Now()

	var errs []error

	// Loop 1: persist FederatedIdentityCredential resource IDs that are not yet on the document before
	// any Azure CreateOrUpdate. PendingConfigure and Configured record IDs not already in
	// AzureResources so a crash after create cannot lose them. PendingConfigure's AzureResources
	// is empty, so that is the full desired set. PendingDeconfigure is skipped: its
	// IDs are already on AzureResources or leftover PendingAzureResources.
	// Configured is skipped only when EarliestRecheckTime is in the future and the
	// desired FIC set is unchanged. A drift is processed immediately.
	for federationKey := range replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		status := replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[federationKey]

		// Configured is skipped only when EarliestRecheckTime is in the future and the
		// desired FIC set is unchanged. A drift is processed immediately.
		if status.Phase == coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured &&
			status.EarliestRecheckTime != nil && timeNow.Before(status.EarliestRecheckTime.Time) &&
			(len(csClusterID) == 0 || !s.desiredFICSetDiffers(existingCluster, federationKey, status, csClusterID)) {
			continue
		}

		switch status.Phase {
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
			coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured:
			if len(csClusterID) == 0 {
				// Credential names and the issuer URL include the cluster service ID. If the cluster service ID is not set,
				// we skip: the FICs cannot be created.
				continue
			}
			desired, err := s.pendingConfigureResourceIDs(existingCluster, federationKey, csClusterID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			status.PendingAzureResources = s.resourceIDsNotIn(desired, status.AzureResources)
		}
	}

	// Note: a replace here will also occur if the ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation is not initialized
	// yet, even if there are no credentials in
	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting pending data-plane OIDC federation credentials onto ServiceProviderCluster")

		persistedServiceProviderCluster, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		existingServiceProviderCluster = persistedServiceProviderCluster
		replacement = existingServiceProviderCluster.DeepCopy()
	}
	// Loop 2: create, update, or delete federated identity credentials in Azure,
	// then update each entry in memory. Azure errors on one FIC do not skip the
	// rest of that identity: remaining creates, updates, and deletes still run,
	// confirmed IDs move to AzureResources, and unfinished IDs stay pending so
	// the persist after this loop can apply a partial update. Phase becomes
	// Configured or Deconfigured only when every Azure call for that identity
	// succeeds. PendingConfigure and Configured Get each desired FIC and
	// CreateOrUpdate only when it is missing or Issuer/Subject/Audiences
	// drifted. Configured also deletes tracked FICs that are no longer desired
	// (see ensureFederation). Deconfigure deletes the union of AzureResources
	// and PendingAzureResources. Results are persisted after the loop.
	// One FederatedIdentityCredentialsClient is built for this reconcile and
	// reused for every identity. It is created on first Azure FIC use so a
	// PendingDeconfigure that skips Azure (ServiceManagedIdentity gone) does
	// not fail client construction.
	getFederatedIdentityCredentialsClient := s.newFederatedIdentityCredentialsClientGetter(ctx, existingCluster)

	for federationKey := range replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		status := replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[federationKey]

		// Configured is skipped only when EarliestRecheckTime is in the future and the
		// desired FIC set is unchanged. A drift is processed immediately.
		if status.Phase == coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured &&
			status.EarliestRecheckTime != nil && timeNow.Before(status.EarliestRecheckTime.Time) &&
			(len(csClusterID) == 0 || !s.desiredFICSetDiffers(existingCluster, federationKey, status, csClusterID)) {
			continue
		}

		switch status.Phase {
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingConfigure,
			coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured:
			if len(csClusterID) == 0 {
				continue
			}
			// TODO should we pass TenantID of the Identity or Tenant ID of the cluster? the OIDC issuer URL is generated from
			// the Tenant ID of the cluster always, so it should be the cluster's Tenant ID.
			issuerURL := s.generateClusterOIDCIssuerURL(federationKey.TenantID, csClusterID)
			client, err := getFederatedIdentityCredentialsClient()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.ensureFederation(ctx, existingCluster, federationKey, status, csClusterID, issuerURL, client)
			if err != nil {
				errs = append(errs, err)
			}
		case coreapi.ManagedIdentityDataplaneOIDCFederationPhasePendingDeconfigure:
			err := s.deconfigureFederation(ctx, existingCluster, federationKey, status, getFederatedIdentityCredentialsClient)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting data-plane OIDC federation configure/deconfigure result onto ServiceProviderCluster")

		persistedServiceProviderCluster, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		existingServiceProviderCluster = persistedServiceProviderCluster
		replacement = existingServiceProviderCluster.DeepCopy()
	}

	return errors.Join(errs...)
}

func (s *dataPlaneOIDCFederationSyncer) pendingConfigureResourceIDs(
	cluster *coreapi.HCPOpenShiftCluster,
	federationKey coreapi.ManagedIdentityDataplaneOIDCFederationKey,
	csClusterID string,
) ([]*azcorearm.ResourceID, error) {
	identityResourceID, err := s.parseManagedIdentityResourceID(federationKey)
	if err != nil {
		return nil, err
	}
	credentials, err := s.federatedIdentityCredentialsForIdentity(cluster, identityResourceID, csClusterID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get federated identity credentials for identity %s: %w", identityResourceID.String(), err))
	}
	return s.resourceIDsFromCredentials(credentials), nil
}

func (s *dataPlaneOIDCFederationSyncer) ensureFederation(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	federationKey coreapi.ManagedIdentityDataplaneOIDCFederationKey,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
	issuerURL string,
	federatedIdentityCredentialsClient azureclient.FederatedIdentityCredentialsClient,
) error {
	identityResourceID, err := s.parseManagedIdentityResourceID(federationKey)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse managed identity ResourceID %s: %w", federationKey.ResourceID, err))
	}

	credentials, err := s.federatedIdentityCredentialsForIdentity(cluster, identityResourceID, csClusterID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get federated identity credentials for identity %s: %w", identityResourceID.String(), err))
	}
	desiredIDs := s.resourceIDsFromCredentials(credentials)
	desiredKeys := make(map[string]struct{}, len(desiredIDs))
	for _, id := range desiredIDs {
		desiredKeys[strings.ToLower(id.String())] = struct{}{}
	}

	ensuredKeys := make(map[string]struct{}, len(credentials))
	var errs []error
	for _, credential := range credentials {
		err = s.ensureFederatedIdentityCredential(ctx, federatedIdentityCredentialsClient, identityResourceID, issuerURL, credential)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ensuredKeys[strings.ToLower(credential.resourceID.String())] = struct{}{}
	}

	// Delete FICs still listed on AzureResources that are no longer desired.
	// This is not identity teardown. PendingDeconfigure handles the identity
	// leaving ManagedIdentityDetails (including the last data-plane operator
	// leaving this identity, even if the same UAMI is still CP or SMI: Fetch
	// drops the DP-keyed details entry), cluster deletion, and ClientID /
	// PrincipalID / TenantID rotation (old key). This loop runs while the
	// identity stays Configured because it is still in DataPlaneOperators.
	//
	// Desired FICs are every Kubernetes service account of every data-plane
	// operator currently mapped to this identity in DataPlaneOperators. Names
	// include the cluster service ID, operator name, SA namespace, and SA
	// name. The set shrinks in these cases (including future mutability of
	// DataPlaneOperators and reuse of one identity across data-plane operators):
	//
	// Data-plane operator assignment on the cluster payload:
	//  - An operator is remapped from this identity to another identity, and
	//    this identity still has at least one other data-plane operator.
	//  - An operator is removed from DataPlaneOperators, and this identity
	//    still has at least one other data-plane operator.
	//  - A DataPlaneOperators map key is renamed. FICs for the old operator
	//    name are no longer desired; FICs for the new name are ensured above.
	//
	// Cluster-scoped identities config:
	//  - A Kubernetes service account is removed from an operator still
	//    mapped to this identity.
	//  - A service account is renamed (name or namespace). The old FIC id is
	//    deleted and the new one is ensured above.
	//  - An operator's service-account list is replaced with a smaller set.
	//  - An operator is dropped from DataPlaneOperatorsIdentities while it
	//    remains on the cluster payload. Unknown operators are skipped, so
	//    all of that operator's FICs become undesired.
	//
	// FIC resource ID inputs:
	//  - The cluster service ID used in FIC names changes, so every
	//    previously tracked FIC id is no longer desired.
	deletedKeys := make(map[string]struct{})
	for _, ficResourceID := range status.AzureResources {
		if _, ok := desiredKeys[strings.ToLower(ficResourceID.String())]; ok {
			continue
		}
		_, err = federatedIdentityCredentialsClient.Delete(ctx, identityResourceID.ResourceGroupName, identityResourceID.Name, ficResourceID.Name, nil)
		if err != nil && !azureclient.IsFederatedCredentialNotFoundErr(err) {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to delete federated identity credential %s on identity %s: %w", ficResourceID.Name, identityResourceID.String(), err)))
			continue
		}
		deletedKeys[strings.ToLower(ficResourceID.String())] = struct{}{}
	}

	// Confirmed desired FICs and extras that Azure did not delete stay on
	// AzureResources so the persist after loop 2 can record partial progress.
	// Desired IDs that were not ensured this pass stay in PendingAzureResources.
	var nextAzureResources []*azcorearm.ResourceID
	for _, ficResourceID := range status.AzureResources {
		key := strings.ToLower(ficResourceID.String())
		if _, desired := desiredKeys[key]; desired {
			nextAzureResources = append(nextAzureResources, ficResourceID)
			continue
		}
		if _, deleted := deletedKeys[key]; !deleted {
			nextAzureResources = append(nextAzureResources, ficResourceID)
		}
	}
	for _, credential := range credentials {
		if _, ok := ensuredKeys[strings.ToLower(credential.resourceID.String())]; ok {
			nextAzureResources = append(nextAzureResources, credential.resourceID)
		}
	}

	if len(errs) > 0 {
		status.AzureResources = s.uniqueSortedResourceIDs(nextAzureResources)
		status.PendingAzureResources = s.uniqueSortedResourceIDs(s.resourceIDsNotIn(desiredIDs, status.AzureResources))
		return errors.Join(errs...)
	}

	status.AzureResources = desiredIDs
	status.PendingAzureResources = nil
	status.Phase = coreapi.ManagedIdentityDataplaneOIDCFederationPhaseConfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(dataPlaneOIDCFederationRecheckInterval, dataPlaneOIDCFederationRecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	return nil
}

func (s *dataPlaneOIDCFederationSyncer) ensureFederatedIdentityCredential(
	ctx context.Context,
	client azureclient.FederatedIdentityCredentialsClient,
	identityResourceID *azcorearm.ResourceID,
	issuerURL string,
	credential dataPlaneOIDCFederatedIdentityCredential,
) error {
	desired := s.buildDesiredFederatedIdentityCredential(issuerURL, credential.subject)
	got, err := client.Get(ctx, identityResourceID.ResourceGroupName, identityResourceID.Name, credential.resourceID.Name, nil)
	if err != nil && !azureclient.IsFederatedCredentialNotFoundErr(err) {
		return utils.TrackError(fmt.Errorf("failed to get federated identity credential %s on identity %s: %w", credential.resourceID.Name, identityResourceID.String(), err))
	}
	if err == nil && s.federatedIdentityCredentialFieldsEqual(got.Properties, desired.Properties) {
		return nil
	}

	_, err = client.CreateOrUpdate(
		ctx,
		identityResourceID.ResourceGroupName,
		identityResourceID.Name,
		credential.resourceID.Name,
		desired,
		nil,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create federated identity credential %s on identity %s: %w", credential.resourceID.Name, identityResourceID.String(), err))
	}
	return nil
}

func (s *dataPlaneOIDCFederationSyncer) federatedIdentityCredentialFieldsEqual(existing, desired *armmsi.FederatedIdentityCredentialProperties) bool {
	switch {
	case existing == nil && desired != nil:
		return false
	case existing != nil && desired == nil:
		return false
	case existing == nil && desired == nil:
		return true
	}
	if !ptr.Equal(existing.Issuer, desired.Issuer) {
		return false
	}
	if !ptr.Equal(existing.Subject, desired.Subject) {
		return false
	}
	return s.audienceSetsEqual(existing.Audiences, desired.Audiences)
}

func (s *dataPlaneOIDCFederationSyncer) audienceSetsEqual(a, b []*string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, audience := range a {
		seen[ptr.Deref(audience, "")]++
	}
	for _, audience := range b {
		key := ptr.Deref(audience, "")
		if seen[key] == 0 {
			return false
		}
		seen[key]--
	}
	return true
}

func (s *dataPlaneOIDCFederationSyncer) deconfigureFederation(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	federationKey coreapi.ManagedIdentityDataplaneOIDCFederationKey,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	getFederatedIdentityCredentialsClient func() (azureclient.FederatedIdentityCredentialsClient, error),
) error {
	identityResourceID, err := s.parseManagedIdentityResourceID(federationKey)
	if err != nil {
		return err
	}

	toDelete := s.uniqueSortedResourceIDs(append(append([]*azcorearm.ResourceID{}, status.AzureResources...), status.PendingAzureResources...))

	smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	smiExists, err := s.serviceManagedIdentityExists(ctx, cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, smiResourceID)
	if err != nil {
		return err
	}
	if smiExists {
		federatedIdentityCredentialsClient, err := getFederatedIdentityCredentialsClient()
		if err != nil {
			return err
		}

		var remaining []*azcorearm.ResourceID
		var errs []error
		for _, ficResourceID := range toDelete {
			_, err = federatedIdentityCredentialsClient.Delete(ctx, identityResourceID.ResourceGroupName, identityResourceID.Name, ficResourceID.Name, nil)
			if err != nil && !azureclient.IsFederatedCredentialNotFoundErr(err) && !azureclient.IsFederatedCredentialParentResourceNotFoundErr(err) &&
				!azureclient.IsAzureAuthorizationFailedError(err) {
				errs = append(errs, utils.TrackError(fmt.Errorf("failed to delete federated identity credential %s on identity %s: %w", ficResourceID.Name, identityResourceID.String(), err)))
				remaining = append(remaining, ficResourceID)
				continue
			}
		}
		if len(errs) > 0 {
			status.AzureResources = s.uniqueSortedResourceIDs(remaining)
			status.PendingAzureResources = nil
			return errors.Join(errs...)
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
	identityKey := strings.ToLower(identityResourceID.String())
	var credentials []dataPlaneOIDCFederatedIdentityCredential
	for operatorName, assignedIdentity := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		if strings.ToLower(assignedIdentity.String()) != identityKey {
			continue
		}

		// TODO we should improve this on RP side so this can never occur, by validating the cluster scoped identities config
		// in RP Frontend instead of only in CS.
		operatorIdentity, ok := s.clusterScopedIdentitiesConfig.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
		if !ok {
			continue
		}

		for _, serviceAccount := range operatorIdentity.KubernetesServiceAccounts {
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
		return strings.Compare(strings.ToLower(a.resourceID.String()), strings.ToLower(b.resourceID.String()))
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

// resourceIDsFromCredentials returns a slice of resource IDs from the given list of dataPlaneOIDCFederatedIdentityCredential
func (s *dataPlaneOIDCFederationSyncer) resourceIDsFromCredentials(credentials []dataPlaneOIDCFederatedIdentityCredential) []*azcorearm.ResourceID {
	ids := make([]*azcorearm.ResourceID, 0, len(credentials))
	for _, credential := range credentials {
		ids = append(ids, credential.resourceID)
	}
	return ids
}

// uniqueSortedResourceIDs returns a slice of resource IDs that are unique and sorted by their string representation.
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

// resourceIDsNotIn returns the IDs in ids that are not present in existing.
func (s *dataPlaneOIDCFederationSyncer) resourceIDsNotIn(ids, existing []*azcorearm.ResourceID) []*azcorearm.ResourceID {
	seen := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		if id == nil {
			continue
		}
		seen[strings.ToLower(id.String())] = struct{}{}
	}
	var out []*azcorearm.ResourceID
	for _, id := range ids {
		if id == nil {
			continue
		}
		if _, ok := seen[strings.ToLower(id.String())]; ok {
			continue
		}
		out = append(out, id)
	}
	return out
}

// serviceManagedIdentityExists reports whether the cluster ServiceManagedIdentity
// still exists in Azure by checking it through the Managed Identities Data Plane API (or using the mock miDataplaneClient
// if the mock mi is enabled).
func (s *dataPlaneOIDCFederationSyncer) serviceManagedIdentityExists(ctx context.Context, clusterIdentityURL string, smiResourceID *azcorearm.ResourceID) (bool, error) {
	miDataplaneClient, err := s.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(clusterIdentityURL)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Client: %w", err))
	}

	resp, err := miDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: []string{smiResourceID.String()},
	})
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceManagedIdentity credentials from Managed Identities Data Plane: %w", err))
	}

	// Currently, the MSI dataplane API returns an identity even if not found. An error is returned here to give visibility if this behaviour ever changes.
	if len(resp.ExplicitIdentities) == 0 {
		return false, utils.TrackError(fmt.Errorf("Managed Identities Data Plane returned no credentials for ServiceManagedIdentity %s", smiResourceID.String()))
	}
	if len(resp.ExplicitIdentities) > 1 {
		return false, utils.TrackError(fmt.Errorf("Managed Identities Data Plane returned %d credentials for ServiceManagedIdentity %s, expected 1", len(resp.ExplicitIdentities), smiResourceID.String()))
	}

	identity := resp.ExplicitIdentities[0]

	// Currently, the MSI dataplane API returns the resource ID even if the identity does not exist. An error is returned here to give visibility if this behaviour ever changes.
	if identity.ResourceID == nil {
		return false, utils.TrackError(fmt.Errorf("Managed Identities Data Plane Credential Resource ID is nil for ServiceManagedIdentity %s", smiResourceID.String()))
	}

	// The MIDataplane shouldn't return resource id different from the requested one. If it does this is unexpected
	// and we return an error.
	if !strings.EqualFold(*identity.ResourceID, smiResourceID.String()) {
		return false, utils.TrackError(fmt.Errorf("Managed Identities Data Plane Credential Resource ID %s in the response does not match requested ServiceManagedIdentity %s", *identity.ResourceID, smiResourceID.String()))
	}

	// If the returned response doesn't contain the expected fields that indicate the Managed Identity exists, we log it and we return false.
	if len(ptr.Deref(identity.ClientID, "")) == 0 ||
		len(ptr.Deref(identity.ClientSecret, "")) == 0 ||
		len(ptr.Deref(identity.TenantID, "")) == 0 ||
		len(ptr.Deref(identity.AuthenticationEndpoint, "")) == 0 {
		utils.LoggerFromContext(ctx).Info("ServiceManagedIdentity is missing required Managed Identities Data Plane fields",
			"service_managed_identity_resource_id", *identity.ResourceID,
			"managed_identities_data_plane_identity_url", clusterIdentityURL,
			"client_id", s.optionalStringState(identity.ClientID),
			"client_secret", s.optionalSecretState(identity.ClientSecret),
			"tenant_id", s.optionalStringState(identity.TenantID),
			"authentication_endpoint", s.optionalStringState(identity.AuthenticationEndpoint),
		)
		return false, nil
	}
	return true, nil
}

// newFederatedIdentityCredentialsClientGetter returns a getter that builds one
// FederatedIdentityCredentialsClient on first use and reuses it for the rest
// of the reconcile. Callers that skip Azure (PendingDeconfigure when the
// ServiceManagedIdentity is gone) never invoke the getter, so they do not
// fail client construction.
func (s *dataPlaneOIDCFederationSyncer) newFederatedIdentityCredentialsClientGetter(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) func() (azureclient.FederatedIdentityCredentialsClient, error) {
	var federatedIdentityCredentialsClient azureclient.FederatedIdentityCredentialsClient
	return func() (azureclient.FederatedIdentityCredentialsClient, error) {
		if federatedIdentityCredentialsClient != nil {
			return federatedIdentityCredentialsClient, nil
		}
		smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
		client, err := s.smiClientBuilder.FederatedIdentityCredentialsClient(
			ctx,
			cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL,
			smiResourceID,
			cluster.ID.SubscriptionID,
		)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to get Federated Identity Credentials Client: %w", err))
		}
		federatedIdentityCredentialsClient = client
		return federatedIdentityCredentialsClient, nil
	}
}

// buildDesiredFederatedIdentityCredential is the Azure CreateOrUpdate payload for one
// data-plane OIDC federated identity credential.
func (s *dataPlaneOIDCFederationSyncer) buildDesiredFederatedIdentityCredential(issuerURL, subject string) armmsi.FederatedIdentityCredential {
	return armmsi.FederatedIdentityCredential{
		Properties: &armmsi.FederatedIdentityCredentialProperties{
			Issuer:    ptr.To(issuerURL),
			Subject:   ptr.To(subject),
			Audiences: []*string{ptr.To(dataPlaneOIDCFederationAudience)},
		},
	}
}

// generateClusterOIDCIssuerURL builds the OIDC issuer URL as
// <oidcIssuerBaseURL><clusterTenantID>/<csClusterID>. If <oidcIssuerBaseURL> does not end with a slash, it is added at
// the end of it, before <clusterTenantID>.
// This generation matches how Clusters Service generates the OIDC issuer URL. It is important to maintain that
// because that's how Clusters Service calculates the OIDC issuer URL, which is also then used to generate the
// Federated Identity Credentials, as well as the issuer URL set in the OIDC discovery document as well as in Hypershift's
// HostedCluster .spec.issuerURL attribute.
func (s *dataPlaneOIDCFederationSyncer) generateClusterOIDCIssuerURL(clusterTenantID string, csClusterID string) string {
	return fmt.Sprintf("%s%s/%s", s.ensureTrailingSlash(s.oidcIssuerBaseURL), clusterTenantID, csClusterID)
}

// ensureTrailingSlash ensures that the value ends with a slash. If it already does, it returns the value unchanged.
func (s *dataPlaneOIDCFederationSyncer) ensureTrailingSlash(value string) string {
	if strings.HasSuffix(value, "/") {
		return value
	}
	return fmt.Sprintf("%s/", value)
}

func (s *dataPlaneOIDCFederationSyncer) optionalStringState(value *string) string {
	if value == nil {
		return "<nil>"
	}
	if len(*value) == 0 {
		return "<empty>"
	}
	return *value
}

func (s *dataPlaneOIDCFederationSyncer) optionalSecretState(value *string) string {
	if value == nil {
		return "<nil>"
	}
	if len(*value) == 0 {
		return "<empty>"
	}
	return "<set>"
}
