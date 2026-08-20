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
	// SPC map key / credential lookups must ToLower the string form because
	// ResourceID.String() is not a stable fully-lowercased key.
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
	clock                       utilsclock.PassiveClock
	resourcesDBClient           corecosmosstorage.ResourcesDBClient
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder
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
// requests, independently on what identity is requested. The returned information
// in those environments is the information associated to the "MI Mock" identity.
//
// On each SyncOnce the controller:
//  1. Returns immediately when the cluster is deleting.
//  2. Collects every identity resource ID from
//     CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
//     (control plane operators and service managed identity).
//  3. Via needsWork, skips Managed Identities Data Plane calls when
//     ServiceProviderCluster.Status.MSIManagedIdentities.EarliestRecheckTime is
//     still in the future AND the identities stored on SPC still match the
//     collected desired set. If the desired identities have changed,
//     EarliestRecheckTime is ignored so the dataplane is queried immediately.
//     The recheck time is shared across every entry in
//     MSIManagedIdentities.ControlPlaneOperatorsIdentities and
//     MSIManagedIdentities.ServiceManagedIdentity.
//  4. Calls the Managed Identities Data Plane (or the fake client implementation
//     in environments where the real Managed Identities Data Plane service
//     is not available) once with the set of identities.
//  5. Matches each returned credential by ResourceID (case-insensitive.
//     ARM IDs are case-insensitive and response order is not assumed)
//     and sets ClientID and PrincipalID when the dataplane returns
//     non-empty values. Resource IDs are stored lowercased in SPC.
//  6. On a fully successful dataplane fetch, it sets EarliestRecheckTime on the
//     in-memory replacement to now plus a long jittered interval.
//  7. Replaces the ServiceProviderCluster document only when the identities map
//     or recheck time changed. needsWork observes EarliestRecheckTime and the
//     desired-vs-stored identity match from Cosmos, so a wait is introduced
//     only after a successful Replace persists a matching set with a future
//     EarliestRecheckTime. If Replace fails (or hits a precondition failure),
//     the new EarliestRecheckTime is not stored. The workqueue requeues and the
//     next needsWork still sees the previously persisted value (typically nil
//     or already past, or a mismatched identity set), so the controller does
//     not wait out the long recheck interval after write failures either.
func NewFetchMSIIdentitiesInfoController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}

	syncer := &fetchMSIIdentitiesInfoSyncer{
		clock:                       clock,
		resourcesDBClient:           resourcesDBClient,
		fpaMIdataplaneClientBuilder: fpaMIdataplaneClientBuilder,
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
// the identities stored on SPC still match desiredIdentitiesToFetch; on
// mismatch (or if future skip-prerequisites fail), it returns true immediately.
// When identities match, it returns false while EarliestRecheckTime is in the
// future, and true when EarliestRecheckTime is nil or already past. Callers
// must skip needsWork entirely when the cluster is deleting.
func (c *fetchMSIIdentitiesInfoSyncer) needsWork(existingSPC *coreapi.ServiceProviderCluster, desiredIdentitiesToFetch *msiBasedIdentitiesToFetch) bool {
	// Only honor EarliestRecheckTime when the desired identity set still matches
	// SPC. Any mismatch (or future "must work now" conditions added alongside
	// this check) should fall through to return true and query the dataplane.
	if c.desiredMSIResourceIDsMatchSPC(desiredIdentitiesToFetch, existingSPC) {
		// Desired identity set still matches SPC. Honor EarliestRecheckTime so we
		// do not repeatedly query the Managed Identities Data Plane for the same
		// identities. Nil means recheck immediately; a future time means skip work.
		earliestRecheckTime := existingSPC.Status.MSIManagedIdentities.EarliestRecheckTime
		if earliestRecheckTime != nil && c.clock.Now().Before(earliestRecheckTime.Time) {
			return false
		}
	}

	return true
}

// desiredMSIResourceIDsMatchSPC reports whether the MSI resource IDs stored on
// SPC match desiredIdentitiesToFetch. Comparison is by lowercased resource ID
// presence/equality; ClientID/PrincipalID and operator names are ignored.
func (c *fetchMSIIdentitiesInfoSyncer) desiredMSIResourceIDsMatchSPC(desiredIdentitiesToFetch *msiBasedIdentitiesToFetch, spc *coreapi.ServiceProviderCluster) bool {
	spcMSIManagedIdentities := spc.Status.MSIManagedIdentities

	spcServiceManagedIdentity := spcMSIManagedIdentities.ServiceManagedIdentity

	// If the SPC service managed identity is nil, the identities do not match, because the cluster should always have a service managed identity.
	if spcServiceManagedIdentity == nil || spcServiceManagedIdentity.ResourceID == nil {
		return false
	}
	// If the SPC service managed identity resource ID does not match the cluster one then the identities do not match.
	if !strings.EqualFold(desiredIdentitiesToFetch.serviceManagedIdentity.String(), spcServiceManagedIdentity.ResourceID.String()) {
		return false
	}

	// If the number of control plane operators is different, the identities do not match.
	if len(desiredIdentitiesToFetch.controlPlaneOperators) != len(spcMSIManagedIdentities.ControlPlaneOperatorsIdentities) {
		return false
	}

	for _, identity := range desiredIdentitiesToFetch.controlPlaneOperators {
		// SPC map keys are lowercased strings. ResourceID.String() may re-canonicalize casing so we lowercase.
		resourceIDStr := strings.ToLower(identity.resourceID.String())
		_, ok := spcMSIManagedIdentities.ControlPlaneOperatorsIdentities[resourceIDStr]
		if !ok {
			return false
		}
	}

	return true
}

func (c *fetchMSIIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	spcCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	existingSPC, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // SPC doesn't exist yet, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	msiBasedIdentitiesToFetch, err := c.collectMSIBasedIdentitiesToFetch(existingCluster)
	if err != nil {
		return err
	}

	if !c.needsWork(existingSPC, msiBasedIdentitiesToFetch) {
		return nil
	}

	identitiesToSyncResourceIDStrs := msiBasedIdentitiesToFetch.resourceIDStrings()

	// On environments where the real Managed Identities Data Plane service is not available, a
	// fake implementation of the Managed Identities Data Plane client is used, which always returns the same information and
	// same set of credentials for all requests, independently on what identity is request. The returned information is
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
	// SPC map keys are lowercased; ResourceID.String() may re-canonicalize casing, so always ToLower for keys.
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

	replacement := existingSPC.DeepCopy()
	replacement.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities = replacementControlPlaneOperatorsIdentities
	replacement.Status.MSIManagedIdentities.ServiceManagedIdentity = replacementServiceManagedIdentity

	// Set an earliest recheck time for the controller so we do not hit the Managed Identities Data Plane service too often
	// when the desired identity set is unchanged. needsWork ignores this wait when OperatorsAuthentication diverges from
	// the identities stored on SPC. The value below is only honored once Replace persists it. A Replace failure leaves
	// Cosmos unchanged, so needsWork will still see the previously persisted value (if any).
	earliestRecheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(
		msiIdentitiesRecheckInterval,
		msiIdentitiesRecheckJitter,
	)))
	replacement.Status.MSIManagedIdentities.EarliestRecheckTime = &earliestRecheckAt

	controlPlaneOperatorsUnchanged := equality.Semantic.DeepEqual(replacement.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities, existingSPC.Status.MSIManagedIdentities.ControlPlaneOperatorsIdentities)
	serviceManagedIdentityUnchanged := equality.Semantic.DeepEqual(replacement.Status.MSIManagedIdentities.ServiceManagedIdentity, existingSPC.Status.MSIManagedIdentities.ServiceManagedIdentity)
	recheckUnchanged := equality.Semantic.DeepEqual(replacement.Status.MSIManagedIdentities.EarliestRecheckTime, existingSPC.Status.MSIManagedIdentities.EarliestRecheckTime)
	if controlPlaneOperatorsUnchanged && serviceManagedIdentityUnchanged && recheckUnchanged {
		return nil
	}

	_, err = spcCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// Status (including any new EarliestRecheckTime) was not written.
		// needsWork will still see the previously persisted value.
		return nil
	}
	if err != nil {
		// Same as precondition failure: EarliestRecheckTime was not
		// persisted, so needsWork will still see the previously persisted value.
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// collectMSIBasedIdentitiesToFetch returns the control-plane operator identities and
// the service managed identity that should be resolved via the Managed
// Identities Data Plane.
func (c *fetchMSIIdentitiesInfoSyncer) collectMSIBasedIdentitiesToFetch(cluster *coreapi.HCPOpenShiftCluster) (*msiBasedIdentitiesToFetch, error) {
	identities := &msiBasedIdentitiesToFetch{}

	for operatorName, operatorIdentityResourceID := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		if len(operatorName) == 0 {
			return nil, utils.TrackError(fmt.Errorf("unexpected empty operator name for control plane operator"))
		}
		if operatorIdentityResourceID == nil {
			return nil, utils.TrackError(fmt.Errorf("unexpected nil identity Resource ID string for control plane operator %q", operatorName))
		}

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
