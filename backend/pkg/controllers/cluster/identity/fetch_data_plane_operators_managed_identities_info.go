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
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
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

	// maxRetrievalErrorLength bounds the number of runes persisted in a
	// ServiceProviderClusterDataPlaneOperatorManagedIdentity.RetrievalError so a
	// verbose Azure error cannot bloat the ServiceProviderCluster document.
	maxRetrievalErrorLength = 1024
)

// fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer reconciles
// ServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities from the
// cluster's configured data plane operator managed identities.
type fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient

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
//     future AND the unique ResourceIDs stored on the ServiceProviderCluster still
//     match that desired set. If the desired ResourceIDs have changed,
//     EarliestRecheckTime is ignored so Azure is queried immediately.
//     EarliestRecheckTime is shared across every entry in the Identities map.
//  3. Otherwise uses the cluster's Service Managed Identity to call Azure
//     UserAssignedIdentitiesClient Get once per unique ResourceID and resolve
//     ClientID and PrincipalID.
//  4. Rebuilds Status.DataPlaneOperatorsManagedIdentities.Identities as a full
//     desired map keyed by lowercased ResourceID (ResourceID, ClientID,
//     PrincipalID, RetrievalError). Entries that are no longer present on the
//     cluster are pruned. Every desired ResourceID is written into the map:
//     - ParseResourceID of a set key failing returns immediately without writing.
//     That cannot happen for keys produced from ResourceID.String().
//     - ResourceNotFound keeps the entry, clears ClientID and PrincipalID (nil)
//     and records the error in RetrievalError, so the ServiceProviderCluster still
//     lists the customer-configured identity while signaling that Azure does not
//     currently have it. This is not treated as a sync failure.
//     - Any other Get failure clears ClientID and PrincipalID (nil), records the
//     error in RetrievalError, is accumulated, and processing continues. A
//     successful Get with nil Properties fails the whole sync immediately without
//     writing.
//     - Otherwise ClientID and PrincipalID are written as returned by Azure,
//     including nil or empty values, and RetrievalError is left nil.
//  5. After every identity is processed without a failing Get, sets
//     EarliestRecheckTime on the in-memory replacement to now plus a jittered
//     interval (including when some identities were ResourceNotFound). When any
//     Get failures were accumulated, EarliestRecheckTime is left nil (cleared)
//     and the accumulated error is returned so needsWork keeps returning true and
//     the workqueue retry re-queries Azure.
//  6. Writes the ServiceProviderCluster when the desired status differs, then
//     returns any accumulated Get errors. needsWork observes EarliestRecheckTime
//     and the desired-vs-stored ResourceID match from the informer cache, so a wait is
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

	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		smiClientBuilder:             smiClientBuilder,
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
// is honored only when those ResourceIDs still match the ServiceProviderCluster; on
// mismatch it returns true immediately. When identities match, it returns false while
// EarliestRecheckTime is in the future, and true when EarliestRecheckTime is
// nil or already past. Callers must skip needsWork entirely when the cluster
// is deleting.
func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) needsWork(serviceProviderCluster *coreapi.ServiceProviderCluster, desiredDataPlaneOperatorsResourceIDStrs map[string]struct{}) bool {
	// Only honor EarliestRecheckTime when the desired identity set still matches the
	// ServiceProviderCluster. Any mismatch should fall through to return true and query Azure.
	if c.desiredDataPlaneOperatorResourceIDsMatchServiceProviderCluster(desiredDataPlaneOperatorsResourceIDStrs, serviceProviderCluster) {
		earliestRecheckTime := serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime
		if earliestRecheckTime != nil && c.clock.Now().Before(earliestRecheckTime.Time) {
			return false
		}
	}

	return true
}

