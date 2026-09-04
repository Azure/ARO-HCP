// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package identity

import (
	"context"
	"fmt"
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

const (
	FetchMSIIdentitiesInfoControllerName = "FetchMSIIdentitiesInfo"

	// msiIdentitiesRecheckInterval is the base interval before re-querying the
	// Managed Identities Data Plane when ClientID/PrincipalID are already
	// resolved. Combined with msiIdentitiesRecheckJitter via wait.Jitter.
	msiIdentitiesRecheckInterval = 12 * time.Hour
	msiIdentitiesRecheckJitter   = 0.5
)

// controlPlaneOperatorIdentityToFetch describes one control-plane operator MSI based
// identity for which extra information should be fetched via the Managed Identities Data Plane.
type controlPlaneOperatorIdentityToFetch struct {
	// resourceID is the ARM resource ID of the user-assigned managed identity.
	// ServiceProviderCluster map key / credential lookups must ToLower the
	// string form because ResourceID.String() is not a stable fully-lowercased key.
	resourceID *azcorearm.ResourceID
}

// msiBasedIdentitiesToFetch holds the MSI-based identities for which extra information
// should be fetched via the Managed Identities Data Plane.
type msiBasedIdentitiesToFetch struct {
	// controlPlaneOperators are the control-plane operator identities to fetch extra information for.
	controlPlaneOperators []*controlPlaneOperatorIdentityToFetch
	// serviceManagedIdentity is the ARM resource ID of the cluster's service
	// managed identity for which extra information should be fetched.
	serviceManagedIdentity *azcorearm.ResourceID
}

// resourceIDStrings returns the ARM resource ID strings for every identity to
// resolve, suitable for a Managed Identities Data Plane credentials request.
func (i msiBasedIdentitiesToFetch) resourceIDStrings() []string {
	resourceIDs := make([]string, 0, len(i.controlPlaneOperators)+1)
	for _, identity := range i.controlPlaneOperators {
		resourceIDs = append(resourceIDs, identity.resourceID.String())
	}

	resourceIDs = append(resourceIDs, i.serviceManagedIdentity.String())

	return resourceIDs
}

// fetchMSIIdentitiesInfoSyncer fetches ClientID and PrincipalID for the
// cluster's MSI-based user-assigned managed identities and writes them onto
// ServiceProviderCluster.Status.MSIManagedIdentities in Cosmos.
type fetchMSIIdentitiesInfoSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	fpaMIdataplaneClientBuilder  azureclient.FPAMIDataplaneClientBuilder
}

var _ controllerutils.ClusterSyncer = (*fetchMSIIdentitiesInfoSyncer)(nil)

