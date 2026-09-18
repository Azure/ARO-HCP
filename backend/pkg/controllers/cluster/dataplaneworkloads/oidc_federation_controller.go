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
// credentials for operators under
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation.
//
// For operators that are still desired (DeconfigureTimestamp nil), FICs are
// created for every Kubernetes service account listed in the cluster-scoped
// identities config. Operators whose EnsuredIdentity matches the parent
// TargetIdentity are rechecked on
// Spec.EarliestRecheckTimesByController[DataPlaneOIDCFederation]
// (or immediately when the desired FIC set changes, pending extras remain,
// the operator is not yet ensured, or a deconfigure is ready). Loop 1 only adds FIC IDs to
// PendingAzureResources so a crash after CreateOrUpdate cannot lose tracking.
// Operators that have left DataPlaneOperators for this identity are skipped
// until intent stamps DeconfigureTimestamp. Deconfigure deletes the union of
// that operator's AzureResources and PendingAzureResources after 24h on a live
// cluster, or immediately when DeletionTimestamp is set. Cluster deletion
// skips ensure. Configure needs a cluster service ID to generate credential
// names. Deconfigure skips Azure deletes if Managed Identities Data Plane
// reports the ServiceManagedIdentity is gone, and still removes the operator.
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
// assignment this pass.
func (s *dataPlaneOIDCFederationSyncer) identityNeedsWork(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
	timeNow time.Time,
	earliestRecheckTime *metav1.Time,
) bool {
	for operatorName, operatorStatus := range status.Operators {
		if s.operatorNeedsWork(cluster, identityResourceIDStr, status, operatorName, operatorStatus, csClusterID, timeNow, earliestRecheckTime) {
			return true
		}
	}
	return false
}

// operatorNeedsWork reports whether this operator would cause the identity
// to need work. Immediate work is a ready deconfigure, a desired operator
// that is not ensured, or a desired FIC set that differs from AzureResources
// (including obsolete IDs that remain only on PendingAzureResources).
// An idle desired operator still needs work when the controller recheck is
// due. Draining operators inside the 24h wait, cluster deletion (except
// ready deconfigure), operators that have left this identity, and a missing
// cluster service ID do not.
func (s *dataPlaneOIDCFederationSyncer) operatorNeedsWork(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	operatorName string,
	operatorStatus *coreapi.DataplaneOIDCFederationOperatorStatus,
	csClusterID string,
	timeNow time.Time,
	earliestRecheckTime *metav1.Time,
) bool {
	if operatorStatus.DeconfigureTimestamp != nil {
		return s.deconfigureCanStartForOperator(cluster, operatorStatus)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}
	// If the operator is not assigned to the identity based on the DataPlaneOperators map, we consider it as not needing work.
	if !s.operatorAssignedToIdentity(cluster, identityResourceIDStr, operatorName) {
		return false
	}
	if len(csClusterID) == 0 {
		return false
	}
	if !status.OperatorEnsured(operatorName) || s.desiredOperatorFederatedIdentityCredentialsSetDiffers(cluster, identityResourceIDStr, operatorName, operatorStatus, csClusterID) {
		return true
	}
	return earliestRecheckTime == nil || timeNow.Compare(earliestRecheckTime.Time) >= 0
}

func (s *dataPlaneOIDCFederationSyncer) operatorAssignedToIdentity(cluster *coreapi.HCPOpenShiftCluster, identityResourceIDStr string, operatorName string) bool {
	assigned := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[operatorName]
	return assigned != nil && strings.EqualFold(assigned.String(), identityResourceIDStr)
}

// desiredOperatorFederatedIdentityCredentialsSetDiffers reports whether the FIC
// IDs that would be generated now for this operator differ from AzureResources,
// or PendingAzureResources still lists IDs that are no longer desired. Pending
// IDs that are still desired are in-flight creates; those already make
// AzureResources differ from desired.
func (s *dataPlaneOIDCFederationSyncer) desiredOperatorFederatedIdentityCredentialsSetDiffers(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	operatorName string,
	operatorStatus *coreapi.DataplaneOIDCFederationOperatorStatus,
	csClusterID string,
) bool {
	desired, err := s.pendingConfigureResourceIDsForOperator(cluster, identityResourceIDStr, operatorName, csClusterID)
	if err != nil {
		return true
	}
	desired = s.uniqueSortedResourceIDs(desired)
	if controllerutil.NeedsUpdate(s.uniqueSortedResourceIDs(operatorStatus.AzureResources), desired) {
		return true
	}
	return len(s.resourceIDsNotIn(operatorStatus.PendingAzureResources, desired)) > 0
}

