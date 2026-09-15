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
	// re-querying Azure for federated identity credentials that are already
	// ensured. Combined with dataPlaneOIDCFederationRecheckJitter via
	// wait.Jitter. The resulting time is stored on
	// Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName].
	dataPlaneOIDCFederationRecheckInterval = 1 * time.Hour
	// dataPlaneOIDCFederationRecheckJitter is the wait.Jitter factor applied to
	// dataPlaneOIDCFederationRecheckInterval when setting this controller's
	// Spec.EarliestRecheckTimesByController entry.
	dataPlaneOIDCFederationRecheckJitter = 0.5

	// dataPlaneOIDCFederationAudience is the Azure AD token-exchange audience required on federated identity credentials.
	dataPlaneOIDCFederationAudience = "openshift"

	// dataPlaneOIDCFederationDeconfigureDelay is how long a live cluster's
	// identity must wait after DeconfigureTimestamp has been set before the actual
	// deconfiguration process starts. When a Cluster deletion has been triggered, this delay is ignored.
	dataPlaneOIDCFederationDeconfigureDelay = 24 * time.Hour
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
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation.
//
// For identities that are still desired (DeconfigureTimestamp nil), each
// identity is mapped to the data-plane operators it is assigned to on the
// Cluster payload. For each of those operators, a federated identity
// credential is created for every Kubernetes service account listed in the
// cluster-scoped identities config. Identities whose TargetIdentity is already
// EnsuredIdentity are rechecked on
// Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederation]
// (or immediately when the desired FIC set changes, TargetIdentity is not yet
// ensured, or a deconfigure is ready). When needsWork is true, every desired
// identity is reconciled this pass: Get each desired FIC and CreateOrUpdate
// only if it is missing or Issuer/Subject/Audiences drifted, and delete
// tracked FICs that are no longer desired while the identity stays desired.
// See ensureFederation for the cases that shrink the desired set.
// Deconfigure (DeconfigureTimestamp set) deletes the credentials already
// tracked on the status (AzureResources and PendingAzureResources). On a live
// cluster Azure deletes wait until 24h after DeconfigureTimestamp. Cluster
// deletion (DeletionTimestamp set) skips ensure so Cluster Service can
// deconfigure FICs without this controller recreating them, and deconfigures
// immediately, including when a wait has not elapsed. New FIC resource
// IDs are persisted to PendingAzureResources before CreateOrUpdate, so a crash
// cannot lose the tracked set. Azure errors on one FIC do not skip the rest:
// remaining creates, updates, and deletes still run, confirmed IDs move to
// AzureResources, and unfinished IDs stay pending so a partial update can be
// persisted. Configure needs a cluster service ID to
// generate credential names. Deconfigure skips Azure deletes
// if Managed Identities Data Plane reports the ServiceManagedIdentity is gone
// (missing ClientID, ClientSecret, TenantID, or AuthenticationEndpoint), and still removes the entry.
type dataPlaneOIDCFederationSyncer struct {
	clock                         utilsclock.PassiveClock
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	subscriptionLister            corelisters.SubscriptionLister
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	smiClientBuilder              azureclient.ServiceManagedIdentityClientBuilder
	fpaMIdataplaneClientBuilder   azureclient.FPAMIDataplaneClientBuilder
	clusterScopedIdentitiesConfig *azure.ClusterScopedIdentitiesConfig
	oidcIssuerBaseURL             string
}

var _ controllerutils.ClusterSyncer = (*dataPlaneOIDCFederationSyncer)(nil)

