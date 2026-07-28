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
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	fetchDataPlaneOperatorsManagedIdentitiesInfoControllerName = "FetchDataPlaneOperatorsManagedIdentitiesInfo"

	// dataPlaneOperatorsManagedIdentitiesRecheckInterval is the base interval
	// before re-querying Azure for ClientID/PrincipalID when the desired set of
	// identities is already fully resolved. Combined with
	// dataPlaneOperatorsManagedIdentitiesRecheckJitter via wait.Jitter.
	dataPlaneOperatorsManagedIdentitiesRecheckInterval = 60 * time.Second
	dataPlaneOperatorsManagedIdentitiesRecheckJitter   = 0.5
)

// fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer reconciles
// ServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities from the
// cluster's configured data plane operator managed identities.
type fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient database.ResourcesDBClient

	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

var _ controllerutils.ClusterSyncer = (*fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer)(nil)

// NewFetchDataPlaneOperatorsManagedIdentitiesInfoController creates a cluster-watching
// controller that keeps ServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities
// in sync with the cluster's CustomerProperties data plane operator managed identities.
//
// On each sync it:
//  1. Reads every operator -> ResourceID entry from
//     Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators.
//  2. Via needsWork, skips Azure calls when EarliestRecheckTime is still in the
//     future. EarliestRecheckTime is shared across every entry in the Identities map.
//  3. Otherwise uses the cluster's Service Managed Identity to call Azure
//     UserAssignedIdentitiesClient Get for each ResourceID and resolve ClientID
//     and PrincipalID.
//  4. Rebuilds Status.DataPlaneOperatorsManagedIdentities.Identities as a full
//     desired map keyed by ResourceID (operator name, ResourceID, ClientID,
//     PrincipalID). Entries that are no longer present on the cluster are pruned.
//     Identities whose Azure Get fails or returns empty ClientID/PrincipalID are
//     omitted from the written map and surfaced as sync errors so the controller
//     retries. This means that if for some reason the SPC had an entry with the
//     info and the API had an issue and/or stopped returning some of the expected
//     info we delete the entry from the SPC.
//  5. On a fully successful Azure fetch (no per-identity sync errors), sets
//     EarliestRecheckTime on the in-memory replacement to now plus a long
//     jittered interval. On per-identity sync errors, leaves EarliestRecheckTime
//     nil so a successful write clears any prior recheck window and the next
//     reconcile retries immediately.
//  6. Writes the ServiceProviderCluster only when the desired status differs.
//     needsWork only observes EarliestRecheckTime from Cosmos, so a wait is
//     introduced only after a successful Replace persists it. If Replace fails
//     (or hits a precondition failure), the new EarliestRecheckTime is not
//     stored; the workqueue requeues and the next needsWork still sees the
//     previously persisted value (typically nil or already past), so the
//     controller does not wait out the long recheck interval after write
//     failures either.
//
// Continuous reconciliation is required because in the future we might support updating the identity assigned
// to an operator.
//
// clock is used for EarliestRecheckTime comparisons and scheduling; pass nil for
// utilsclock.RealClock{}.
func NewFetchDataPlaneOperatorsManagedIdentitiesInfoController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	backendInformers informers.BackendInformers,
	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
		clock:             clock,
		resourcesDBClient: resourcesDBClient,
		smiClientBuilder:  smiClientBuilder,
	}

	controller := controllerutils.NewClusterWatchingController(
		fetchDataPlaneOperatorsManagedIdentitiesInfoControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)

	return controller
}

// needsWork reports whether Azure should be queried for data plane operator
// managed identity metadata. It returns false when the cluster is deleting, or
// when the EarliestRecheckTime persisted on the ServiceProviderCluster is still
// in the future. A nil EarliestRecheckTime means recheck immediately.
func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) needsWork(
	cluster *api.HCPOpenShiftCluster,
	spc *api.ServiceProviderCluster,
) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	earliestRecheckTime := spc.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime
	if earliestRecheckTime != nil && c.clock.Now().Before(earliestRecheckTime.Time) {
		return false
	}

	return true
}

