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
	"maps"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const fetchManagedIdentitiesInfoControllerName = "FetchManagedIdentitiesInfo"

type fetchManagedIdentitiesInfoSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	fpaMIdataplaneClientBuilder  azureclient.FPAMIDataplaneClientBuilder
}

func NewManagedIdentitiesInfoController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
) controllerutils.Controller {

	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &fetchManagedIdentitiesInfoSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		fpaMIdataplaneClientBuilder:  fpaMIdataplaneClientBuilder,
	}

	controller := controllerutils.NewClusterWatchingController(
		fetchManagedIdentitiesInfoControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (s *fetchManagedIdentitiesInfoSyncer) needsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	// TODO fix to only honor EarliestRecheckTime when the desired identity set still matches the
	// ServiceProviderCluster. Any mismatch should fall through to return true and query Azure.
	if serviceProviderCluster.Status.ManagedIdentitiesEarliestRecheckTime != nil && s.clock.Now().Before(serviceProviderCluster.Status.ManagedIdentitiesEarliestRecheckTime.Time) {
		return false
	}

	return true
}

func (s *fetchManagedIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
	_ = logger // TODO: use logger

	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	existingServiceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The ServiceProviderCluster has not been created yet. The dedicated
		// CreateServiceProviderCluster controller creates it; we pick it up on a
		// later requeue once it exists.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	if !s.needsWork(existingServiceProviderCluster) {
		return nil
	}

	var errs []error

	msiBasedIdentityDetails, toContinue, err := s.syncMSIBasedIdentities(ctx, existingCluster)
	if err != nil {
		errs = append(errs, err)
	}
	// TODO decide if this is the mechanism we want (the problem is that if there's intermittent error we don't want to unset triggering deconfiguration of further steps)
	if !toContinue {
		return errors.Join(errs...)
	}

	dataPlaneOperatorIdentityDetails, success, err := s.syncDataPlaneOperatorsIdentities(ctx, existingCluster)
	if err != nil {
		errs = append(errs, err)
	}
	if !success {
		return errors.Join(errs...)
	}

	managedIdentityDetails := make(map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails, len(msiBasedIdentityDetails)+len(dataPlaneOperatorIdentityDetails))
	maps.Copy(managedIdentityDetails, msiBasedIdentityDetails)
	maps.Copy(managedIdentityDetails, dataPlaneOperatorIdentityDetails)

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.ManagedIdentityDetails = managedIdentityDetails

	if len(errs) == 0 {
		// Set an earliest recheck time for the controller so we do not hit the Azure API too often.
		// The value below is only honored once Replace persists it. A Replace failure leaves Cosmos
		// unchanged, so needsWork will still see the previously persisted value (if any).
		// On Get failures we skip this branch entirely: EarliestRecheckTime stays nil (see the
		// replacement initialization above), so needsWork keeps returning true and the workqueue
		// retry re-queries Azure instead of waiting out a stale recheck interval.
		recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(
			dataPlaneOperatorsManagedIdentitiesRecheckInterval,
			dataPlaneOperatorsManagedIdentitiesRecheckJitter,
		)))
		replacement.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime = &recheckAt
	}

	if !equality.Semantic.DeepEqual(replacement.Status.DataPlaneOperatorsManagedIdentities, existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities) {
		_, err = s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			// Status (including any new Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime) was not written.
			// needsWork will still see the previously persisted value.
			return errors.Join(errs...)
		}
		if err != nil {
			// Same as precondition failure: Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime was not
			// persisted, so needsWork will still see the previously persisted value.
			return errors.Join(append(errs, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))...)
		}
	}

	return errors.Join(errs...)

}