// NewDataPlaneOIDCFederationController creates a cluster-watching controller
// that configures and deconfigures data-plane OIDC federation on the cluster's
// managed identities using
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation.
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
	_, subscriptionLister := backendInformers.Subscriptions()

	syncer := &dataPlaneOIDCFederationSyncer{
		clock:                         clock,
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		subscriptionLister:            subscriptionLister,
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
	// We get the current time to use it in the identityNeedsWork function. This is so all of them have the same reference time.
	now := s.clock.Now()
	earliestRecheckTime := serviceProviderCluster.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName]

	for identityResourceIDStr, status := range serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		if s.identityNeedsWork(cluster, identityResourceIDStr, status, csClusterID, now, earliestRecheckTime) {
			return true
		}
	}
	return false
}

// identityNeedsWork reports whether this identity would cause the cluster
// to need work. If any identity returns true, SyncOnce reconciles every
// desired identity this pass. Deconfigure is ready when the 24h wait has
// elapsed, or immediately when the cluster is deleting. Ensure is skipped
// while Cluster Service is still tearing down (DeletionTimestamp set,
// DeconfigureTimestamp not yet stamped) so this controller does not
// recreate FICs CS is deleting.
// TODO: remove the DeletionTimestamp ensure skip once Cluster Service no
// longer deconfigures data-plane FICs. Ensure also requires a cluster
// service ID to generate FIC names and the issuer URL.
func (s *dataPlaneOIDCFederationSyncer) identityNeedsWork(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
	timeNow time.Time,
	earliestRecheckTime *metav1.Time,
) bool {
	if status.DeconfigureTimestamp != nil {
		// If the identity has been stamped for deconfiguration, we check if the deconfigure delay has elapsed or if the cluster deletion process has started
		// and we based on that we determine if the identity needs work.
		return s.deconfigureCanStartForIdentity(cluster, status)
	}

	// If the identity has not been stamped for deconfiguration but the cluster deletion process has started, we consider that the identity does not need work.
	// This is to avoid reconciling FederatedIdentityCredentials while the cluster deletion process is ongoing on Cluster Service side:
	// Cluster Service deconfigures FederatedIdentityCredentials during its cluster teardown. Creating or reconciling the identity here
	// while that process is ongoing would race with that. In that case, if the identity has not been marked as deconfigured yet
	// and the aro-hcp cluster deletion process has started we don't consider that the identity needs work.
	// TODO this check should be removed once the data plane identities oidc federation logic has been removed from Cluster Service.
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	// If the cluster service ID is not set, we consider that the identity does not need work. This is because the FederatedIdentityCredentials cannot be created nor reconciled
	// without that information.
	if len(csClusterID) == 0 {
		return false
	}

	// If the target identity is not ensured or the set of desired Azure FederatedIdentityCredential resource IDs that would be generated differs from
	// the FederatedIdentityCredential persisted in AzureResources, we consider that the identity needs work.
	// Note: The desiredFICSetDiffers check allows us to react to changes in the desired set of FederatedIdentityCredential resource IDs. In that case that
	// set can change in the following scenarios:
	// - The identity starts and/or stops being specified as desired in a data plane operator
	// - The K8s ServiceAccount list in the cluster-scoped identities config changes for the data plane operators that have the identity specified as their desired identity
	if !status.TargetIdentityEnsured() || s.desiredFederatedIdentityCredentialsSetDiffers(cluster, identityResourceIDStr, status, csClusterID) {
		return true
	}

	// At this point, this means that the identity is currently considered as reconciled, so we only consider the identity
	// needs work if the earliest recheck time is not set or if it has already passed.
	return earliestRecheckTime == nil || timeNow.Compare(earliestRecheckTime.Time) >= 0
}