// deconfigureCanStartForOperator reports whether deconfigure can start for an
// operator with DeconfigureTimestamp set. Cluster deletion ignores the 24h wait.
func (s *dataPlaneOIDCFederationSyncer) deconfigureCanStartForOperator(cluster *coreapi.HCPOpenShiftCluster, operatorStatus *coreapi.DataplaneOIDCFederationOperatorStatus) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return true
	}
	return !s.clock.Now().Before(operatorStatus.DeconfigureTimestamp.Add(dataPlaneOIDCFederationDeconfigureDelay))
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
		// Loop 1: add desired FIC IDs that are not yet on AzureResources into
		// PendingAzureResources (union with leftover pending). Persist before
		// Azure CreateOrUpdate so a crash cannot lose tracking. Stamped
		// operators and operators that have left this identity are skipped so
		// leftover pending IDs stay for deconfigure.
		for identityResourceIDStr := range replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
			identityFederationStatus := replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[identityResourceIDStr]
			if identityFederationStatus == nil {
				continue
			}
			for operatorName, operatorStatus := range identityFederationStatus.Operators {
				if operatorStatus == nil || operatorStatus.DeconfigureTimestamp != nil {
					continue
				}
				if !s.operatorAssignedToIdentity(existingCluster, identityResourceIDStr, operatorName) {
					continue
				}
				desired, err := s.pendingConfigureResourceIDsForOperator(existingCluster, identityResourceIDStr, operatorName, csClusterID)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				trackedPending := append(append([]*azcorearm.ResourceID{}, desired...), operatorStatus.PendingAzureResources...)
				operatorStatus.PendingAzureResources = s.uniqueSortedResourceIDs(s.resourceIDsNotIn(trackedPending, operatorStatus.AzureResources))
			}
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

	// Loop 2: create, update, or delete federated identity credentials in Azure
	// per operator. Successful deconfigure removes that operator. The identity
	// key is dropped when Operators is empty.
	for identityResourceIDStr := range replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation {
		identityStatus := replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[identityResourceIDStr]

		for operatorName, operatorStatus := range identityStatus.Operators {
			if operatorStatus.DeconfigureTimestamp != nil {
				if !s.deconfigureCanStartForOperator(existingCluster, operatorStatus) {
					continue
				}
				smiExists, err := serviceManagedIdentityExistsGetter()
				if err != nil {
					errs = append(errs, err)
					continue
				}
				err = s.deconfigureOIDCFederationForOperator(ctx, identityResourceIDStr, operatorStatus, smiExists, federatedIdentityCredentialsClientGetter)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				delete(identityStatus.Operators, operatorName)
				continue
			}

			// If we cannot configure the desired OIDC federation, we skip the identity reconciliation.
			if !canConfigureDesiredOIDCFederation {
				continue
			}
			// If the operator is not assigned to the identity based on the DataPlaneOperators map, we skip the identity reconciliation.
			if !s.operatorAssignedToIdentity(existingCluster, identityResourceIDStr, operatorName) {
				continue
			}
			issuerURL := s.generateClusterOIDCIssuerURL(clusterTenantID, csClusterID)
			client, err := federatedIdentityCredentialsClientGetter()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.ensureOIDCFederationForOperator(ctx, existingCluster, identityResourceIDStr, operatorName, identityStatus, operatorStatus, csClusterID, issuerURL, client)
			if err != nil {
				errs = append(errs, err)
			}
		}
		if len(identityStatus.Operators) == 0 {
			delete(replacement.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation, identityResourceIDStr)
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

func (s *dataPlaneOIDCFederationSyncer) pendingConfigureResourceIDsForOperator(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	operatorName string,
	csClusterID string,
) ([]*azcorearm.ResourceID, error) {
	identityResourceID, err := s.parseManagedIdentityResourceID(identityResourceIDStr)
	if err != nil {
		return nil, err
	}
	credentials, err := s.federatedIdentityCredentialsForOperator(cluster, identityResourceID, operatorName, csClusterID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get federated identity credentials for identity %s operator %q: %w", identityResourceID.String(), operatorName, err))
	}
	return s.resourceIDsFromCredentials(credentials), nil
}

func (s *dataPlaneOIDCFederationSyncer) ensureOIDCFederationForOperator(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	operatorName string,
	identityStatus *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	operatorStatus *coreapi.DataplaneOIDCFederationOperatorStatus,
	csClusterID string,
	issuerURL string,
	federatedIdentityCredentialsClient azureclient.FederatedIdentityCredentialsClient,
) error {
	identityResourceID, err := s.parseManagedIdentityResourceID(identityResourceIDStr)
	if err != nil {
		return err
	}

	credentials, err := s.federatedIdentityCredentialsForOperator(cluster, identityResourceID, operatorName, csClusterID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get federated identity credentials for identity %s operator %q: %w", identityResourceID.String(), operatorName, err))
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

	// Delete tracked FICs that are no longer desired for this operator (SA-list
	// shrink, FIC name change).
	tracked := s.uniqueSortedResourceIDs(append(append([]*azcorearm.ResourceID{}, operatorStatus.AzureResources...), operatorStatus.PendingAzureResources...))
	deletedKeys := make(map[string]struct{})
	for _, ficResourceID := range tracked {
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

	// The next* slices are only persisted when this pass had errors. Full
	// success replaces AzureResources with desiredIDs and clears pending.
	// uniqueSorted later collapses IDs that both loops append.
	var nextAzureResources []*azcorearm.ResourceID
	// Previously confirmed IDs: keep if still desired (even when this pass's
	// ensure failed) or if an extra Delete failed (retry next pass).
	for _, ficResourceID := range operatorStatus.AzureResources {
		key := strings.ToLower(ficResourceID.String())
		if _, desired := desiredKeys[key]; desired {
			nextAzureResources = append(nextAzureResources, ficResourceID)
			continue
		}
		if _, deleted := deletedKeys[key]; !deleted {
			nextAzureResources = append(nextAzureResources, ficResourceID)
		}
	}
	// IDs CreateOrUpdate succeeded on this pass. They may have been pending
	// only; without this, a sibling error would persist without recording them.
	for _, credential := range credentials {
		if _, ok := ensuredKeys[strings.ToLower(credential.resourceID.String())]; ok {
			nextAzureResources = append(nextAzureResources, credential.resourceID)
		}
	}

	// Desired IDs not yet on nextAzureResources stay pending. Undesired IDs
	// that Delete failed stay pending too (they may never have been on
	// AzureResources; desired-minus-azure would drop them).
	nextPending := s.resourceIDsNotIn(desiredIDs, nextAzureResources)
	for _, ficResourceID := range tracked {
		key := strings.ToLower(ficResourceID.String())
		if _, desired := desiredKeys[key]; desired {
			continue
		}
		if _, deleted := deletedKeys[key]; deleted {
			continue
		}
		nextPending = append(nextPending, ficResourceID)
	}

	allDesiredEnsured := len(ensuredKeys) == len(credentials)
	if allDesiredEnsured {
		operatorStatus.EnsuredIdentity = ptr.To(identityStatus.TargetIdentity)
	}

	if len(errs) > 0 {
		operatorStatus.AzureResources = s.uniqueSortedResourceIDs(nextAzureResources)
		operatorStatus.PendingAzureResources = s.uniqueSortedResourceIDs(nextPending)
		return errors.Join(errs...)
	}

	operatorStatus.AzureResources = desiredIDs
	operatorStatus.PendingAzureResources = nil
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

// deconfigureOIDCFederationForOperator deletes tracked FICs for one operator on
// one identity. A nil error means the operator can be dropped from Operators.
func (s *dataPlaneOIDCFederationSyncer) deconfigureOIDCFederationForOperator(
	ctx context.Context,
	identityResourceIDStr string,
	operatorStatus *coreapi.DataplaneOIDCFederationOperatorStatus,
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

	federatedIdentityCredentialResourceIDsToDelete := s.uniqueSortedResourceIDs(append(append([]*azcorearm.ResourceID{}, operatorStatus.AzureResources...), operatorStatus.PendingAzureResources...))

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
		operatorStatus.AzureResources = s.uniqueSortedResourceIDs(remaining)
		operatorStatus.PendingAzureResources = nil
		return errors.Join(errs...)
	}

	return nil
}

// federatedIdentityCredentialsForOperator returns the FICs that should exist on
// identityResourceID for one data-plane operator.
func (s *dataPlaneOIDCFederationSyncer) federatedIdentityCredentialsForOperator(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceID *azcorearm.ResourceID,
	operatorName string,
	csClusterID string,
) ([]dataPlaneOIDCFederatedIdentityCredential, error) {
	assignedIdentity := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[operatorName]
	if assignedIdentity == nil || !strings.EqualFold(assignedIdentity.String(), identityResourceID.String()) {
		return nil, nil
	}

	// TODO in case the operator is not found in the cluster scoped identities config, do we want to return an error, or
	// do we want to continue ignoring that operator?
	operatorIdentity, ok := s.clusterScopedIdentitiesConfig.DataPlaneOperatorsIdentities[azure.ClusterOperatorIdentifier(operatorName)]
	if !ok {
		return nil, nil
	}

	var credentials []dataPlaneOIDCFederatedIdentityCredential
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
		return false, utils.TrackError(fmt.Errorf("managed Identities Data Plane returned no credentials for ServiceManagedIdentity %s", smiResourceID.String()))
	}
	if len(resp.ExplicitIdentities) > 1 {
		return false, utils.TrackError(fmt.Errorf("managed Identities Data Plane returned %d credentials for ServiceManagedIdentity %s, expected 1", len(resp.ExplicitIdentities), smiResourceID.String()))
	}

	identity := resp.ExplicitIdentities[0]

	// Currently, the MSI dataplane API returns the resource ID even if the identity does not exist. An error is returned here to give visibility if this behaviour ever changes.
	if identity.ResourceID == nil {
		return false, utils.TrackError(fmt.Errorf("managed Identities Data Plane Credential Resource ID is nil for ServiceManagedIdentity %s", smiResourceID.String()))
	}

	// The MIDataplane shouldn't return resource id different from the requested one. If it does this is unexpected
	// and we return an error.
	if !strings.EqualFold(*identity.ResourceID, smiResourceID.String()) {
		return false, utils.TrackError(fmt.Errorf("managed Identities Data Plane Credential Resource ID %s in the response does not match requested ServiceManagedIdentity %s", *identity.ResourceID, smiResourceID.String()))
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
		if !s.identityAzureIdle(cluster, identityResourceIDStr, status, csClusterID) {
			return false
		}
	}
	return true
}

// identityAzureIdle reports whether this identity has no immediate Azure FIC
// work. If any identity is not idle, the controller recheck time is left in
// place.
func (s *dataPlaneOIDCFederationSyncer) identityAzureIdle(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	csClusterID string,
) bool {
	for operatorName, operatorStatus := range status.Operators {
		if !s.operatorAzureIdle(cluster, identityResourceIDStr, status, operatorName, operatorStatus, csClusterID) {
			return false
		}
	}
	return true
}

// operatorAzureIdle reports whether this operator has no immediate Azure FIC
// work. Ready deconfigure, a desired operator that is not ensured, or a
// desired FIC set that differs from AzureResources or still has pending extras
// (when the cluster service ID is set) are not idle. Draining inside the 24h
// wait, cluster deletion (except ready deconfigure), and operators that have
// left this identity are idle. This is not the inverse of operatorNeedsWork: a
// missing cluster service ID still means not idle when the operator is not
// ensured, and a due controller recheck does not make the operator not idle.
func (s *dataPlaneOIDCFederationSyncer) operatorAzureIdle(
	cluster *coreapi.HCPOpenShiftCluster,
	identityResourceIDStr string,
	status *coreapi.ManagedIdentityDataplaneOIDCFederationStatus,
	operatorName string,
	operatorStatus *coreapi.DataplaneOIDCFederationOperatorStatus,
	csClusterID string,
) bool {
	if operatorStatus.DeconfigureTimestamp != nil {
		return !s.deconfigureCanStartForOperator(cluster, operatorStatus)
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return true
	}
	if !s.operatorAssignedToIdentity(cluster, identityResourceIDStr, operatorName) {
		return true
	}
	if !status.OperatorEnsured(operatorName) {
		return false
	}

	if len(csClusterID) == 0 {
		return true
	}

	if s.desiredOperatorFederatedIdentityCredentialsSetDiffers(cluster, identityResourceIDStr, operatorName, operatorStatus, csClusterID) {
		return false
	}

	return true
}