func (s *fetchManagedIdentitiesInfoSyncer) syncMSIBasedIdentities(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) (map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails, bool, error) {
	msiBasedIdentityDetails := map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{}

	msiBasedIdentitiesToFetch, err := s.collectMSIBasedIdentitiesToFetch(cluster)
	if err != nil {
		return nil, false, err
	}

	for _, identity := range msiBasedIdentitiesToFetch.controlPlaneOperators {
		msiBasedIdentityDetails[coreapi.ManagedIdentityDetailsKey{ResourceID: identity.resourceID.String(), MSIBasedDetails: true}] = &coreapi.ManagedIdentityDetails{ResourceID: identity.resourceID}
	}

	msiBasedIdentityDetails[coreapi.ManagedIdentityDetailsKey{ResourceID: msiBasedIdentitiesToFetch.serviceManagedIdentity.String(), MSIBasedDetails: true}] = &coreapi.ManagedIdentityDetails{ResourceID: msiBasedIdentitiesToFetch.serviceManagedIdentity}

	// TODO fill in the rest of the MSIBasedIdentityDetails

	// On environments where the real Managed Identities Data Plane service is not available, a
	// fake implementation of the Managed Identities Data Plane client is used, which always returns the same information and
	// same set of credentials for all requests, independently of which identity is requested. The returned information is
	// the information associated to the "MI Mock" identity.
	fpaMIDataplaneClient, err := s.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(cluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL)
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Client: %w", err))
	}

	// We get all the Managed Identities information in a single Managed Identities Data Plane Credentials request to minimize
	// calls to the Managed Identities Data Plane Service.
	identitiesToSyncResourceIDStrs := msiBasedIdentitiesToFetch.resourceIDStrings()
	fpaMIDataplaneCredentialsRequest := dataplane.UserAssignedIdentitiesRequest{IdentityIDs: identitiesToSyncResourceIDStrs}
	fpaMIDataplaneCredentials, err := fpaMIDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, fpaMIDataplaneCredentialsRequest)
	if err != nil {
		return nil, false, utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Credentials: %w", err))
	}

	if len(fpaMIDataplaneCredentials.ExplicitIdentities) != len(identitiesToSyncResourceIDStrs) {
		return nil, false, utils.TrackError(fmt.Errorf("unexpected number of Managed Identities Data Plane Credentials. Expected: %d, Received: %d", len(identitiesToSyncResourceIDStrs), len(fpaMIDataplaneCredentials.ExplicitIdentities)))
	}

	// Index returned credentials by lowercased Resource ID so later lookups are
	// case-insensitive. ARM resource IDs are case-insensitive and the MI dataplane may return a different casing than Cosmos, as well as a
	// different order than how it's been requested.
	returnedCredentialsByLowerResourceID := make(map[string]dataplane.UserAssignedIdentityCredentials, len(fpaMIDataplaneCredentials.ExplicitIdentities))
	for idx, fpaMIDataplaneCredential := range fpaMIDataplaneCredentials.ExplicitIdentities {
		if fpaMIDataplaneCredential.ResourceID == nil || len(*fpaMIDataplaneCredential.ResourceID) == 0 {
			// The MIDataplane service should not return a nil or empty Resource ID. This is the case even when the identity does not exist in Azure.
			// If this occurs, we return an error instead of accumulating it as this is unexpected and should not happen..
			return nil, false, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential Resource ID is nil or empty in MI Dataplane service response at index %d (Resource ID %q, Client ID %q, Principal ID %q)",
				idx,
				ptr.Deref(fpaMIDataplaneCredential.ResourceID, ""),
				ptr.Deref(fpaMIDataplaneCredential.ClientID, ""),
				ptr.Deref(fpaMIDataplaneCredential.ObjectID, ""),
			))
		}
		returnedCredentialsByLowerResourceID[strings.ToLower(*fpaMIDataplaneCredential.ResourceID)] = fpaMIDataplaneCredential
	}

	// For ClientID and PrincipalID of each identity, we set the value returned from the MIDataplane service. This includes
	// the cases where the value is nil or empty. At the moment of writing this (2026-08-11), when the actual identity does
	// not exist in Azure, the MIDataplane service returns null for ClientID and PrincipalID.
	// ServiceProviderCluster map keys are lowercased; ResourceID.String() may re-canonicalize casing, so always ToLower for keys.
	for _, identity := range msiBasedIdentityDetails {
		resourceIDStr := strings.ToLower(identity.ResourceID.String())
		credential, ok := returnedCredentialsByLowerResourceID[resourceIDStr]
		if !ok {
			// The MIDataplane service should return a Resource ID that matches one of the identities requested. That is even if the identity actually does not exist anymore in Azure.
			// If it does not, we return an error instead of accumulating it.
			return nil, false, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Resource ID is not found in the cluster's identities", resourceIDStr))
		}
		identity.ClientID = credential.ClientID
		identity.PrincipalID = credential.ObjectID
		identity.TenantID = credential.TenantID
	}

	return msiBasedIdentityDetails, true, nil
}