// desiredFederatedIdentityCredentialsSetDiffers reports whether the FederatedIdentityCredential resource
// IDs that would be generated now differ from Status.AzureResources, the last
// confirmed set. It is the needsWork bypass for an already-ensured identity
// whose ID list changed while EnsuredIdentity is still set, so the cluster
// does not wait for Spec.EarliestRecheckTimesByController. PendingAzureResources
// is not compared: that list is crash recovery for IDs not yet confirmed, and
// unioning it with AzureResources would hide a desired-set growth that only
// got as far as persisting pending. Issuer, Subject, and Audiences drift is
// not compared; that is the Azure Get in ensureFederation.
func (s *dataPlaneOIDCFederationSyncer) desiredFederatedIdentityCredentialsSetDiffers(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
) bool {
	desired, err := s.pendingConfigureResourceIDs(cluster, identityResourceIDStr, csClusterID)
	if err != nil {
		return true
	}
	// TODO it's a bit odd to use NeedsUpdate. Alternative? does semantic.DeepEqual work correctly? or not because it's azcorearm.ResourceID?
	return controllerutil.NeedsUpdate(s.uniqueSortedResourceIDs(status.AzureResources), s.uniqueSortedResourceIDs(desired))
}

// deconfigureCanStartForIdentity reports whether the deconfiguration process can start for an
// identity with DeconfigureTimestamp set. The deconfiguration process for a identity with DeconfigureTimestamp set
// can start if the cluster deletion has been triggered or if the deconfigure delay has elapsed. If a Cluster deletion
// has been triggered, the deconfigure delay is ignored and the deconfiguration process can start immediately.
func (s *dataPlaneOIDCFederationSyncer) deconfigureCanStartForIdentity(cluster *coreapi.HCPOpenShiftCluster, status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return true
	}

	return !s.clock.Now().Before(status.DeconfigureTimestamp.Time.Add(dataPlaneOIDCFederationDeconfigureDelay))
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

	clusterTenantID, err := s.clusterTenantID(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return err
	}

	// Get the cluster service ID from the cluster. This can be empty if the cluster service is not yet created.
	// At this point we continue even if it's not set yet, because only some part of the logic requires it to be set.
	csClusterID := controllerutils.ClusterServiceIDForCluster(existingCluster)

	replacement := existingServiceProviderCluster.DeepCopy()

	var errs []error

	// Loop 1 overwrites PendingAzureResources with (desired FederatedIdentityCredential resource IDs - AzureResources).
	// Skip that rewrite for the whole cluster when either of the following conditions are met:
	// - Cluster's DeletionTimestamp is set: Cluster Service still deconfigures FederatedIdentityCredential resources during
	//   teardown. Staging new pending IDs would record create intent and race
	//   with that. Existing PendingAzureResources is left unchanged so Loop 2
	//   deconfigure can still delete leftover IDs once CS is gone.
	//   TODO: remove the DeletionTimestamp check once Cluster Service no longer
	//   deconfigures data-plane FederatedIdentityCredential resources.
	// - csClusterID is not set: FederatedIdentityCredential resource IDs include the cluster service ID, so
	//   the desired set cannot be computed.
	//   TODO this check could be removed at some point if we decouple the FederatedIdentityCredential resource IDs from the cluster service ID. That
	//   would only be possible when CS doesn't have oidc federation logic anymore, nor dependencies to the FederatedIdentityCredential resource IDs, and
	//   it would require starting to use a different approach to generate the FederatedIdentityCredential resource IDs, ensuring no collisions when
	//   identity reusing nor within and across clusters.
	canConfigureDesiredOIDCFederation := existingCluster.ServiceProviderProperties.DeletionTimestamp == nil && len(csClusterID) > 0
	if canConfigureDesiredOIDCFederation {
		// Loop 1: calculate the FederatedIdentityCredential resource IDs that are desired but not yet confirmed in Azure.
		// Those are then set in PendingAzureResources, and persisted on the ServiceProviderCluster document if there are changes detected on that attribute
		// compared to the current value on the document. Persisting it there as a first phase ensures that if a crash or replace failure occurs we do not
		// lose the tracked set.
		// After an identity instance change, AzureResources is empty, so that is the full desired set.
		// Identities that have been stamped for deconfiguration are skipped: Loop 1 overwrites PendingAzureResources from
		// the live desired set, which would drop leftover pending IDs Loop 2 still deletes.
		for identityResourceIDStr := range replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
			identityFederationStatus := replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[identityResourceIDStr]
			// Loop 1 overwrites PendingAzureResources from the live desired set.
			// Deconfigure must keep leftover pending IDs so Loop 2 can delete them.
			if identityFederationStatus.DeconfigureTimestamp != nil {
				continue
			}

			desired, err := s.pendingConfigureResourceIDs(existingCluster, identityResourceIDStr, csClusterID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			identityFederationStatus.PendingAzureResources = s.uniqueSortedResourceIDs(s.resourceIDsNotIn(desired, identityFederationStatus.AzureResources))
		}
	}

	// Persist the PendingAzureResources set on the ServiceProviderCluster document if there are changes detected on that attribute
	// compared to the current value on the document. This is done as a first phase to ensure that if a crash or replace failure occurs we do not
	// lose the tracked set.
	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting pending data-plane OIDC federation credentials onto ServiceProviderCluster")

		persistedServiceProviderCluster, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		existingServiceProviderCluster = persistedServiceProviderCluster
		replacement = existingServiceProviderCluster.DeepCopy()
	}

	// We instantiate a FederatedIdentityCredentialsClientGetter that allows us to lazily get a shared FederatedIdentityCredentialsClient instance for this
	// reconcile iteration. This is to avoid creating the client multiple times. Each time a client instance is created based on the cluster's Service Managed Identity
	// an interaction with the Managed Identities Data Plane (or its mock client) is performed in order to get the credentials to authenticate.
	federatedIdentityCredentialsClientGetter := s.newFederatedIdentityCredentialsClientGetter(ctx, existingCluster)
	serviceManagedIdentityExistsGetter := s.newServiceManagedIdentityExistsGetter(ctx, existingCluster)

	// Loop 2: create, update, or delete federated identity credentials in Azure,
	// then update each entry in memory. Azure errors on one FIC do not skip the
	// rest of that identity: remaining creates, updates, and deletes still run,
	// confirmed IDs move to AzureResources, and unfinished IDs stay pending so
	// the persist after this loop can apply a partial update. EnsuredIdentity
	// is copied from TargetIdentity when every desired FIC is ensured.
	// Successful deconfigure removes the identity from the map. Desired
	// identities Get each desired FIC and CreateOrUpdate only when it is
	// missing or Issuer/Subject/Audiences drifted. Cluster deletion skips
	// ensure so Cluster Service can deconfigure FICs without this controller
	// recreating them. Ensured identities also delete tracked FICs that are no
	// longer desired (see ensureFederation). Deconfigure deletes the union of
	// AzureResources and PendingAzureResources. Results are persisted after
	// the loop.
	for identityResourceIDStr := range replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		identityStatus := replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[identityResourceIDStr]

		if identityStatus.DeconfigureTimestamp != nil {
			if !s.deconfigureCanStartForIdentity(existingCluster, identityStatus) {
				continue
			}
			smiExists, err := serviceManagedIdentityExistsGetter()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.deconfigureOIDCFederationForIdentity(ctx, identityResourceIDStr, identityStatus, smiExists, federatedIdentityCredentialsClientGetter)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			// Nil error is success, including when the ServiceManagedIdentity is
			// already gone and Azure FIC deletes were skipped. Keep the map entry
			// only when deconfigure returned an error; the persist after this loop
			// still writes partial Azure progress from failed deletes.
			delete(replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, identityResourceIDStr)
			continue
		}

		// If we cannot configure the desired OIDC federation, we skip the identity reconciliation.
		if !canConfigureDesiredOIDCFederation {
			continue
		}
		issuerURL := s.generateClusterOIDCIssuerURL(clusterTenantID, csClusterID)
		client, err := federatedIdentityCredentialsClientGetter()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		err = s.ensureOIDCFederationForIdentity(ctx, existingCluster, identityResourceIDStr, identityStatus, csClusterID, issuerURL, client)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation) == 0 {
		replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation = nil
	}

	if len(errs) == 0 {
		s.syncFederationRecheckTime(replacement, existingCluster, csClusterID)
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting data-plane OIDC federation configure/deconfigure result onto ServiceProviderCluster")

		_, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return errors.Join(errs...)
		}
		if err != nil {
			return errors.Join(append(errs, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))...)
		}
	}

	return errors.Join(errs...)
}

