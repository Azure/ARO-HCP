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
package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

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
	fetchMSIIdentitiesInfoControllerName = "FetchMSIIdentitiesInfo"

	// msiIdentitiesRecheckInterval is the base interval before re-querying the
	// Managed Identities Data Plane when ClientID/PrincipalID are already
	// resolved. Combined with msiIdentitiesRecheckJitter via wait.Jitter.
	msiIdentitiesRecheckInterval = 12 * time.Hour
	msiIdentitiesRecheckJitter   = 0.5
)

// fetchMSIIdentitiesInfoSyncer fetches ClientID and PrincipalID for the
// cluster's MSI-based user-assigned managed identities and writes them onto
// HCPOpenShiftCluster.Identity.UserAssignedIdentities in Cosmos.
type fetchMSIIdentitiesInfoSyncer struct {
	clock                       utilsclock.PassiveClock
	resourcesDBClient           corecosmosstorage.ResourcesDBClient
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder
}

var _ controllerutils.ClusterSyncer = (*fetchMSIIdentitiesInfoSyncer)(nil)

// NewFetchMSIIdentitiesInfoController creates a cluster-watching controller
// that resolves ClientID and PrincipalID for every MSI-based identity of
// the cluster and persists them in the .identity section of the cluster resource
// in Cosmos.
//
// These MSI-based identities are the cluster's control plane operator
// managed identities and the cluster's service managed identity. The
// Frontend stores their resource IDs under Identity.UserAssignedIdentities. This
// controller fills in ClientID. To do so, it calls the Managed Identities Data
// Plane service. In environments where the real Managed Identities Data Plane
// service is not available, a fake implementation of the Managed Identities
// Data Plane client is used, which always returns the same information and same
// set of credentials for all requests, independently on what identity is
// requested. The returned information in those environments is the information
// associated to the "MI Mock" identity.
//
// On each SyncOnce the controller:
//  1. Via needsWork, skips Managed Identities Data Plane calls when
//     ServiceProviderProperties.MSIIdentitiesEarliestRecheckTime is still in the
//     future. That recheck time is shared across every entry in
//     Identity.UserAssignedIdentities.
//  2. Collects every identity resource ID from Identity.UserAssignedIdentities.
//  3. Calls the Managed Identities Data Plane (or the fake client implementation
//     in environments where the real Managed Identities Data Plane service
//     is not available) once with the set of identities.
//  4. Matches each returned credential by ResourceID (case-insensitive.
//     ARM IDs are case-insensitive and response order is not assumed)
//     and sets ClientID and PrincipalID when the dataplane returns
//     non-empty values.
//  5. On a fully successful dataplane fetch (no per-identity sync errors), sets
//     MSIIdentitiesEarliestRecheckTime on the in-memory replacement to now plus
//     a long jittered interval. On per-identity sync errors, leaves
//     MSIIdentitiesEarliestRecheckTime nil so a successful write clears any prior
//     recheck window and the next reconcile retries immediately.
//  6. Replaces the HCPCluster document only when the identity map or recheck
//     time changed. needsWork only observes MSIIdentitiesEarliestRecheckTime
//     from Cosmos, so a wait is introduced only after a successful Replace
//     persists it. If Replace fails (or hits a precondition failure), the new
//     MSIIdentitiesEarliestRecheckTime is not stored; the workqueue requeues and
//     the next needsWork still sees the previously persisted value (typically
//     nil or already past), so the controller does not wait out the long recheck
//     interval after write failures either.
//
// clock is used for MSIIdentitiesEarliestRecheckTime comparisons and
// scheduling; pass nil for utilsclock.RealClock{}.
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
		fetchMSIIdentitiesInfoControllerName,
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
// deleting, or when the MSIIdentitiesEarliestRecheckTime persisted on the
// cluster is still in the future. A nil MSIIdentitiesEarliestRecheckTime means
// recheck immediately.
func (c *fetchMSIIdentitiesInfoSyncer) needsWork(existingCluster *coreapi.HCPOpenShiftCluster) bool {
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	earliestRecheckTime := existingCluster.ServiceProviderProperties.MSIIdentitiesEarliestRecheckTime
	if earliestRecheckTime != nil && c.clock.Now().Before(earliestRecheckTime.Time) {
		return false
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

	if !c.needsWork(existingCluster) {
		return nil
	}

	// TODO do we need to check if existingCluster.Identity is nil or are we guaranteed that after Frontend stores to cosmos
	// that section is not nil?
	// TODO do we need to check if existingCluster.Identity.UserAssignedIdentities is nil or are we guaranteed that after Frontend stores to cosmos
	// that section is not nil?
	var identitiesToSyncResourceIDStrs []string
	for identityResourceIDStr := range existingCluster.Identity.UserAssignedIdentities {
		identitiesToSyncResourceIDStrs = append(identitiesToSyncResourceIDStrs, identityResourceIDStr)
	}

	// On environments where the real Managed Identities Data Plane service is not available, a
	// fake implementation of the Managed Identities Data Plane client is used, which always returns the same information and
	// same set of credentials for all requests, independently on what identity is request. The returned information is
	// the information associated to the "MI Mock" identity.
	fpaMIDataplaneClient, err := c.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Client: %w", err))
	}

	// We get all the Managed Identities information in a single Managed Identities Data Plane Credentials to minimize
	// calls to the Managed Identities Data Plane Service.
	fpaMIDataplaneCredentialsRequest := dataplane.UserAssignedIdentitiesRequest{IdentityIDs: identitiesToSyncResourceIDStrs}
	fpaMIDataplaneCredentials, err := fpaMIDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, fpaMIDataplaneCredentialsRequest)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Credentials: %w", err))
	}
	if len(fpaMIDataplaneCredentials.ExplicitIdentities) == 0 {
		return utils.TrackError(fmt.Errorf("returned number of Managed Identities Data Plane Credentials is 0"))
	}

	if len(fpaMIDataplaneCredentials.ExplicitIdentities) != len(identitiesToSyncResourceIDStrs) {
		return utils.TrackError(fmt.Errorf("unexpected number of Managed Identities Data Plane Credentials. Expected: %d, Received: %d", len(identitiesToSyncResourceIDStrs), len(fpaMIDataplaneCredentials.ExplicitIdentities)))
	}

	// TODO at some point we will also have to implement logic that retrieves the initial set of credentials for the
	// control plane operators managed identities and for the service managed identity and store it in the Managed
	// Identities Key Vault (a Management Cluster scoped resource). Do we want to do it here at the same time because
	// we are already calling the Managed Identities Data Plane Service and getting credentials here? As relevant context,
	// these set of initial credentials should be stored in the Managed Identities Key Vault before creating the HostedCluster
	// and those credentials have a limited lifespan (unknown which without investigating further).

	replacement := existingCluster.DeepCopy()

	var syncErrors []error
	for _, fpaMIDataplaneCredential := range fpaMIDataplaneCredentials.ExplicitIdentities {
		if fpaMIDataplaneCredential.ResourceID == nil || len(*fpaMIDataplaneCredential.ResourceID) == 0 {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential Resource ID is nil or empty (Resource ID %q, Client ID %q, Principal ID %q)",
				ptr.Deref(fpaMIDataplaneCredential.ResourceID, ""),
				ptr.Deref(fpaMIDataplaneCredential.ClientID, ""),
				ptr.Deref(fpaMIDataplaneCredential.ObjectID, ""))))
			continue
		}
		credentialResourceID := *fpaMIDataplaneCredential.ResourceID

		// Match case-insensitively: ARM resource IDs are case-insensitive and the
		// MI dataplane may return a different casing (and order) than Cosmos.
		// Keep writing under the Cosmos key so we do not duplicate entries.
		_, replacementIdentity, ok := c.findUserAssignedIdentityByResourceID(replacement.Identity.UserAssignedIdentities, credentialResourceID)
		if !ok {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Resource ID is not found in the cluster's identities", credentialResourceID)))
			continue
		}

		// TODO should we check if existingCluster/replacementIdentity.Identity is nil and initialize it?  or are we guaranteed that after Frontend stores to cosmos
		// that section is not nil?
		// TODO as of now if the returned information from the MIDataplane has nil/empty ClientID/PrincipalID we don't set it in the replacement. Do
		// we want to follow that approach or 1:1 set what's returned from the MIDataplane? That means that if for some reason it's set and the MIDataplane
		// stops setting it we would be unsetting it too.
		if fpaMIDataplaneCredential.ClientID != nil && len(*fpaMIDataplaneCredential.ClientID) > 0 {
			replacementIdentity.ClientID = fpaMIDataplaneCredential.ClientID
		} else {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Client ID is nil or empty", credentialResourceID)))
		}

		if fpaMIDataplaneCredential.ObjectID != nil && len(*fpaMIDataplaneCredential.ObjectID) > 0 {
			replacementIdentity.PrincipalID = fpaMIDataplaneCredential.ObjectID
		} else {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Principal ID is nil or empty", credentialResourceID)))
		}
	}

	// Only schedule the long recheck window after every identity resolved
	// successfully. Leave MSIIdentitiesEarliestRecheckTime nil on dataplane
	// errors so a later successful Replace clears any prior window and
	// needsWork retries immediately. The value below is only honored once
	// Replace persists it; a Replace failure leaves Cosmos unchanged, so
	// needsWork does not wait.
	if len(syncErrors) == 0 {
		recheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(
			msiIdentitiesRecheckInterval,
			msiIdentitiesRecheckJitter,
		)))
		replacement.ServiceProviderProperties.MSIIdentitiesEarliestRecheckTime = &recheckAt
	} else {
		replacement.ServiceProviderProperties.MSIIdentitiesEarliestRecheckTime = nil
	}

	identitiesUnchanged := equality.Semantic.DeepEqual(replacement.Identity.UserAssignedIdentities, existingCluster.Identity.UserAssignedIdentities)
	recheckUnchanged := equality.Semantic.DeepEqual(replacement.ServiceProviderProperties.MSIIdentitiesEarliestRecheckTime, existingCluster.ServiceProviderProperties.MSIIdentitiesEarliestRecheckTime)
	if identitiesUnchanged && recheckUnchanged {
		return errors.Join(syncErrors...)
	}

	// TODO are we ok with storing this directly in the HCPCluster resource in the .Identity section as that where it needs to be exposed? or do we
	// want to store it in a separate place and sync it at some point in the .identity section? Notice how this is different from the data plane identities
	// where we don't expose that information in the .Identity section or anywhere to the end-user.
	_, err = c.resourcesDBClient.HCPClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		// Status (including any new MSIIdentitiesEarliestRecheckTime) was not written.
		// Requeue; needsWork will still see the previously persisted value.
		return errors.Join(syncErrors...)
	}
	if err != nil {
		// Same as precondition failure: MSIIdentitiesEarliestRecheckTime was not
		// persisted, so a write error does not introduce a long recheck wait.
		syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to replace HCPCluster: %w", err)))
	}

	return errors.Join(syncErrors...)
}

// findUserAssignedIdentityByResourceID looks up an identity using a
// case-insensitive ResourceID match. ARM resource IDs are case-insensitive
// and the MI dataplane may return a different casing than Cosmos.
// It returns the Cosmos map key (preserving stored casing), the matching
// identity value (or nil if not found), and whether a match was found.
func (c *fetchMSIIdentitiesInfoSyncer) findUserAssignedIdentityByResourceID(identities map[string]*coreapi.UserAssignedIdentity, resourceIDStr string) (string, *coreapi.UserAssignedIdentity, bool) {
	for k, v := range identities {
		if strings.EqualFold(k, resourceIDStr) {
			return k, v, true
		}
	}
	return "", nil, false
}