// NewFetchMSIIdentitiesInfoController creates a cluster-watching controller
// that resolves ClientID and PrincipalID for every MSI-based identity of
// the cluster and persists them on ServiceProviderCluster.Status.MSIManagedIdentities.
//
// These MSI-based identities are the cluster's control plane operator
// managed identities and the cluster's service managed identity. Their
// resource IDs come from
// CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.
// This controller fills in ClientID and PrincipalID for each one of them.
// To do so, it calls the Managed Identities Data Plane service. In environments
// where the real Managed Identities Data Plane service is not available, a fake
// implementation of the Managed Identities Data Plane client is used, which
// always returns the same information and same set of credentials for all
// requests, independently of which identity is requested. The returned information
// in those environments is the information associated to the "MI Mock" identity.
//
// On each SyncOnce the controller:
//  1. Returns immediately when the cluster is deleting or when its Managed
//     Identities Data Plane identity URL is not yet populated.
//  2. Collects every identity resource ID from
//     CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
//     (control plane operators and service managed identity), de-duplicating
//     control plane operator identities that share a resource ID.
//  3. Via needsWork, skips Managed Identities Data Plane calls when this
//     controller's entry in
//     ServiceProviderCluster.Spec.EarliestRecheckTimesByController (keyed by
//     FetchMSIIdentitiesInfoControllerName) is still in the future AND the
//     identities stored on the ServiceProviderCluster still match the collected
//     desired set. If the desired identities have changed, the recheck time is
//     ignored so the dataplane is queried immediately. The recheck time is shared
//     across every entry in MSIManagedIdentities.ControlPlaneOperatorsIdentities
//     and MSIManagedIdentities.ServiceManagedIdentity.
//  4. Calls the Managed Identities Data Plane (or the fake client implementation
//     in environments where the real Managed Identities Data Plane service
//     is not available) once with the set of identities.
//  5. Matches each returned credential by ResourceID (case-insensitive.
//     ARM IDs are case-insensitive and response order is not assumed)
//     and sets ClientID and PrincipalID when the dataplane returns
//     non-empty values. Resource IDs are stored lowercased in the ServiceProviderCluster.
//  6. On a fully successful dataplane fetch, it sets this controller's entry in
//     Spec.EarliestRecheckTimesByController on the in-memory replacement to now
//     plus a long jittered interval.
//  7. Replaces the ServiceProviderCluster document when the resulting document
//     differs from the one that was read. Reads use the informer cache, and the
//     Replace uses the cached document's etag, so a stale cache results in a
//     precondition failure and a requeue rather than clobbering newer data.
func NewFetchMSIIdentitiesInfoController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}

	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &fetchMSIIdentitiesInfoSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		fpaMIdataplaneClientBuilder:  fpaMIdataplaneClientBuilder,
	}

	controller := controllerutils.NewClusterWatchingController(
		FetchMSIIdentitiesInfoControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)

	return controller
}

// needsWork reports whether the Managed Identities Data Plane should be
// queried for MSI identity metadata. EarliestRecheckTime is honored only when
// the identities stored on the ServiceProviderCluster still match
// desiredIdentitiesToFetch; on mismatch (or if future skip-prerequisites fail),
// it returns true immediately. When identities match, it returns false while
// EarliestRecheckTime is in the future, and true when EarliestRecheckTime is
// nil or already past. Callers must skip needsWork entirely when the cluster is
// deleting.
func (c *fetchMSIIdentitiesInfoSyncer) needsWork(existingServiceProviderCluster *coreapi.ServiceProviderCluster, desiredIdentitiesToFetch *msiBasedIdentitiesToFetch) bool {
	// Only honor EarliestRecheckTime when the desired identity set still matches
	// the ServiceProviderCluster. Any mismatch (or future "must work now"
	// conditions added alongside this check) should fall through to return true
	// and query the dataplane.
	if c.desiredMSIResourceIDsMatchServiceProviderCluster(desiredIdentitiesToFetch, existingServiceProviderCluster) {
		// Desired identity set still matches the ServiceProviderCluster. Honor
		// EarliestRecheckTime so we do not repeatedly query the Managed Identities
		// Data Plane for the same identities. Nil means recheck immediately; a
		// future time means skip work.
		earliestRecheckTime := existingServiceProviderCluster.Spec.EarliestRecheckTimesByController[FetchMSIIdentitiesInfoControllerName]
		if earliestRecheckTime != nil && c.clock.Now().Before(earliestRecheckTime.Time) {
			return false
		}
	}

	return true
}