func (s *dataPlaneOIDCFederationSyncer) pendingConfigureResourceIDs(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	csClusterID string,
) ([]*azcorearm.ResourceID, error) {
	identityResourceID, err := s.parseManagedIdentityResourceID(identityResourceIDStr)
	if err != nil {
		return nil, err
	}
	credentials, err := s.federatedIdentityCredentialsForIdentity(cluster, identityResourceID, csClusterID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get federated identity credentials for identity %s: %w", identityResourceID.String(), err))
	}
	return s.resourceIDsFromCredentials(credentials), nil
}

func (s *dataPlaneOIDCFederationSyncer) ensureOIDCFederationForIdentity(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
	issuerURL string,
	federatedIdentityCredentialsClient azureclient.FederatedIdentityCredentialsClient,
) error {
	identityResourceID, err := s.parseManagedIdentityResourceID(identityResourceIDStr)
	if err != nil {
		return err
	}

	credentials, err := s.federatedIdentityCredentialsForIdentity(cluster, identityResourceID, csClusterID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get federated identity credentials for identity %s: %w", identityResourceID.String(), err))
	}
	desiredIDs := s.uniqueSortedResourceIDs(s.resourceIDsFromCredentials(credentials))
	desiredKeys := make(map[string]struct{}, len(desiredIDs))
	for _, id := range desiredIDs {
		desiredKeys[strings.ToLower(id.String())] = struct{}{}
	}

	ensuredKeys := make(map[string]struct{})
	var errs []error
	for _, credential := range credentials {
		err = s.ensureFederatedIdentityCredentialForIdentity(ctx, federatedIdentityCredentialsClient, identityResourceID, issuerURL, credential)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ensuredKeys[strings.ToLower(credential.resourceID.String())] = struct{}{}
	}

	// Delete FICs still listed on AzureResources that are no longer desired.
	// This is not identity teardown. Deconfigure handles the identity
	// leaving the desired data-plane set (including the last data-plane operator
	// leaving this identity, even if the same UAMI is still CP or SMI:
	// DataPlaneOIDCFederationIntent drops it from the desired set) and cluster
	// deletion. ClientID / PrincipalID / TenantID rotation on the same
	// ResourceID updates TargetIdentity and clears EnsuredIdentity; it does not
	// deconfigure. This loop runs while the identity stays desired because
	// it is still in DataPlaneOperators.
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
		// If the FederatedIdentityCredential resource ID is still in the desired set, we skip the deletion.
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

	// TODO do we want this here, or do we want to make it dependent on len(errs) where we would only set it
	// if len(errs) == 0?
	allDesiredEnsured := len(ensuredKeys) == len(credentials)
	if allDesiredEnsured {
		status.EnsuredIdentity = ptr.To(status.TargetIdentity)
	}

	if len(errs) > 0 {
		status.AzureResources = s.uniqueSortedResourceIDs(nextAzureResources)
		status.PendingAzureResources = s.uniqueSortedResourceIDs(s.resourceIDsNotIn(desiredIDs, status.AzureResources))
		return errors.Join(errs...)
	}

	status.AzureResources = desiredIDs
	status.PendingAzureResources = nil
	return nil
}