func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// TODO unclear if we should put the data plane operators managed identities info in the ServiceProviderCluster resource or in
	// the HCPCluster resource. For now we put it in the ServiceProviderCluster resource.
	// Maybe on the Cluster's ServiceProviderProperties because it would ensure that we can leverage ETag to ensure we calculated from
	// the content of the Cluster?
	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if !c.needsWork(existingCluster, existingServiceProviderCluster) {
		return nil
	}

	type identityToSync struct {
		ResourceID   *azcorearm.ResourceID
		OperatorName string
	}

	identitiesToSync := []*identityToSync{}
	for operatorName, dataPlaneOperatorResourceID := range existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators {
		identitiesToSync = append(identitiesToSync, &identityToSync{
			ResourceID:   dataPlaneOperatorResourceID,
			OperatorName: operatorName,
		})
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.DataPlaneOperatorsManagedIdentities = api.ServiceProviderClusterDataPlaneOperatorsManagedIdentities{
		Identities: make(map[string]*api.ServiceProviderClusterDataPlaneOperatorManagedIdentity),
	}

	smiResourceID := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	userAssignedIdentitiesClient, err := c.smiClientBuilder.UserAssignedIdentitiesClient(ctx, existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, smiResourceID, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get User Assigned Identities Client: %w", err))
	}

	var syncErrors []error
	// TODO as of now if there's any error processing the identity, if it's missing properties or any of the expected info we remove the identity
	// from the SPC. This means that if for some reason the SPC had an entry with the info and the API stopped returning it we are deleting it.
	// Alternatives could be:
	// - Keep what's in the SPC for that entry in that case
	// - Write what we can. This makes us having the PrincipalID and ClientID attributes as pointers instead.
	for _, dataPlaneOperatorIdentityToSync := range identitiesToSync {
		currentMI, err := userAssignedIdentitiesClient.Get(ctx, dataPlaneOperatorIdentityToSync.ResourceID.ResourceGroupName, dataPlaneOperatorIdentityToSync.ResourceID.Name, nil)
		if err != nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to get Data Plane Operator Managed Identity: %w", err)))
			continue
		}

		if currentMI.Properties == nil {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Data Plane Operator Managed Identity %s Properties is nil", dataPlaneOperatorIdentityToSync.ResourceID.String())))
			continue
		}

		// We do not want to sync the identity if it's missing information. That means that if somehow it had all the information
		// and now it doesn't it would remove the identity from the ServiceProviderCluster resource.
		identityMissingInfo := false

		if currentMI.Properties.ClientID == nil || len(*currentMI.Properties.ClientID) == 0 {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Data Plane Operator Managed Identity %s Client ID is nil or empty", dataPlaneOperatorIdentityToSync.ResourceID.String())))
			identityMissingInfo = true
		}

		if currentMI.Properties.PrincipalID == nil || len(*currentMI.Properties.PrincipalID) == 0 {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("unexpected Data Plane Operator Managed Identity %s Principal ID is nil or empty", dataPlaneOperatorIdentityToSync.ResourceID.String())))
			identityMissingInfo = true
		}

		if identityMissingInfo {
			continue
		}

		replacement.Status.DataPlaneOperatorsManagedIdentities.Identities[dataPlaneOperatorIdentityToSync.ResourceID.String()] = &api.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
			ResourceID:   arm.DeepCopyResourceID(dataPlaneOperatorIdentityToSync.ResourceID),
			OperatorName: dataPlaneOperatorIdentityToSync.OperatorName,
			ClientID:     *currentMI.Properties.ClientID,
			PrincipalID:  *currentMI.Properties.PrincipalID,
		}
	}

	// Only schedule the long recheck window after every identity resolved
	// successfully. Leave EarliestRecheckTime nil on Azure fetch errors so a
	// later successful Replace clears any prior window and needsWork retries
	// immediately. The value below is only honored once Replace persists it;
	// a Replace failure leaves Cosmos unchanged, so needsWork does not wait.
	if len(syncErrors) == 0 {
		// TODO we should make the jitter unit testable on the random part so we can unit test outcomes. Should we define a
		// jitterFunc as a dependency `func Jitter(duration time.Duration, maxFactor float64) time.Duration` that we
		// initialize as wait.Jitter?
		recheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(
			dataPlaneOperatorsManagedIdentitiesRecheckInterval,
			dataPlaneOperatorsManagedIdentitiesRecheckJitter,
		)))
		replacement.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime = &recheckAt
	}

	if equality.Semantic.DeepEqual(replacement.Status.DataPlaneOperatorsManagedIdentities, existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities) {
		return errors.Join(syncErrors...)
	}

	_, err = c.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		return errors.Join(syncErrors...)
	}
	if err != nil {
		syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))
	}

	return errors.Join(syncErrors...)
}