// desiredMSIResourceIDsMatchServiceProviderCluster reports whether the MSI resource IDs stored on
// the ServiceProviderCluster match desiredIdentitiesToFetch. Comparison is by lowercased resource ID
// presence/equality; ClientID/PrincipalID and operator names are ignored.
func (c *fetchMSIIdentitiesInfoSyncer) desiredMSIResourceIDsMatchServiceProviderCluster(desiredIdentitiesToFetch *msiBasedIdentitiesToFetch, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	serviceProviderClusterMSIManagedIdentities := serviceProviderCluster.Status.MSIManagedIdentities

	serviceProviderClusterServiceManagedIdentity := serviceProviderClusterMSIManagedIdentities.ServiceManagedIdentity

	// If the ServiceProviderCluster service managed identity is nil, the identities do not match, because the cluster should always have a service managed identity.
	if serviceProviderClusterServiceManagedIdentity == nil || serviceProviderClusterServiceManagedIdentity.ResourceID == nil {
		return false
	}
	// If the ServiceProviderCluster service managed identity resource ID does not match the cluster one then the identities do not match.
	if !strings.EqualFold(desiredIdentitiesToFetch.serviceManagedIdentity.String(), serviceProviderClusterServiceManagedIdentity.ResourceID.String()) {
		return false
	}

	// If the number of control plane operators is different, the identities do not match.
	if len(desiredIdentitiesToFetch.controlPlaneOperators) != len(serviceProviderClusterMSIManagedIdentities.ControlPlaneOperatorsIdentities) {
		return false
	}

	for _, identity := range desiredIdentitiesToFetch.controlPlaneOperators {
		// ServiceProviderCluster map keys are lowercased strings. ResourceID.String() may re-canonicalize casing so we lowercase.
		resourceIDStr := strings.ToLower(identity.resourceID.String())
		_, ok := serviceProviderClusterMSIManagedIdentities.ControlPlaneOperatorsIdentities[resourceIDStr]
		if !ok {
			return false
		}
	}

	return true
}