func (s *dataPlaneOIDCFederationSyncer) ensureFederatedIdentityCredentialForIdentity(
	ctx context.Context,
	client azureclient.FederatedIdentityCredentialsClient,
	identityResourceID *azcorearm.ResourceID,
	issuerURL string,
	credential dataPlaneOIDCFederatedIdentityCredential,
) error {
	desired := s.buildDesiredFederatedIdentityCredential(credential.resourceID.Name, issuerURL, credential.subject)
	got, err := client.Get(ctx, identityResourceID.ResourceGroupName, identityResourceID.Name, credential.resourceID.Name, nil)
	if err != nil && !azureclient.IsFederatedCredentialNotFoundErr(err) {
		return utils.TrackError(fmt.Errorf("failed to get federated identity credential %s on identity %s: %w", credential.resourceID.String(), identityResourceID.String(), err))
	}
	if err == nil && !s.federatedIdentityCredentialNeedsUpdate(got.Properties, desired.Properties) {
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
		return utils.TrackError(fmt.Errorf("failed to create federated identity credential %s on identity %s: %w", credential.resourceID.String(), identityResourceID.String(), err))
	}

	return nil
}

func (s *dataPlaneOIDCFederationSyncer) federatedIdentityCredentialNeedsUpdate(existing, desired *armmsi.FederatedIdentityCredentialProperties) bool {
	if existing == nil && desired == nil {
		return false
	}
	if existing == nil && desired != nil {
		return true
	}
	if existing != nil && desired == nil {
		return true
	}

	if !ptr.Equal(existing.Issuer, desired.Issuer) {
		return true
	}
	if !ptr.Equal(existing.Subject, desired.Subject) {
		return true
	}

	if !s.audienceSetsEqual(existing.Audiences, desired.Audiences) {
		return true
	}

	return false
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

// deconfigureOIDCFederationForIdentity deletes tracked FICs for one identity.
// A nil error means the identity can be dropped from the federation map:
// either Azure deletes succeeded, or the ServiceManagedIdentity is already
// gone so FIC deletes cannot run (the FIC client is authenticated with SMI
// credentials). FICs on the data-plane UAMI may remain in Azure in that
// second case; this controller stops tracking them.
func (s *dataPlaneOIDCFederationSyncer) deconfigureOIDCFederationForIdentity(
	ctx context.Context,
	identityResourceIDStr string,
	identityStatus *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	serviceManagedIdentityExists bool,
	federatedIdentityCredentialsClientGetter func() (azureclient.FederatedIdentityCredentialsClient, error),
) error {
	identityResourceID, err := s.parseManagedIdentityResourceID(identityResourceIDStr)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse managed identity ResourceID %s: %w", identityResourceIDStr, err))
	}

	if !serviceManagedIdentityExists {
		logger := utils.LoggerFromContext(ctx)
		logger.Info("ServiceManagedIdentity is gone. Skipping federated identity credential deletes and treating data-plane OIDC federation as deconfigured",
			"managedIdentityResourceID", identityResourceIDStr,
		)
		return nil
	}

	federatedIdentityCredentialsClient, err := federatedIdentityCredentialsClientGetter()
	if err != nil {
		return err
	}

	// We get the union of AzureResources and PendingAzureResources to delete all the FederatedIdentityCredential resources that are tracked. This ensures
	// that we delete all the FederatedIdentityCredential resources that are tracked, even if some of them are in PendingAzureResources, to cover the scenario
	// where resources in PendingAzureResources were created but failed to persist afterwards for some reason.
	federatedIdentityCredentialResourceIDsToDelete := s.uniqueSortedResourceIDs(append(append([]*azcorearm.ResourceID{}, identityStatus.AzureResources...), identityStatus.PendingAzureResources...))

	var remaining []*azcorearm.ResourceID
	var errs []error
	for _, federatedIdentityCredentialResourceID := range federatedIdentityCredentialResourceIDsToDelete {
		_, err := federatedIdentityCredentialsClient.Delete(ctx, identityResourceID.ResourceGroupName, identityResourceID.Name, federatedIdentityCredentialResourceID.Name, nil)
		if err != nil && !azureclient.IsFederatedCredentialNotFoundErr(err) && !azureclient.IsFederatedCredentialParentResourceNotFoundErr(err) &&
			!azureclient.IsAzureAuthorizationFailedError(err) {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to delete federated identity credential %s on identity %s: %w", federatedIdentityCredentialResourceID.Name, identityResourceID.String(), err)))
			remaining = append(remaining, federatedIdentityCredentialResourceID)
			continue
		}
	}

	if len(errs) > 0 {
		// TODO are we fine with setting all the failed FederatedIdentityCredential resource deletions in AzureResources only, coming from the union
		// of AzureResources and PendingAzureResources? The reasoning is that if deletion failed is because they existed so if some were in
		// PendingAzureResources they can be moved to AzureResources.
		identityStatus.AzureResources = s.uniqueSortedResourceIDs(remaining)
		identityStatus.PendingAzureResources = nil
		return errors.Join(errs...)
	}

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
			federatedIdentityCredentialResourceID, err := federatedidentitycredential.GenerateFederatedIdentityCredentialResourceID(
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
				resourceID: federatedIdentityCredentialResourceID,
			})
		}
	}

	slices.SortFunc(credentials, func(a, b dataPlaneOIDCFederatedIdentityCredential) int {
		return strings.Compare(strings.ToLower(a.resourceID.String()), strings.ToLower(b.resourceID.String()))
	})

	return credentials, nil
}

