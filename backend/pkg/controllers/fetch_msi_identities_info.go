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
	"k8s.io/utils/ptr"

	"github.com/Azure/msi-dataplane/pkg/dataplane"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const fetchMSIIdentitiesInfoControllerName = "FetchMSIIdentitiesInfo"

// fetchMSIIdentitiesInfoSyncer fetches ClientID and PrincipalID for the
// cluster's MSI-based user-assigned managed identities and writes them onto
// HCPOpenShiftCluster.Identity.UserAssignedIdentities in Cosmos.
type fetchMSIIdentitiesInfoSyncer struct {
	resourcesDBClient database.ResourcesDBClient

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
//  1. Collects every identity resource ID from Identity.UserAssignedIdentities.
//  2. Calls the Managed Identities Data Plane (or the fake client implementation
//     in environments where the real Managed Identities Data Plane service
//     is not available) once with the set of identities.
//  3. Matches each returned credential by ResourceID (case-insensitive.
//     ARM IDs are case-insensitive and response order is not assumed)
//     and sets ClientID and PrincipalID when the dataplane returns
//     non-empty values.
//  4. Replaces the HCPCluster document only when the identity map changed.
//
// It does not stop early when ClientID/PrincipalID are already set, so
// future managed-identity updates can refresh the values.
func NewFetchMSIIdentitiesInfoController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
) controllerutils.Controller {

	syncer := &fetchMSIIdentitiesInfoSyncer{
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

func (c *fetchMSIIdentitiesInfoSyncer) needsWork(existingCluster *api.HCPOpenShiftCluster) bool {
	return existingCluster.ServiceProviderProperties.DeletionTimestamp == nil
}

func (c *fetchMSIIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
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
	// TODO we do not check if the ClientID/PrincipalID is set to stop early. This is because in the future we might allow
	// updating the managed identities. Maybe we could have a case where the resourceid is the same but the clientid/principalid has changed? Is this
	// what we want?
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

	if equality.Semantic.DeepEqual(replacement.Identity.UserAssignedIdentities, existingCluster.Identity.UserAssignedIdentities) {
		return errors.Join(syncErrors...)
	}

	// TODO are we ok with storing this directly in the HCPCluster resource in the .Identity section as that where it needs to be exposed? or do we
	// want to store it in a separate place and sync it at some point in the .identity section? Notice how this is different from the data plane identities
	// where we don't expose that information in the .Identity section or anywhere to the end-user.
	_, err = c.resourcesDBClient.HCPClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName).Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return errors.Join(syncErrors...)
	}
	if err != nil {
		syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to replace HCPCluster: %w", err)))
	}

	return errors.Join(syncErrors...)
}

// findUserAssignedIdentityByResourceID looks up an identity using a
// case-insensitive ResourceID match. ARM resource IDs are case-insensitive
// and the MI dataplane may return a different casing than Cosmos.
// It returns the Cosmos map key (preserving stored casing), the matching
// identity value (or nil if not found), and whether a match was found.
func (c *fetchMSIIdentitiesInfoSyncer) findUserAssignedIdentityByResourceID(identities map[string]*arm.UserAssignedIdentity, resourceIDStr string) (string, *arm.UserAssignedIdentity, bool) {
	for k, v := range identities {
		if strings.EqualFold(k, resourceIDStr) {
			return k, v, true
		}
	}
	return "", nil, false
}