func (c *fetchMSIIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	// The Managed Identities Data Plane identity URL is required to build a
	// dataplane client. It is omitempty and may not be populated yet on freshly
	// created clusters; skip until it is set instead of requeueing forever on an
	// unusable empty URL.
	if len(existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL) == 0 {
		return nil
	}

	existingServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // ServiceProviderCluster doesn't exist yet, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}

	msiBasedIdentitiesToFetch, err := c.collectMSIBasedIdentitiesToFetch(existingCluster)
	if err != nil {
		return err
	}

	if !c.needsWork(existingServiceProviderCluster, msiBasedIdentitiesToFetch) {
		return nil
	}

	identitiesToSyncResourceIDStrs := msiBasedIdentitiesToFetch.resourceIDStrings()

	// On environments where the real Managed Identities Data Plane service is not available, a
	// fake implementation of the Managed Identities Data Plane client is used, which always returns the same information and
	// same set of credentials for all requests, independently of which identity is requested. The returned information is
	// the information associated to the "MI Mock" identity.
	fpaMIDataplaneClient, err := c.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Client: %w", err))
	}

	// We get all the Managed Identities information in a single Managed Identities Data Plane Credentials request to minimize
	// calls to the Managed Identities Data Plane Service.
	fpaMIDataplaneCredentialsRequest := dataplane.UserAssignedIdentitiesRequest{IdentityIDs: identitiesToSyncResourceIDStrs}
	fpaMIDataplaneCredentials, err := fpaMIDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, fpaMIDataplaneCredentialsRequest)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Credentials: %w", err))
	}

	if len(fpaMIDataplaneCredentials.ExplicitIdentities) != len(identitiesToSyncResourceIDStrs) {
		return utils.TrackError(fmt.Errorf("unexpected number of Managed Identities Data Plane Credentials. Expected: %d, Received: %d", len(identitiesToSyncResourceIDStrs), len(fpaMIDataplaneCredentials.ExplicitIdentities)))
	}

	// Index returned credentials by lowercased Resource ID so later lookups are
	// case-insensitive. ARM resource IDs are case-insensitive and the MI dataplane may return a different casing than Cosmos, as well as a
	// different order than how it's been requested.
	returnedCredentialsByLowerResourceID := make(map[string]dataplane.UserAssignedIdentityCredentials, len(fpaMIDataplaneCredentials.ExplicitIdentities))
	for idx, fpaMIDataplaneCredential := range fpaMIDataplaneCredentials.ExplicitIdentities {
		if fpaMIDataplaneCredential.ResourceID == nil || len(*fpaMIDataplaneCredential.ResourceID) == 0 {
			// The MIDataplane service should not return a nil or empty Resource ID. This is the case even when the identity does not exist in Azure.
			// If this occurs, we return an error instead of accumulating it as this is unexpected and should not happen..
			return utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential Resource ID is nil or empty in MI Dataplane service response at index %d (Resource ID %q, Client ID %q, Principal ID %q)",
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
	replacementControlPlaneOperatorsIdentities := make(map[string]*coreapi.ServiceProviderClusterControlPlaneOperatorIdentity, len(msiBasedIdentitiesToFetch.controlPlaneOperators))
	for _, identity := range msiBasedIdentitiesToFetch.controlPlaneOperators {
		resourceIDStr := strings.ToLower(identity.resourceID.String())
		credential, ok := returnedCredentialsByLowerResourceID[resourceIDStr]
		if !ok {
			// The MIDataplane service should return a Resource ID that matches one of the identities requested. That is even if the identity actually does not exist anymore in Azure.
			// If it does not, we return an error instead of accumulating it.
			return utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Resource ID is not found in the cluster's identities", resourceIDStr))
		}
		replacementControlPlaneOperatorsIdentities[resourceIDStr] = &coreapi.ServiceProviderClusterControlPlaneOperatorIdentity{
			ResourceID:  coreapi.DeepCopyResourceID(identity.resourceID),
			ClientID:    credential.ClientID,
			PrincipalID: credential.ObjectID,
		}
	}

	serviceManagedIdentityResourceIDStr := strings.ToLower(msiBasedIdentitiesToFetch.serviceManagedIdentity.String())
	serviceManagedIdentityCredential, ok := returnedCredentialsByLowerResourceID[serviceManagedIdentityResourceIDStr]
	if !ok {
		// The MIDataplane service should return a Resource ID that matches one of the identities requested. That is even if the identity actually does not exist anymore in Azure.
		// If it does not, we return an error instead of accumulating it.
		return utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Resource ID is not found in the cluster's identities", serviceManagedIdentityResourceIDStr))
	}
	replacementServiceManagedIdentity := &coreapi.ServiceProviderClusterServiceManagedIdentity{
		ResourceID:  coreapi.DeepCopyResourceID(msiBasedIdentitiesToFetch.serviceManagedIdentity),
		ClientID:    serviceManagedIdentityCredential.ClientID,
		PrincipalID: serviceManagedIdentityCredential.ObjectID,
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = replacementControlPlaneOperatorsIdentities
	replacement.Status.MSIManagedIdentities.ServiceManagedIdentity = replacementServiceManagedIdentity

	// Set an earliest recheck time for the controller so we do not hit the Managed Identities Data Plane service too often
	// when the desired identity set is unchanged. needsWork ignores this wait when OperatorsAuthentication diverges from
	// the identities stored on the ServiceProviderCluster.
	earliestRecheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(
		msiIdentitiesRecheckInterval,
		msiIdentitiesRecheckJitter,
	)))
	if replacement.Spec.EarliestRecheckTimesByController == nil {
		replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
	}
	replacement.Spec.EarliestRecheckTimesByController[FetchMSIIdentitiesInfoControllerName] = &earliestRecheckAt

	if equality.Semantic.DeepEqual(replacement, existingServiceProviderCluster) {
		return nil
	}

	serviceProviderClusterCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClusterCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// Spec (including any new EarliestRecheckTimesByController entry) was not written.
		// The informer will observe the newer document and requeue.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// collectMSIBasedIdentitiesToFetch returns the control-plane operator identities and
// the service managed identity that should be resolved via the Managed
// Identities Data Plane. Control plane operator identities that share a resource
// ID are de-duplicated so a shared identity is only fetched once.
func (c *fetchMSIIdentitiesInfoSyncer) collectMSIBasedIdentitiesToFetch(cluster *coreapi.HCPOpenShiftCluster) (*msiBasedIdentitiesToFetch, error) {
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
