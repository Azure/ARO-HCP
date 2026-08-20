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
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	fetchDataPlaneOperatorsManagedIdentitiesInfoControllerName = "FetchDataPlaneOperatorsManagedIdentitiesInfo"

	// dataPlaneOperatorsManagedIdentitiesRecheckInterval is the base interval
	// before re-querying Azure for ClientID/PrincipalID when the desired set of
	// identities is already fully resolved. Combined with
	// dataPlaneOperatorsManagedIdentitiesRecheckJitter via wait.Jitter.
	dataPlaneOperatorsManagedIdentitiesRecheckInterval = 12 * time.Hour
	dataPlaneOperatorsManagedIdentitiesRecheckJitter   = 0.5
)

// fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer reconciles
// ServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities from the
// cluster's configured data plane operator managed identities.
type fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer struct {
	clock             utilsclock.PassiveClock
	resourcesDBClient corecosmosstorage.ResourcesDBClient

	smiClientBuilder azureclient.ServiceManagedIdentityClientBuilder
}

var _ controllerutils.ClusterSyncer = (*fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer)(nil)

// NewFetchDataPlaneOperatorsManagedIdentitiesInfoController creates a cluster-watching
// controller that keeps ServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities
// in sync with the cluster's CustomerProperties data plane operator managed identities.
//
// On each sync it:
//  1. Reads every operator -> ResourceID entry from
//     Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
//     and deduplicates by lowercased ResourceID (multiple operators may share one
//     identity).
//  2. Via needsWork, skips Azure calls when EarliestRecheckTime is still in the
//     future AND the unique ResourceIDs stored on SPC still match that desired
//     set. If the desired ResourceIDs have changed, EarliestRecheckTime is
//     ignored so Azure is queried immediately. EarliestRecheckTime is shared
//     across every entry in the Identities map.
//  3. Otherwise uses the cluster's Service Managed Identity to call Azure
//     UserAssignedIdentitiesClient Get once per unique ResourceID and resolve
//     ClientID and PrincipalID.
//  4. Rebuilds Status.DataPlaneOperatorsManagedIdentities.Identities as a full
//     desired map keyed by lowercased ResourceID (ResourceID, ClientID,
//     PrincipalID). Entries that are no longer present on the cluster are pruned.
//     Every desired ResourceID is written into the map:
//     - ParseResourceID of a set key failing returns immediately without writing.
//     That cannot happen for keys produced from ResourceID.String().
//     - ResourceNotFound keeps the entry and sets ClientID and PrincipalID to
//     nil, so the SPC still lists the customer-configured identity while
//     signaling that Azure does not currently have it.
//     - Any other Get failure is accumulated and processing continues. The entry
//     keeps any previously resolved ClientID/PrincipalID from the existing SPC
//     when present, otherwise leaves them unset. A successful Get with nil
//     Properties fails the whole sync immediately without writing.
//     - Otherwise ClientID and PrincipalID are written as returned by Azure,
//     including nil or empty values.
//  5. After every identity is processed without a failing Get, sets
//     EarliestRecheckTime on the in-memory replacement to now plus a jittered
//     interval (including when some identities were ResourceNotFound). When any
//     Get failures were accumulated, the existing EarliestRecheckTime is left
//     unchanged (nil or already past) and the accumulated error is returned so
//     the workqueue retries.
//  6. Writes the ServiceProviderCluster when the desired status differs, then
//     returns any accumulated Get errors. needsWork observes EarliestRecheckTime
//     and the desired-vs-stored ResourceID match from Cosmos, so a wait is
//     introduced only after a successful Replace persists a matching set with a
//     future EarliestRecheckTime. If Replace fails (or hits a precondition
//     failure), the new EarliestRecheckTime is not stored; the workqueue requeues
//     and the next needsWork still sees the previously persisted value (typically
//     nil or already past, or a mismatched identity set), so the controller does
//     not wait out the recheck interval after write failures either.
func NewFetchDataPlaneOperatorsManagedIdentitiesInfoController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
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
// managed identity metadata. desiredDataPlaneOperatorIdentities must already be
// the unique lowercased ResourceID set from CustomerProperties. EarliestRecheckTime
// is honored only when those ResourceIDs still match SPC; on mismatch it returns
// true immediately. When identities match, it returns false while
// EarliestRecheckTime is in the future, and true when EarliestRecheckTime is
// nil or already past. Callers must skip needsWork entirely when the cluster
// is deleting.
func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) needsWork(spc *coreapi.ServiceProviderCluster, desiredDataPlaneOperatorsResourceIDStrs map[string]struct{}) bool {
	// Only honor EarliestRecheckTime when the desired identity set still matches
	// SPC. Any mismatch should fall through to return true and query Azure.
	if c.desiredDataPlaneOperatorResourceIDsMatchSPC(desiredDataPlaneOperatorsResourceIDStrs, spc) {
		earliestRecheckTime := spc.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime
		if earliestRecheckTime != nil && c.clock.Now().Before(earliestRecheckTime.Time) {
			return false
		}
	}

	return true
}