func (s *fetchManagedIdentitiesInfoSyncer) syncDataPlaneOperatorsIdentities(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) (map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails, bool, error) {
	DataPlaneOperatorIdentityDetails := map[coreapi.ManagedIdentityDetailsKey]*coreapi.ManagedIdentityDetails{}

	dataPlaneOperatorResourceIDs := s.uniqueDataPlaneOperatorResourceIDs(cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators)

	for resourceIDStr := range dataPlaneOperatorResourceIDs {
		resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
		if err != nil {
			// We should never get a nil ResourceID from uniqueDataPlaneOperatorResourceIDs because it's built from
			// the Cluster's customer properties which should have been validated beforehand. Because of this, we return an error instead of accumulating.
			return nil, false, utils.TrackError(fmt.Errorf("failed to parse Data Plane Operator Managed Identity ResourceID %s: %w", resourceIDStr, err))
		}

		DataPlaneOperatorIdentityDetails[coreapi.ManagedIdentityDetailsKey{ResourceID: resourceIDStr, MSIBasedDetails: false}] = &coreapi.ManagedIdentityDetails{ResourceID: resourceID}
	}

	return DataPlaneOperatorIdentityDetails, true, nil
}

// collectMSIBasedIdentitiesToFetch returns the control-plane operator identities and
// the service managed identity that should be resolved via the Managed
// Identities Data Plane. Control plane operator identities that share a resource
// ID are de-duplicated so a shared identity is only fetched once.
func (c *fetchManagedIdentitiesInfoSyncer) collectMSIBasedIdentitiesToFetch(cluster *coreapi.HCPOpenShiftCluster) (*msiBasedIdentitiesToFetch, error) {
	identities := &msiBasedIdentitiesToFetch{}

	// Multiple control plane operators may reference the same user-assigned
	// identity, so de-duplicate by lowercased resource ID. Otherwise the
	// request/response count check and desiredMSIResourceIDsMatchServiceProviderCluster's
	// length comparison (against the lowercased-keyed stored map) would never converge.
	seenControlPlaneOperatorResourceIDs := map[string]struct{}{}
	for operatorName, operatorIdentityResourceID := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		if len(operatorName) == 0 {
			return nil, utils.TrackError(fmt.Errorf("unexpected empty operator name for control plane operator"))
		}
		if operatorIdentityResourceID == nil {
			return nil, utils.TrackError(fmt.Errorf("unexpected nil identity Resource ID string for control plane operator %q", operatorName))
		}

		lowerResourceIDStr := strings.ToLower(operatorIdentityResourceID.String())
		if _, alreadySeen := seenControlPlaneOperatorResourceIDs[lowerResourceIDStr]; alreadySeen {
			continue
		}
		seenControlPlaneOperatorResourceIDs[lowerResourceIDStr] = struct{}{}

		identities.controlPlaneOperators = append(identities.controlPlaneOperators, &controlPlaneOperatorIdentityToFetch{
			resourceID: coreapi.DeepCopyResourceID(operatorIdentityResourceID),
		})
	}

	serviceManagedIdentity := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	if serviceManagedIdentity == nil {
		return nil, utils.TrackError(fmt.Errorf("unexpected nil identity Resource ID for service managed identity"))
	}
	identities.serviceManagedIdentity = coreapi.DeepCopyResourceID(serviceManagedIdentity)

	return identities, nil
}

// uniqueDataPlaneOperatorResourceIDs returns the unique lowercased ResourceID
// strings from desiredDataPlaneOperators. It returns nil if any ResourceID is nil.
func (s *fetchManagedIdentitiesInfoSyncer) uniqueDataPlaneOperatorResourceIDs(desiredDataPlaneOperators map[string]*azcorearm.ResourceID) map[string]struct{} {
	unique := make(map[string]struct{}, len(desiredDataPlaneOperators))
	for _, resourceID := range desiredDataPlaneOperators {
		if resourceID == nil {
			// The desired set is not fully resolved yet. Return nil so callers
			// (SyncOnce checks identitiesToSync == nil) fail safely and retry,
			// as documented, instead of dereferencing a nil ResourceID.
			return nil
		}
		unique[strings.ToLower(resourceID.String())] = struct{}{}
	}
	return unique
}