func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	existingServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The ServiceProviderCluster has not been created yet. The dedicated
		// CreateServiceProviderCluster controller creates it; we pick it up on a
		// later requeue once it exists.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
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
	// EarliestRecheckTime is intentionally initialized to nil and only set to a future
	// value in the len(errs) == 0 success branch below. On any accumulated Get error it
	// stays nil so needsWork keeps returning true and the workqueue retry re-queries Azure
	// instead of being gated by a stale (possibly future) recheck time persisted alongside
	// a partial update.
	replacement.Status.DataPlaneOperatorsManagedIdentities = coreapi.ServiceProviderClusterDataPlaneOperatorsManagedIdentities{
		Identities:          make(map[string]*coreapi.ServiceProviderClusterDataPlaneOperatorManagedIdentity, len(identitiesToSync)),
		EarliestRecheckTime: nil,
	}

	smiResourceID := existingCluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ServiceManagedIdentity
	if smiResourceID == nil {
		// ServiceManagedIdentity is optional in the cluster model (*azcorearm.ResourceID with
		// omitempty). The SMI client builder dereferences smiResourceID.String() internally, so a
		// nil value would panic and crash the backend process. Return a tracked error instead so the
		// workqueue retries once the cluster's Service Managed Identity is populated.
		return utils.TrackError(fmt.Errorf("cluster ServiceManagedIdentity is nil; cannot resolve data plane operator managed identities"))
	}
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
			// The identity is not found in Azure. Keep the entry so the ServiceProviderCluster
			// still lists the customer-configured identity, but clear ClientID/PrincipalID and
			// record why they are nil in RetrievalError. This is an expected, potentially
			// transient state rather than a sync failure, so it is not accumulated into errs.
			replacementIdentity.ClientID = nil
			replacementIdentity.PrincipalID = nil
			replacementIdentity.RetrievalError = truncateRetrievalError(err.Error())
			continue
		}
		if err != nil {
			// On any other Get failure, clear ClientID/PrincipalID because the previously
			// resolved values are no longer trustworthy, and record the (truncated) error in
			// RetrievalError. Accumulate the failure and keep going so successfully resolved
			// identities can still be persisted; the accumulated error is returned so the
			// workqueue retry re-queries Azure.
			replacementIdentity.ClientID = nil
			replacementIdentity.PrincipalID = nil
			replacementIdentity.RetrievalError = truncateRetrievalError(err.Error())
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to get Data Plane Operator Managed Identity %s: %w", identityResourceIDStr, err)))
			continue
		}

		if currentMI.Properties == nil {
			// The identity should always have properties. If it doesn't, we return an error instead of accumulating it, as this is unexpected and should not happen.
			return utils.TrackError(fmt.Errorf("unexpected Data Plane Operator Managed Identity %s Properties is nil", identityResourceIDStr))
		}

		// For ClientID and PrincipalID of the identity, we set the value returned from the Azure API as is. This includes the cases where the
		// value is nil or empty. RetrievalError is left nil because the retrieval succeeded.
		replacementIdentity.ClientID = currentMI.Properties.ClientID
		replacementIdentity.PrincipalID = currentMI.Properties.PrincipalID
	}

	if len(errs) == 0 {
		// Set an earliest recheck time for the controller so we do not hit the Azure API too often.
		// The value below is only honored once Replace persists it. A Replace failure leaves Cosmos
		// unchanged, so needsWork will still see the previously persisted value (if any).
		// On Get failures we skip this branch entirely: EarliestRecheckTime stays nil (see the
		// replacement initialization above), so needsWork keeps returning true and the workqueue
		// retry re-queries Azure instead of waiting out a stale recheck interval.
		recheckAt := metav1.NewTime(c.clock.Now().Add(wait.Jitter(
			dataPlaneOperatorsManagedIdentitiesRecheckInterval,
			dataPlaneOperatorsManagedIdentitiesRecheckJitter,
		)))
		replacement.Status.DataPlaneOperatorsManagedIdentities.EarliestRecheckTime = &recheckAt
	}

	if !equality.Semantic.DeepEqual(replacement.Status.DataPlaneOperatorsManagedIdentities, existingServiceProviderCluster.Status.DataPlaneOperatorsManagedIdentities) {
		_, err = c.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
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

// uniqueDataPlaneOperatorResourceIDs returns the unique lowercased ResourceID
// strings from desiredDataPlaneOperators. It returns nil if any ResourceID is nil.
func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) uniqueDataPlaneOperatorResourceIDs(desiredDataPlaneOperators map[string]*azcorearm.ResourceID) map[string]struct{} {
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

// desiredDataPlaneOperatorResourceIDsMatchServiceProviderCluster reports whether the
// unique data plane operator managed identity ResourceIDs stored on the
// ServiceProviderCluster match desiredDataPlaneOperatorIdentities.
// desiredDataPlaneOperatorIdentities must already be keyed by lowercased ResourceID.
// Comparison is by ResourceID presence only; ClientID/PrincipalID are ignored.
func (c *fetchDataPlaneOperatorsManagedIdentitiesInfoSyncer) desiredDataPlaneOperatorResourceIDsMatchServiceProviderCluster(desiredDataPlaneOperatorsResourceIDStrs map[string]struct{}, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	serviceProviderClusterIdentities := serviceProviderCluster.Status.DataPlaneOperatorsManagedIdentities.Identities
	if len(desiredDataPlaneOperatorsResourceIDStrs) != len(serviceProviderClusterIdentities) {
		return false
	}

	for resourceIDKey := range desiredDataPlaneOperatorsResourceIDStrs {
		if _, ok := serviceProviderClusterIdentities[resourceIDKey]; !ok {
			return false
		}
	}

	return true
}

// truncateRetrievalError returns a pointer to errMsg truncated to at most
// maxRetrievalErrorLength runes so multi-byte UTF-8 sequences are never split. It bounds
// the size of the per-identity RetrievalError persisted on the ServiceProviderCluster.
func truncateRetrievalError(errMsg string) *string {
	if runes := []rune(errMsg); len(runes) > maxRetrievalErrorLength {
		errMsg = string(runes[:maxRetrievalErrorLength])
	}
	return &errMsg
}
