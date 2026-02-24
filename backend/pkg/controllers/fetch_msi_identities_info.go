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
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/msi-dataplane/pkg/dataplane"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
)

// readAndPersistMaestroReadonlyBundlesContentSyncer is a controller that reads the Maestro readonly bundles
// references stored in the ServiceProviderCluster resource, retrieves the Maestro readonly bundles using those
// references, extracts the content of the Maestro readonly bundles and persists it in Cosmos.
// It is not responsible for creating the Maestro readonly bundles themselves. That is the responsibility of
// the createMaestroReadonlyBundlesSyncer controller.
// Right now we only support reading the content of the Maestro readonly bundle for HostedCluster associated to the cluster.
// In the future we might want to support reading the content of the Maestro readonly bundle for other resources.
type fetchMSIIdentitiesInfoSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	activeOperationLister listers.ActiveOperationLister

	cosmosClient database.DBClient

	clusterServiceClient ocm.ClusterServiceClientSpec

	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder
}

var _ controllerutils.ClusterSyncer = (*fetchMSIIdentitiesInfoSyncer)(nil)

func NewFetchMSIIdentitiesInfoController(
	activeOperationLister listers.ActiveOperationLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	fpaMIdataplaneClientBuilder azureclient.FPAMIDataplaneClientBuilder,
) controllerutils.Controller {

	syncer := &fetchMSIIdentitiesInfoSyncer{
		cooldownChecker:             controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:                cosmosClient,
		clusterServiceClient:        clusterServiceClient,
		activeOperationLister:       activeOperationLister,
		fpaMIdataplaneClientBuilder: fpaMIdataplaneClientBuilder,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ReadAndPersistMaestroReadonlyBundlesContent",
		cosmosClient,
		clusterInformer,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *fetchMSIIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// TODO do we need to check if existingCluster.Identity is nil or are we guaranteed that after Frontend stores to cosmos
	// that section is not nil?
	// TODO do we need to check if existingCluster.Identity.UserAssignedIdentities is nil or are we guaranteed that after Frontend stores to cosmos
	// that section is not nil?
	var identitiesToSync []string
	for identityResourceIDStr, identity := range existingCluster.Identity.UserAssignedIdentities {
		if identity.ClientID == nil || len(*identity.ClientID) == 0 {
			identitiesToSync = append(identitiesToSync, identityResourceIDStr)
		}

		if identity.PrincipalID == nil || len(*identity.PrincipalID) == 0 {
			identitiesToSync = append(identitiesToSync, identityResourceIDStr)
		}
	}

	if len(identitiesToSync) == 0 {
		return nil
	}

	// TODO for now we get the Managed Identities Data Plane Identity URL from the Cluster Service Cluster. In the future
	// we are going to calculate it from the RP and store it in Cosmos.
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster Service Cluster: %w", err))
	}

	// As a relevant note, on environments where the real Managed Identities Data Plane service is not available a
	// fake implementation of the Managed Identities Data Plane client is used, which always returns the information and
	// same set of credentials for all requests. The returned information is the information associated to the "mock MSI" identity.
	fpaMIDataplaneClient, err := c.fpaMIdataplaneClientBuilder.ManagedIdentitiesDataplane(csCluster.Azure().OperatorsAuthentication().ManagedIdentities().ManagedIdentitiesDataPlaneIdentityUrl())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Client: %w", err))
	}

	// We get all the Managed Identities information in a single Managed Identities Data Plane Credentials request because
	// we have been told to minimize calls to the Managed Identities Data Plane Service.
	fpaMIDataplaneCredentialsRequest := dataplane.UserAssignedIdentitiesRequest{
		IdentityIDs: identitiesToSync,
	}
	fpaMIDataplaneCredentials, err := fpaMIDataplaneClient.GetUserAssignedIdentitiesCredentials(ctx, fpaMIDataplaneCredentialsRequest)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Managed Identities Data Plane Credentials: %w", err))
	}

	// TODO at some point we will also have to implement logic that retrieves the initial set of credentials for the
	// control plane operators managed identities and for the service managed identity and store it in the Managed
	// Identities Key Vault (a Management Cluster scoped resource). Do we want to do it here at the same time because
	// we are already calling the Managed Identities Data Plane Service and getting credentials here? As relevant context,
	// these set of initial credentials should be stored in the Managed Identities Key Vault before creating the HostedCluster
	// and those credentials have a limited lifespan (unknown which without investigating further).

	if len(fpaMIDataplaneCredentials.ExplicitIdentities) == 0 {
		return utils.TrackError(fmt.Errorf("returned number of Managed Identities Data Plane Credentials is 0"))
	}

	if len(fpaMIDataplaneCredentials.ExplicitIdentities) != len(identitiesToSync) {
		return utils.TrackError(fmt.Errorf("unexpected number of Managed Identities Data Plane Credentials. Expected: %d, Received: %d", len(identitiesToSync), len(fpaMIDataplaneCredentials.ExplicitIdentities)))
	}

	desiredMSIIdentities := make(map[string]*arm.UserAssignedIdentity)
	var syncErrors []error
	for i, fpaMIDataplaneCredential := range fpaMIDataplaneCredentials.ExplicitIdentities {
		if fpaMIDataplaneCredential.ResourceID == nil || len(*fpaMIDataplaneCredential.ResourceID) == 0 {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Resource ID is nil or empty", identitiesToSync[i])))
			continue
		}
		desiredMSIIdentities[*fpaMIDataplaneCredential.ResourceID] = &arm.UserAssignedIdentity{}
		currentDesiredMSIIdentity := desiredMSIIdentities[*fpaMIDataplaneCredential.ResourceID]

		if fpaMIDataplaneCredential.ClientID != nil && len(*fpaMIDataplaneCredential.ClientID) > 0 {
			currentDesiredMSIIdentity.ClientID = fpaMIDataplaneCredential.ClientID
		} else {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Client ID is nil or empty", identitiesToSync[i])))
		}

		if fpaMIDataplaneCredential.ObjectID != nil && len(*fpaMIDataplaneCredential.ObjectID) > 0 {
			currentDesiredMSIIdentity.PrincipalID = fpaMIDataplaneCredential.ObjectID
		} else {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Managed Identities Data Plane Credential %s Principal ID is nil or empty", identitiesToSync[i])))
		}
	}

	// TODO are we ok with storing this directly in the HCPCluster resource, as it needs to be set anyway because it
	// is returned as part of the API of the Cluster to end-users?
	if !equality.Semantic.DeepEqual(existingCluster.Identity.UserAssignedIdentities, desiredMSIIdentities) && len(desiredMSIIdentities) > 0 {
		// TODO should we check if existingCluster.Identity is nil and initialize it?  or are we guaranteed that after Frontend stores to cosmos
		// that section is not nil?

		for desiredIdentityResourceIDStr, desiredIdentity := range desiredMSIIdentities {
			if desiredIdentity.ClientID != nil && len(*desiredIdentity.ClientID) > 0 {
				existingCluster.Identity.UserAssignedIdentities[desiredIdentityResourceIDStr].ClientID = desiredIdentity.ClientID
			}
			if desiredIdentity.PrincipalID != nil && len(*desiredIdentity.PrincipalID) > 0 {
				existingCluster.Identity.UserAssignedIdentities[desiredIdentityResourceIDStr].PrincipalID = desiredIdentity.PrincipalID
			}
		}

		_, err := c.cosmosClient.HCPClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName).Replace(ctx, existingCluster, nil)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to replace HCPCluster: %w", err)))
		}
	}

	return errors.Join(syncErrors...)
}