func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	existingServiceProviderCluster, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	desiredDataPlaneOperators := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
	identitiesToSync := c.uniqueDataPlaneOperatorResourceIDs(desiredDataPlaneOperators)
	if identitiesToSync == nil {
		return utils.TrackError(fmt.Errorf("data plane operator managed identity ResourceID is nil"))
	}
	if !c.needsWork(existingServiceProviderCluster, identitiesToSync) {
		return nil
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.DataPlaneOperatorsManagedIdentities = coreapi.ServiceProviderClusterDataPlaneOperatorsManagedIdentities{
		Identities:          make(map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity, len(identitiesToSync)),
		EarliestRecheckTime: existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime.DeepCopy(),
	}

	smiResourceID := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	userAssignedIdentitiesClient, err := c.smiClientBuilder.UserAssignedIdentitiesClient(ctx, existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL, smiResourceID, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get User Assigned Identities Client: %w", err))
	}

	errs := []error{}
	for identityResourceIDStr := range identitiesToSync {
		resourceID, err := azcorearm.ParseResourceID(identityResourceIDStr)
		if err != nil {
			// We should never get a nil ResourceID from uniqueDataPlaneOperatorResourceIDs because it's built from
			// the Cluster's customer properties which should have been validated beforehand. Because of this, we return an error instead of accumulating.
			return utils.TrackError(fmt.Errorf("failed to parse Data Plane Operator Managed Identity ResourceID %s: %w", identityResourceIDStr, err))
		}

		replacementIdentity := &coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity{
			ResourceID: resourceID,
		}
		replacement.Status.DataPlaneOperatorsManagedIdentities.Identities[identityResourceIDStr] = replacementIdentity

		currentMI, err := userAssignedIdentitiesClient.Get(ctx, resourceID.ResourceGroupName, resourceID.Name, nil)
		if azureclient.IsResourceNotFoundErr(err) {
			// If the identity is not found, we still keep the identity in the resource but we set the ClientID and PrincipalID to nil. In this way, we keep
			// the same set of data plane operator managed identities resource IDs in the customer properties but we signal that the identity is missing
			// by setting the ClientID and PrincipalID to nil.
			replacementIdentity.ClientID = nil
			replacementIdentity.PrincipalID = nil
			continue
		}
		if err != nil {
			// Accumulate Get failures and keep going so successfully resolved identities
			// can still be persisted. Preserve any previously resolved ClientID/PrincipalID
			// so a transient failure does not wipe known values.
			if existingIdentity := existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities[identityResourceIDStr]; existingIdentity != nil {
				replacementIdentity.ClientID = existingIdentity.ClientID
				replacementIdentity.PrincipalID = existingIdentity.PrincipalID
			}
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to get Data Plane Operator Managed Identity %s: %w", identityResourceIDStr, err)))
			continue
		}

		if currentMI.Properties == nil {
			// The identity should always have properties. If it doesn't, we return an error instead of accumulating it, as this is unexpected and should not happen.
			return utils.TrackError(fmt.Errorf("unexpected Data Plane Operator Managed Identity %s Properties is nil", identityResourceIDStr))
		}

		// For ClientID and PrincipalID of the identity, we set the value returned from the Azure API as is. This includes the cases where the
		// value is nil or empty.
		replacementIdentity.ClientID = currentMI.Properties.ClientID
		replacementIdentity.PrincipalID = currentMI.Properties.PrincipalID
	}

	if len(errs) == 0 {
		// Set an earliest recheck time for the controller so we do not hit the Azure API too often.
		// The value below is only honored once Replace persists it. A Replace failure leaves Cosmos unchanged, so needsWork will still see the
		// previously persisted value (if any).
		// On Get failures we skip this and keep the DeepCopied existing EarliestRecheckTime
		// (nil or already past), then return the accumulated error so the workqueue retries.
		// needsWork ignores EarliestRecheckTime when desired DataPlaneOperators ResourceIDs
		// no longer match SPC, so identity replacement is detected without waiting out the gate.
		recheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(
			dataPlaneOperatorsManagedIdentitiesRecheckInterval,
			dataPlaneOperatorsManagedIdentitiesRecheckJitter,
		)))
		replacement.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime = &recheckAt
	}

	if !equality.Semantic.DeepEqual(replacement.Status.DataPlaneOperatorsManagedIdentities, existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities) {
		_, err = c.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			// Status (including any new DataPlaneOperatorsManagedIdentitiesEarliestRecheckTime) was not written.
			// needsWork will still see the previously persisted value.
			return errors.Join(errs...)
		}
		if err != nil {
			// Same as precondition failure: DataPlaneOperatorsManagedIdentitiesEarliestRecheckTime was not
			// persisted, so needsWork will still see the previously persisted value.
			return errors.Join(append(errs, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))...)
		}
	}

	return errors.Join(errs...)
}

// uniqueDataPlaneOperatorResourceIDs returns the unique lowercased ResourceID
// strings from desiredDataPlaneOperators. It returns nil if any ResourceID is nil.
func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) uniqueDataPlaneOperatorResourceIDs(desiredDataPlaneOperators map[string]*azcorearm.ResourceID) map[string]struct{} {
	unique := make(map[string]struct{}, len(desiredDataPlaneOperators))
	for _, resourceID := range desiredDataPlaneOperators {
		unique[strings.ToLower(resourceID.String())] = struct{}{}
	}
	return unique
}

// desiredDataPlaneOperatorResourceIDsMatchSPC reports whether the unique data
// plane operator managed identity ResourceIDs stored on SPC match
// desiredDataPlaneOperatorIdentities. desiredDataPlaneOperatorIdentities must
// already be keyed by lowercased ResourceID. Comparison is by ResourceID
// presence only; ClientID/PrincipalID are ignored.
func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) desiredDataPlaneOperatorResourceIDsMatchSPC(desiredDataPlaneOperatorsResourceIDStrs map[string]struct{}, spc *coreapi.ServiceProviderCluster) bool {
	spcIdentities := spc.Status.DataPlaneOperatorsManagedIdentities.Identities
	if len(desiredDataPlaneOperatorsResourceIDStrs) != len(spcIdentities) {
		return false
	}

	for resourceIDKey := range desiredDataPlaneOperatorsResourceIDStrs {
		if _, ok := spcIdentities[resourceIDKey]; !ok {
			return false
		}
	}

	return true
}
