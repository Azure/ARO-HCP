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
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
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

// identityToFetch describes one MSI identity that should be resolved via the
// Managed Identities Data Plane.
type identityToFetch struct {
	operatorName string
	resourceID   *azcorearm.ResourceID
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
//  1. Via needsWork, skips Managed Identities Data Plane calls when
//     ServiceProviderCluster.Status.MSIManagedIdentities.EarliestRecheckTime is
//     still in the future. That recheck time is shared across every entry in
//     MSIManagedIdentities.Identities.
//  2. Collects every identity resource ID from
//     CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
//     (control plane operators and service managed identity).
//  3. Calls the Managed Identities Data Plane (or the fake client implementation
//     in environments where the real Managed Identities Data Plane service
//     is not available) once with the set of identities.
//  4. Matches each returned credential by ResourceID (case-insensitive.
//     ARM IDs are case-insensitive and response order is not assumed)
//     and sets ClientID and PrincipalID when the dataplane returns
//     non-empty values. Resource IDs are stored lowercased in SPC.
//  5. On a fully successful dataplane fetch, it sets EarliestRecheckTime on the
//     in-memory replacement to now plus a long jittered interval.
//  6. Replaces the ServiceProviderCluster document only when the identities map
//     or recheck time changed. needsWork only observes EarliestRecheckTime from
//     Cosmos, so a wait is introduced only after a successful Replace persists
//     it. If Replace fails (or hits a precondition failure), the new
//     EarliestRecheckTime is not stored. The workqueue requeues and the next
//     needsWork still sees the previously persisted value (typically nil or
//     already past), so the controller does not wait out the long recheck
//     interval after write failures either.
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
// queried for MSI identity metadata. It returns false when the cluster is
// deleting, or when EarliestRecheckTime persisted on the ServiceProviderCluster
// is still in the future. A nil EarliestRecheckTime means recheck immediately.
func (c *fetchMSIIdentitiesInfoSyncer) needsWork(existingCluster *coreapi.HCPOpenShiftCluster, existingSPC *coreapi.ServiceProviderCluster) bool {
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	earliestRecheckTime := existingSPC.Status.MSIManagedIdentities.EarliestRecheckTime
	if earliestRecheckTime != nil && c.clock.Now().Before(earliestRecheckTime.Time) {
		return false
	}

	return true
}

// TODO do we actually want to implement continuous syncing of the identities as of now? Changing this over time
// would have downstream effects and we do not have the support for those other pieces yet.
func (c *fetchMSIIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	spcCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	existingSPC, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // SPC doesn't exist yet, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	if !c.needsWork(existingCluster, existingSPC) {
		return nil
	}

	identitiesToFetch, err := collectIdentitiesToFetch(existingCluster)
	if err != nil {
		return err
	}
	if len(identitiesToFetch) == 0 {
		return nil
	}

	var identitiesToSyncResourceIDStrs []string
	for _, identity := range identitiesToFetch {
		identitiesToSyncResourceIDStrs = append(identitiesToSyncResourceIDStrs, identity.resourceID.String())
	}

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

	credentialsByLowerResourceID := make(map[string]dataplane.UserAssignedIdentityCredentials, len(fpaMIDataplaneCredentials.ExplicitIdentities))
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
		credentialsByLowerResourceID[strings.ToLower(*fpaMIDataplaneCredential.ResourceID)] = fpaMIDataplaneCredential
	}

	replacementIdentities := make(map[string]*coreapi.ServiceProviderClusterMSIManagedIdentity, len(identitiesToFetch))
	for _, identity := range identitiesToFetch {
		lowerResourceIDStr := strings.ToLower(identity.resourceID.String())
		credential, ok := credentialsByLowerResourceID[lowerResourceIDStr]
		if !ok {
			// The MIDataplane service should return a Resource ID that matches one of the identities requested. That is even if the identity actually does not exist anymore in Azure.
			// If it does not, we return an error instead of accumulating it.
			return utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Resource ID is not found in the cluster's identities", identity.resourceID.String()))
		}

		lowerResourceID := metadataapi.Must(azcorearm.ParseResourceID(lowerResourceIDStr))
		// For ClientID and PrincipalID of the identity, we set the value returned from the MIDataplane service. This includes the cases where the
		// value is nil or empty. At the moment of writing this (2026-08-11), when the actual identity does not exist in Azure, the MIDataplane service
		// returns null for ClientID and PrincipalID.
		replacementIdentities[lowerResourceIDStr] = &coreapi.ServiceProviderClusterMSIManagedIdentity{
			OperatorName: identity.operatorName,
			ResourceID:   lowerResourceID,
			ClientID:     credential.ClientID,
			PrincipalID:  credential.ObjectID,
		}
	}

	replacement := existingSPC.DeepCopy()
	replacement.Status.MSIManagedIdentities.Identities = replacementIdentities

	// Set an earliest recheck time for the controller so we do not hit the Managed Identities Data Plane service too often.
	// The value below is only honored once Replace persists it. A Replace failure leaves Cosmos unchanged, so needsWork will still see the
	// previously persisted value (if any).
	// TODO this is more or less reasonable for now because we currently do not support identities replacement, but at the moment we need to support
	// that we will need to change this because we should detect when the identities provided by the end-user are changed. A possibility could be
	// to store the information in a separate field so we can then compare the previous and latest evaluated values and use that as one of the conditions
	// of needsWork.
	earliestRecheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(
		msiIdentitiesRecheckInterval,
		msiIdentitiesRecheckJitter,
	)))
	replacement.Status.MSIManagedIdentities.EarliestRecheckTime = &earliestRecheckAt

	identitiesUnchanged := equality.Semantic.DeepEqual(replacement.Status.MSIManagedIdentities.Identities, existingSPC.Status.MSIManagedIdentities.Identities)
	recheckUnchanged := equality.Semantic.DeepEqual(replacement.Status.MSIManagedIdentities.EarliestRecheckTime, existingSPC.Status.MSIManagedIdentities.EarliestRecheckTime)
	if identitiesUnchanged && recheckUnchanged {
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

// collectIdentitiesToFetch returns the control-plane operator identities and
// the service managed identity that should be resolved via the Managed
// Identities Data Plane.
func collectIdentitiesToFetch(cluster *coreapi.HCPOpenShiftCluster) ([]identityToFetch, error) {
	var identities []identityToFetch

	for operatorName, operatorIdentityResourceID := range cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators {
		if operatorIdentityResourceID == nil {
			continue
		}
		if len(operatorIdentityResourceID.String()) == 0 {
			return nil, utils.TrackError(fmt.Errorf("unexpected empty identity Resource ID string for control plane operator %q", operatorName))
		}
		identities = append(identities, identityToFetch{
			operatorName: operatorName,
			resourceID:   operatorIdentityResourceID,
		})
	}

	if serviceManagedIdentity := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity; serviceManagedIdentity != nil {
		if len(serviceManagedIdentity.String()) == 0 {
			return nil, utils.TrackError(fmt.Errorf("unexpected empty identity Resource ID string for service managed identity"))
		}
		identities = append(identities, identityToFetch{
			operatorName: "",
			resourceID:   serviceManagedIdentity,
		})
	}

	return identities, nil
}