func (s *dataPlaneOIDCFederationSyncer) parseManagedIdentityResourceID(identityResourceIDStr string) (*azcorearm.ResourceID, error) {
	identityResourceID, err := azcorearm.ParseResourceID(identityResourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse managed identity ResourceID %s: %w", identityResourceIDStr, err))
	}
	return identityResourceID, nil
}

// clusterTenantID returns the Azure Tenant ID of the cluster's subscription.
func (s *dataPlaneOIDCFederationSyncer) clusterTenantID(ctx context.Context, subscriptionID string) (string, error) {
	subscription, err := s.subscriptionLister.Get(ctx, subscriptionID)
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get Subscription %s: %w", subscriptionID, err))
	}
	if subscription.Properties == nil {
		return "", utils.TrackError(fmt.Errorf("subscription %s has no properties", subscriptionID))
	}

	if subscription.Properties.TenantId == nil {
		return "", utils.TrackError(fmt.Errorf("subscription %s has nil tenantId", subscriptionID))
	}

	if len(*subscription.Properties.TenantId) == 0 {
		return "", utils.TrackError(fmt.Errorf("subscription %s has empty tenantId", subscriptionID))
	}

	return *subscription.Properties.TenantId, nil
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
// The comparison is done by their string representation, case-insensitive.
func (s *dataPlaneOIDCFederationSyncer) uniqueSortedResourceIDs(ids []*azcorearm.ResourceID) []*azcorearm.ResourceID {
	seen := map[string]struct{}{}
	var out []*azcorearm.ResourceID
	for _, id := range ids {
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

// resourceIDsNotIn returns the IDs in ids that are not present in existing. The
// comparison is done by their string representation, case-insensitive.
func (s *dataPlaneOIDCFederationSyncer) resourceIDsNotIn(ids, existing []*azcorearm.ResourceID) []*azcorearm.ResourceID {
	seen := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		seen[strings.ToLower(id.String())] = struct{}{}
	}
	var out []*azcorearm.ResourceID
	for _, id := range ids {
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
	logger := utils.LoggerFromContext(ctx)
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
		logger.Info("ServiceManagedIdentity is missing required Managed Identities Data Plane fields",
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

// newServiceManagedIdentityExistsGetter returns a getter that checks ServiceManagedIdentity
// existence once and reuses that result for the rest of this reconcile. Deconfigure
// needs this to decide whether FIC Deletes can run; calling the Managed Identities
// Data Plane once per identity would repeat the same cluster-level lookup.
func (s *dataPlaneOIDCFederationSyncer) newServiceManagedIdentityExistsGetter(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) func() (bool, error) {
	var (
		checked bool
		exists  bool
		err     error
	)
	return func() (bool, error) {
		if checked {
			return exists, err
		}
		// TODO serviceManagedIdentityExists relies on the usage of the Managed Identities Data Plane Service. Right now
		// in the environments where it does not exist we use the Managed Identities Data Plane mock client that returns the hardcoded identity. Are we fine
		// with that approach or do we want to make it explicit that we are using a hardcoded identity in that case? To be aware, is that probably we
		// don't have permissions to check the existence of the HardcodedIdentity with any identity that we have available in those environments.
		smiResourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
		exists, err = s.serviceManagedIdentityExists(ctx, cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, smiResourceID)
		checked = true
		return exists, err
	}
}

// newFederatedIdentityCredentialsClientGetter returns a getter that builds one
// FederatedIdentityCredentialsClient on first use and returns the same same client
// instance for the rest of the calls to the returned getter.
func (s *dataPlaneOIDCFederationSyncer) newFederatedIdentityCredentialsClientGetter(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) func() (azureclient.FederatedIdentityCredentialsClient, error) {
	var federatedIdentityCredentialsClient azureclient.FederatedIdentityCredentialsClient
	return func() (azureclient.FederatedIdentityCredentialsClient, error) {
		if federatedIdentityCredentialsClient != nil {
			return federatedIdentityCredentialsClient, nil
		}

		// TODO should we use smiClientBuilder, which transparently uses the fake miDataplaneClient, or do we want to make it explicit that the hardcodedidentity
		// is use under the hood? that would create branching here and have to make the controller aware of the Managed Identities Data Plane Service not being available, becoming
		// aware.
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
func (s *dataPlaneOIDCFederationSyncer) buildDesiredFederatedIdentityCredential(name string, issuerURL string, subject string) armmsi.FederatedIdentityCredential {
	return armmsi.FederatedIdentityCredential{
		Name: ptr.To(name),
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

// syncFederationRecheckTime sets Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederation]
// when remaining entries are idle (all ensured, or ensured plus deconfigure
// still inside the 24h wait). An empty federation map deletes the entry:
// there is nothing to re-query. Immediate work remaining (TargetIdentity not
// ensured, ready deconfigure, or ensured FIC-set drift) leaves any existing
// time in place so needsWork stays true until that work finishes.
func (s *dataPlaneOIDCFederationSyncer) syncFederationRecheckTime(replacement *coreapi.ServiceProviderCluster, cluster *coreapi.HCPOpenShiftCluster, csClusterID string) {
	if len(replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation) == 0 {
		delete(replacement.Spec.EarliestRecheckTimesByController, DataPlaneOIDCFederationControllerName)
		return
	}

	if !s.federationAzureIdle(cluster, replacement, csClusterID) {
		return
	}

	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(dataPlaneOIDCFederationRecheckInterval, dataPlaneOIDCFederationRecheckJitter)))
	if replacement.Spec.EarliestRecheckTimesByController == nil {
		replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
	}
	replacement.Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederationControllerName] = &recheckAt
}

// federationAzureIdle reports whether Azure FIC work is finished for this
// cluster aside from ensured identities sleeping until the controller
// recheck and deconfigures still inside the 24h wait.
func (s *dataPlaneOIDCFederationSyncer) federationAzureIdle(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster, csClusterID string) bool {
	for identityResourceIDStr, status := range serviceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		if status.DeconfigureTimestamp != nil {
			if s.deconfigureCanStartForIdentity(cluster, status) {
				return false
			}
			continue
		}
		if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
			continue
		}
		if !status.TargetIdentityEnsured() {
			return false
		}
		if len(csClusterID) > 0 && s.desiredFederatedIdentityCredentialsSetDiffers(cluster, identityResourceIDStr, status, csClusterID) {
			return false
		}
	}
	return true
}
