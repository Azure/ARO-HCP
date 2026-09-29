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

package denyassignments

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ClusterDenyAssignmentControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const ClusterDenyAssignmentControllerName = "ClusterDenyAssignment"

const (
	// clusterDenyAssignmentRecheckInterval is the base interval before
	// re-querying Azure for deny assignments that are already ensured.
	// Combined with clusterDenyAssignmentRecheckJitter via wait.Jitter. The
	// resulting time is stored on
	// Spec.EarliestRecheckTimesByController[ClusterDenyAssignmentControllerName].
	clusterDenyAssignmentRecheckInterval = 1 * time.Hour
	// clusterDenyAssignmentRecheckJitter is the wait.Jitter factor applied to
	// clusterDenyAssignmentRecheckInterval when setting this controller's
	// Spec.EarliestRecheckTimesByController entry.
	clusterDenyAssignmentRecheckJitter = 0.5

	// denyAssignmentExcludedIdentityDeconfigureDelay is how long a live
	// cluster keeps a cooldown principal on ExcludePrincipals after
	// DeconfigureTimestamp. ClusterDenyAssignments may drop older
	// waiters sooner when Azure's 25-principal ExcludePrincipals limit
	// would otherwise block currently desired principals.
	denyAssignmentExcludedIdentityDeconfigureDelay = 24 * time.Hour

	// denyAssignmentExcludePrincipalsLimit is the Azure maximum number of
	// principals on one deny assignment's ExcludePrincipals list. It is a limit
	// imposed by Azure.
	denyAssignmentExcludePrincipalsLimit = 25
)

// clusterDenyAssignmentSyncer creates and deletes Azure deny assignments for
// entries in ServiceProviderCluster.Status.DenyAssignmentsOverManagedResourceGroup.
//
// For types that are still desired (DeconfigureTimestamp nil), the Azure deny
// assignment is created or updated from the live definition. Types whose
// desired principals are EnsuredIdentity and whose EnsuredPermissions match
// are rechecked on
// Spec.EarliestRecheckTimesByController[ClusterDenyAssignment]
// (or immediately when a desired principal is not yet EnsuredIdentity,
// permissions drifted, pending extras remain, the type is not yet ensured, or
// a cooldown wait has elapsed). Loop 1 only sets PendingAzureResource so a
// crash after CreateOrUpdate cannot lose tracking.
// Types that have left the definition set are skipped until intent stamps
// DeconfigureTimestamp. Deconfigure deletes AzureResource and
// PendingAzureResource immediately; there is no 24h wait at the type level.
// Cluster deletion (DeletionTimestamp set) skips all work: deny assignments
// are scoped to the managed resource group, so Azure deletes them in cascade
// when that resource group is removed. Configure and deconfigure wait until
// Status.AzureResources.ManagedResourceGroup.AzureResource is set: that is the
// observed managed resource group they are scoped to.
// ExcludePrincipals is built from ExcludedIdentities rows whose
// DeconfigureTimestamp is nil, plus cooldown rows still inside the 24h wait,
// capped at 25 with oldest waiters dropped first. After a successful PUT,
// EnsuredIdentity is written for the nil-timestamp rows, EnsuredPermissions
// is written from the live definition, and omitted waiters are deleted from
// the map.
type clusterDenyAssignmentSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*clusterDenyAssignmentSyncer)(nil)

// NewClusterDenyAssignmentController creates a cluster-watching controller
// that creates and deletes Azure deny assignments using
// ServiceProviderCluster.Status.DenyAssignmentsOverManagedResourceGroup.
func NewClusterDenyAssignmentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()

	syncer := &clusterDenyAssignmentSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		resourcesDBClient:            resourcesDBClient,
		azureFPAClientBuilder:        azureFPAClientBuilder,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterDenyAssignmentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *clusterDenyAssignmentSyncer) needsWork(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	// If the cluster is being deleted, we skip the deny assignment work. Because the deny assignments are scoped to the managed resource group, when
	// the managed resource group is deleted, the deny assignments are also deleted so no need to do anything in this controller in that case.
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	// If the managed resource group is not yet created, we skip the deny assignment work, as we can't create deny assignments
	// over the Managed Resource Group if it doesn't exist.
	if serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource == nil {
		return false
	}

	now := s.clock.Now()
	earliestRecheckTime := serviceProviderCluster.Spec.EarliestRecheckTimesByController[ClusterDenyAssignmentControllerName]
	definitionsByType := denyAssignmentDefinitionsByType(cluster)

	for denyAssignmentType, status := range serviceProviderCluster.Status.DenyAssignmentsOverManagedResourceGroup {
		if s.denyAssignmentTypeNeedsWork(cluster, denyAssignmentType, status, definitionsByType, now, earliestRecheckTime) {
			return true
		}
	}
	return false
}

// denyAssignmentTypeNeedsWork reports whether this type would cause the cluster to need work.
// Immediate work is a ready type deconfigure, a desired type that is not
// ensured, permissions drift, a desired principal that is not EnsuredIdentity,
// or a cooldown wait that has elapsed. An idle desired type still needs work
// when the controller recheck is due. Types that have left the definition set
// do not.
func (s *clusterDenyAssignmentSyncer) denyAssignmentTypeNeedsWork(
	cluster *coreapi.Cluster,
	denyAssignmentType string,
	status *coreapi.DenyAssignmentStatus,
	definitionsByType map[string]*denyAssignmentDefinition,
	timeNow time.Time,
	earliestRecheckTime *metav1.Time,
) bool {
	// If the deny assignment type itself is being deconfigured, because it is not defined anymore, we immediately start the deconfigure process.
	// TODO do we want this at this level? or to wait a delay? do not confuse with the per identity service principal delay.
	if status.DeconfigureTimestamp != nil {
		return true
	}
	// A nil DeconfigureTimestamp does not mean the type is still required.
	// Intent and this executor are independent: after a type leaves
	// denyAssignmentDefinitions, Cosmos can still hold an unstamped entry
	// until intent's leftover pass stamps or drops it. That leftover does
	// not need work: do not ensure it, and do not deconfigure until intent
	// stamps DeconfigureTimestamp.
	if !s.denyAssignmentTypeStillRequired(cluster, denyAssignmentType) {
		return false
	}

	csClusterID := controllerutils.ClusterServiceIDForCluster(cluster)
	// If the cluster service ID is not calculated yet and the denyassignment type is not marked for deconfiguration, we consider there's
	// no need to work on it, as the deny assignment resource ID cannot be calculated yet as it depends on it.
	if len(csClusterID) == 0 {
		return false
	}

	if s.denyAssignmentTypeNeedsImmediateWork(denyAssignmentType, status, definitionsByType) {
		return true
	}

	// Check if any of the excluded identities need immediate work.
	for _, excludedIdentityStatus := range status.ExcludedIdentities {
		if s.excludedIdentityNeedsImmediateWork(excludedIdentityStatus, timeNow) {
			return true
		}
	}

	return earliestRecheckTime == nil || timeNow.Compare(earliestRecheckTime.Time) >= 0
}

func (s *clusterDenyAssignmentSyncer) excludedIdentityNeedsImmediateWork(status *coreapi.DenyAssignmentExcludedIdentityStatus, now time.Time) bool {
	if status.DeconfigureTimestamp == nil {
		return false
	}
	return !now.Before(status.DeconfigureTimestamp.Add(denyAssignmentExcludedIdentityDeconfigureDelay))
}

func (s *clusterDenyAssignmentSyncer) denyAssignmentTypeStillRequired(cluster *coreapi.Cluster, denyAssignmentType string) bool {
	_, ok := denyAssignmentDefinitionsByType(cluster)[denyAssignmentType]
	return ok
}

func (s *clusterDenyAssignmentSyncer) denyAssignmentTypeNeedsImmediateWork(
	denyAssignmentType string,
	status *coreapi.DenyAssignmentStatus,
	definitionsByType map[string]*denyAssignmentDefinition,
) bool {

	// TODO is this correct?
	// If the deny assignment is persisted but it is not in the definitions, it means
	// it is not desired anymore. But at this point we are checking if it needs immediate work. If it doesn't have
	// DeconfigureTimestamp it means we need to wait for the intent controller to stamp it
	denyAssignmentTypeDefinition, ok := definitionsByType[denyAssignmentType]
	if !ok {
		return false
	}

	if !s.denyAssignmentTypeEnsuredPermissionsMatch(status.EnsuredPermissions, denyAssignmentTypeDefinition) {
		return true
	}

	// TODO do we need something with the array itself of excludedidentities at this level? like for example if the target is nil???
	for principalID, identityStatus := range status.ExcludedIdentities {
		if identityStatus.EnsuredIdentity == nil || identityStatus.EnsuredIdentity.PrincipalID != principalID {
			return true
		}
	}

	return false

}

func (s *clusterDenyAssignmentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	existingServiceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	if !s.needsWork(existingCluster, existingServiceProviderCluster) {
		return nil
	}

	clusterTenantID, err := s.clusterTenantID(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return err
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	timeNow := s.clock.Now()
	var errs []error

	csClusterID := controllerutils.ClusterServiceIDForCluster(existingCluster)
	canConfigureDenyAssignment := len(csClusterID) > 0

	if canConfigureDenyAssignment {
		// Loop 1: persist deny assignment resource IDs that are not yet on the
		// document before any Azure CreateOrUpdate. Stamped types and types that
		// have left the definition set are skipped so leftover pending IDs stay
		// for deconfigure.
		for denyAssignmentType := range replacement.Status.DenyAssignmentsOverManagedResourceGroup {
			status := replacement.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentType]
			if status.DeconfigureTimestamp != nil {
				// If the deny assignment is being deconfigured, we skip it, as we will handle it in the next loop.
				continue
			}

			if !s.denyAssignmentTypeStillRequired(existingCluster, denyAssignmentType) {
				// If the deny assignment is persisted but not in the definitions anymore and it's not been marked for
				// deconfiguration it means that the intent controller has not yet stamped the DeconfigureTimestamp. In that case,
				// we skip doing anything with it in this loop.
				continue
			}

			desiredResourceID, err := generateDenyAssignmentResourceID(existingCluster, denyAssignmentType)
			if err != nil {
				errs = append(errs, err)
				continue
			}

			if controllerutil.ResourceIDsEqual(status.AzureResource, desiredResourceID) {
				status.PendingAzureResource = nil
				continue
			}
			status.PendingAzureResource = desiredResourceID
		}
	}

	// Persist the PendingAzureResource onto the ServiceProviderCluster if there are
	// changes detected on that attribute compared to the current value on the document. This is done
	// as a first phase to ensure that if a crash or replace failure occurs we do not lose the tracked resource.
	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting pending deny assignments onto ServiceProviderCluster")

		persistedServiceProviderCluster, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		existingServiceProviderCluster = persistedServiceProviderCluster
		replacement = existingServiceProviderCluster.DeepCopy()
	}

	genericResourcesClientGetter := s.newGenericResourcesClientGetter(existingCluster, clusterTenantID)
	denyAssignmentsClientGetter := s.newDenyAssignmentsClientGetter(existingCluster, clusterTenantID)

	definitionsByType := denyAssignmentDefinitionsByType(existingCluster)

	// Loop 2: create, update, or delete deny assignments in Azure, then update
	// each entry in memory. Azure errors on one type do not skip the rest:
	// remaining types still run so a partial update can be persisted.
	// Successful type deconfigure removes the map entry.
	for denyAssignmentType := range replacement.Status.DenyAssignmentsOverManagedResourceGroup {
		status := replacement.Status.DenyAssignmentsOverManagedResourceGroup[denyAssignmentType]
		if status.DeconfigureTimestamp != nil {
			genericResourcesClient, err := genericResourcesClientGetter()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.deconfigureDenyAssignmentType(ctx, status, genericResourcesClient)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			delete(replacement.Status.DenyAssignmentsOverManagedResourceGroup, denyAssignmentType)
			continue
		}

		if !s.denyAssignmentTypeStillRequired(existingCluster, denyAssignmentType) {
			// If the deny assignment is persisted but not in the definitions anymore and it's not been marked for
			// deconfiguration it means that the intent controller has not yet stamped the DeconfigureTimestamp. In that case,
			// we skip doing anything with it in this loop.
			continue
		}

		genericResourcesClient, err := genericResourcesClientGetter()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		denyAssignmentsClient, err := denyAssignmentsClientGetter()
		if err != nil {
			errs = append(errs, err)
			continue
		}

		err = s.ensureDenyAssignmentType(ctx, existingCluster, existingServiceProviderCluster, denyAssignmentType, status, definitionsByType, genericResourcesClient, denyAssignmentsClient, timeNow)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(replacement.Status.DenyAssignmentsOverManagedResourceGroup) == 0 {
		replacement.Status.DenyAssignmentsOverManagedResourceGroup = nil
	}

	if len(errs) == 0 {
		s.syncDenyAssignmentRecheckTime(replacement, existingCluster)
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting deny assignment configure/deconfigure result onto ServiceProviderCluster")

		_, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			return errors.Join(errs...)
		}
		if err != nil {
			return errors.Join(append(errs, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))...)
		}
	}

	return errors.Join(errs...)
}

func (s *clusterDenyAssignmentSyncer) newGenericResourcesClientGetter(cluster *coreapi.Cluster, clusterTenantID string) func() (azureclient.GenericResourcesClient, error) {
	var client azureclient.GenericResourcesClient
	return func() (azureclient.GenericResourcesClient, error) {
		if client != nil {
			return client, nil
		}
		genericResourcesClient, err := s.azureFPAClientBuilder.GenericResourcesClient(clusterTenantID, cluster.ID.SubscriptionID)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to create generic resources client: %w", err))
		}
		client = genericResourcesClient
		return client, nil
	}
}

func (s *clusterDenyAssignmentSyncer) newDenyAssignmentsClientGetter(cluster *coreapi.Cluster, clusterTenantID string) func() (azureclient.DenyAssignmentsClient, error) {
	var client azureclient.DenyAssignmentsClient
	return func() (azureclient.DenyAssignmentsClient, error) {
		if client != nil {
			return client, nil
		}
		denyAssignmentsClient, err := s.azureFPAClientBuilder.DenyAssignmentsClient(clusterTenantID, cluster.ID.SubscriptionID)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to create deny assignments client: %w", err))
		}
		client = denyAssignmentsClient
		return client, nil
	}
}

func (s *clusterDenyAssignmentSyncer) ensureDenyAssignmentType(
	ctx context.Context,
	cluster *coreapi.Cluster,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	denyAssignmentType string,
	status *coreapi.DenyAssignmentStatus,
	definitionsByType map[string]*denyAssignmentDefinition,
	genericResourcesClient azureclient.GenericResourcesClient,
	denyAssignmentsClient azureclient.DenyAssignmentsClient,
	now time.Time,
) error {
	// In the context of this function, we consider that the deny assignment type being processed should be present in the
	// definitions. If for some reason it is not, we return an error.
	definition, ok := definitionsByType[denyAssignmentType]
	if !ok || definition == nil {
		return utils.TrackError(fmt.Errorf("no definition for deny assignment type %q", denyAssignmentType))
	}

	desiredResourceID, err := generateDenyAssignmentResourceID(cluster, denyAssignmentType)
	if err != nil {
		return err
	}

	desiredIdentities, err := desiredExcludedIdentitiesFromDenyAssignmentStatus(status)
	if err != nil {
		return err
	}

	excludedPrincipalIDs, droppedKeys, err := s.selectExcludedPrincipalsForDenyAssignmentPUT(status, desiredIdentities, now, denyAssignmentExcludePrincipalsLimit)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to select excluded principals for %s: %w", denyAssignmentType, err))
	}

	managedResourceGroupID := serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource

	// TODO rename to ensureDenyAssignment
	err = applyDenyAssignment(ctx, denyAssignmentsClient, genericResourcesClient,
		desiredResourceID, managedResourceGroupID, excludedPrincipalIDs,
		definition.actions, definition.notActions, definition.dataActions)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to ensure deny assignment %s: %w", denyAssignmentType, err))
	}

	droppedKeySet := make(map[string]struct{}, len(droppedKeys))
	for _, key := range droppedKeys {
		droppedKeySet[key] = struct{}{}
	}
	if err := s.syncEnsuredExcludedIdentities(status, desiredIdentities, droppedKeySet); err != nil {
		return utils.TrackError(fmt.Errorf("failed to sync ensured excluded identities for %s: %w", denyAssignmentType, err))
	}

	status.AzureResource = desiredResourceID
	status.PendingAzureResource = nil
	status.EnsuredPermissions = s.ensuredPermissionsFromDefinition(definition)
	utils.LoggerFromContext(ctx).Info("Ensured deny assignment", "denyAssignmentType", denyAssignmentType, "resourceID", desiredResourceID.String())
	return nil
}

func (s *clusterDenyAssignmentSyncer) deconfigureDenyAssignmentType(ctx context.Context, status *coreapi.DenyAssignmentStatus, genericResourcesClient azureclient.GenericResourcesClient) error {
	var errs []error
	remainingPendingAzureResource := status.PendingAzureResource
	remainingAzureResource := status.AzureResource

	if status.PendingAzureResource != nil {
		err := deleteDenyAssignment(ctx, genericResourcesClient, status.PendingAzureResource)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to delete pending deny assignment %s: %w", status.PendingAzureResource.String(), err)))
		} else {
			remainingPendingAzureResource = nil
		}
	}

	if status.AzureResource != nil && !controllerutil.ResourceIDsEqual(status.AzureResource, status.PendingAzureResource) {
		err := deleteDenyAssignment(ctx, genericResourcesClient, status.AzureResource)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to delete deny assignment %s: %w", status.AzureResource.String(), err)))
		} else {
			remainingAzureResource = nil
		}
	}

	status.PendingAzureResource = remainingPendingAzureResource
	status.AzureResource = remainingAzureResource
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// syncDenyAssignmentRecheckTime sets Spec.EarliestRecheckTimesByController[ClusterDenyAssignment]
// when remaining types are idle (all ensured, or ensured plus identity
// cooldowns still inside the 24h wait). An empty deny assignment map deletes
// the entry: there is nothing to re-query. Immediate work remaining (type not
// ensured, ready type deconfigure, or elapsed identity cooldown) leaves any
// existing time in place so needsWork stays true until that work finishes.
func (s *clusterDenyAssignmentSyncer) syncDenyAssignmentRecheckTime(replacement *coreapi.ServiceProviderCluster, cluster *coreapi.Cluster) {
	if len(replacement.Status.DenyAssignmentsOverManagedResourceGroup) == 0 {
		delete(replacement.Spec.EarliestRecheckTimesByController, ClusterDenyAssignmentControllerName)
		return
	}

	if !s.denyAssignmentsAzureIdle(cluster, replacement) {
		return
	}

	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(clusterDenyAssignmentRecheckInterval, clusterDenyAssignmentRecheckJitter)))
	if replacement.Spec.EarliestRecheckTimesByController == nil {
		replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
	}
	replacement.Spec.EarliestRecheckTimesByController[ClusterDenyAssignmentControllerName] = &recheckAt
}

func (s *clusterDenyAssignmentSyncer) denyAssignmentsAzureIdle(cluster *coreapi.Cluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	now := s.clock.Now()
	definitionsByType := denyAssignmentDefinitionsByType(cluster)
	for denyAssignmentType, status := range serviceProviderCluster.Status.DenyAssignmentsOverManagedResourceGroup {
		if !s.denyAssignmentTypeAzureIdle(cluster, denyAssignmentType, status, definitionsByType, now) {
			return false
		}
	}
	return true
}

// denyAssignmentTypeAzureIdle reports whether this type has no immediate Azure work. Ready
// type deconfigure, a desired type that is not ensured, permissions drift, a
// desired principal that is not EnsuredIdentity, or a cooldown wait that has
// elapsed are not idle. Types that have left the definition set and identity
// cooldowns still inside the 24h wait are idle. This is not the inverse of
// typeNeedsWork: a due controller recheck does not make the type not idle.
func (s *clusterDenyAssignmentSyncer) denyAssignmentTypeAzureIdle(
	cluster *coreapi.Cluster,
	denyAssignmentType string,
	status *coreapi.DenyAssignmentStatus,
	definitionsByType map[string]*denyAssignmentDefinition,
	timeNow time.Time,
) bool {

	// If the deny assignment is being deconfigured, it is not idle.
	// TODO do we want this at this level? or to wait a delay? do not confuse with the per identity service principal delay.
	if status.DeconfigureTimestamp != nil {
		return false
	}

	if !s.denyAssignmentTypeStillRequired(cluster, denyAssignmentType) {
		return true
	}

	// The deny assignment resource ID cannot be calculated until the cluster
	// service ID exists, so there is no Azure call to make.
	if len(controllerutils.ClusterServiceIDForCluster(cluster)) == 0 {
		return true
	}

	if s.denyAssignmentTypeNeedsImmediateWork(denyAssignmentType, status, definitionsByType) {
		return false
	}

	for _, excludedIdentityStatus := range status.ExcludedIdentities {
		if s.excludedIdentityNeedsImmediateWork(excludedIdentityStatus, timeNow) {
			return false
		}
	}

	return true
}

// desiredExcludedIdentitiesFromDenyAssignmentStatus returns the principals Intent marked as
// still desired: ExcludedIdentities rows whose DeconfigureTimestamp is nil.
// Rows with a timestamp are cooldown waiters and are not included. A nil row
// is an error.
func desiredExcludedIdentitiesFromDenyAssignmentStatus(status *coreapi.DenyAssignmentStatus) (map[string]struct{}, error) {
	if len(status.ExcludedIdentities) == 0 {
		return nil, nil
	}

	desired := make(map[string]struct{})
	for principalID, identityStatus := range status.ExcludedIdentities {
		if identityStatus.DeconfigureTimestamp != nil {
			continue
		}
		desired[principalID] = struct{}{}
	}
	if len(desired) == 0 {
		return nil, nil
	}
	return desired, nil
}

func (s *clusterDenyAssignmentSyncer) ensuredPermissionsFromDefinition(definition *denyAssignmentDefinition) *coreapi.DenyAssignmentEnsuredPermissions {
	return &coreapi.DenyAssignmentEnsuredPermissions{
		Actions:     slices.Clone(definition.actions),
		NotActions:  slices.Clone(definition.notActions),
		DataActions: slices.Clone(definition.dataActions),
	}
}

func (s *clusterDenyAssignmentSyncer) denyAssignmentTypeEnsuredPermissionsMatch(ensured *coreapi.DenyAssignmentEnsuredPermissions, definition *denyAssignmentDefinition) bool {
	if ensured == nil {
		return false
	}

	return s.denyAssignmentStringSlicesEqual(ensured.Actions, definition.actions) &&
		s.denyAssignmentStringSlicesEqual(ensured.NotActions, definition.notActions) &&
		s.denyAssignmentStringSlicesEqual(ensured.DataActions, definition.dataActions)
}

func (s *clusterDenyAssignmentSyncer) denyAssignmentStringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	left := slices.Clone(a)
	right := slices.Clone(b)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

// clusterTenantID returns the Azure Tenant ID of the cluster's subscription.
func (s *clusterDenyAssignmentSyncer) clusterTenantID(ctx context.Context, subscriptionID string) (string, error) {
	subscription, err := s.subscriptionLister.Get(ctx, subscriptionID)
	if err != nil {
		return "", utils.TrackError(fmt.Errorf("failed to get Subscription %s: %w", subscriptionID, err))
	}
	if subscription.Properties == nil {
		return "", utils.TrackError(fmt.Errorf("subscription %s has no properties", subscriptionID))
	}

	if subscription.Properties.TenantId == nil {
		return "", utils.TrackError(fmt.Errorf("subscription %s has nil tenantId", subscriptionID))
	}

	if len(*subscription.Properties.TenantId) == 0 {
		return "", utils.TrackError(fmt.Errorf("subscription %s has empty tenantId", subscriptionID))
	}

	return *subscription.Properties.TenantId, nil
}

type waitingExcludedIdentity struct {
	principalID string
	timestamp   time.Time
}

func (s *clusterDenyAssignmentSyncer) excludedIdentityWaitElapsed(status *coreapi.DenyAssignmentExcludedIdentityStatus, now time.Time) bool {
	if status == nil || status.DeconfigureTimestamp == nil {
		return false
	}
	return !now.Before(status.DeconfigureTimestamp.Add(denyAssignmentExcludedIdentityDeconfigureDelay))
}

// selectExcludedPrincipalsForDenyAssignmentPUT returns the principal IDs that should be on
// Azure ExcludePrincipals for this type, and the cooldown principal IDs that
// will not be included (wait elapsed or LRU-evicted for the maximum number of
// excluded principals allowed by Azure).
//
// desiredIdentities are the ExcludedIdentities keys whose DeconfigureTimestamp
// is nil. They always go on the list. If that set already exceeds limit, this
// is an error. The status map is then scanned only for cooldown rows
// (DeconfigureTimestamp set). Remaining slots are filled with those still
// inside the 24h wait, newest DeconfigureTimestamp first. Older waiters are
// dropped only after the remaining slots are full.
func (s *clusterDenyAssignmentSyncer) selectExcludedPrincipalsForDenyAssignmentPUT(
	status *coreapi.DenyAssignmentStatus,
	desiredIdentities map[string]struct{},
	now time.Time,
	limit int,
) ([]string, []string, error) {
	mustInclude := make([]string, 0, len(desiredIdentities))
	for principalID := range desiredIdentities {
		mustInclude = append(mustInclude, principalID)
	}

	// If the number of desired identities exceeds the limit, we return an error.
	if len(mustInclude) > limit {
		return nil, nil, utils.TrackError(fmt.Errorf("desired excluded principals %d exceed Azure ExcludePrincipals limit %d", len(mustInclude), limit))
	}

	// We then track what identities are still inside the cooldown wait period.
	waiting := make([]waitingExcludedIdentity, 0)
	dropped := make([]string, 0)
	for principalID, identityStatus := range status.ExcludedIdentities {
		if _, desired := desiredIdentities[principalID]; desired || identityStatus.DeconfigureTimestamp == nil {
			continue
		}
		if s.excludedIdentityWaitElapsed(identityStatus, now) {
			dropped = append(dropped, principalID)
			continue
		}
		waiting = append(waiting, waitingExcludedIdentity{principalID: principalID, timestamp: identityStatus.DeconfigureTimestamp.Time})
	}

	// We sort the waiting identities by timestamp descending (most recently stamped first).
	slices.SortFunc(waiting, func(a, b waitingExcludedIdentity) int {
		return b.timestamp.Compare(a.timestamp)
	})

	included := append([]string{}, mustInclude...)
	slots := limit - len(mustInclude)
	waitingKept := 0
	for _, waiter := range waiting {
		// If we have filled all the slots, we drop the entry. Because the list is sorted by timestamp descending (most recently stamped first), then
		// we remove the oldest entries first.
		if waitingKept >= slots {
			dropped = append(dropped, waiter.principalID)
			continue
		}
		included = append(included, waiter.principalID)
		waitingKept++
	}

	if len(included) == 0 {
		included = nil
	}
	if len(dropped) == 0 {
		dropped = nil
	}
	return included, dropped, nil
}

// syncEnsuredExcludedIdentities updates ExcludedIdentities after a successful
// Azure PUT. Desired principals, which are keys already present on
// ExcludedIdentities, get EnsuredIdentity. Wait-elapsed or LRU-dropped keys
// are deleted.
func (s *clusterDenyAssignmentSyncer) syncEnsuredExcludedIdentities(
	status *coreapi.DenyAssignmentStatus,
	desiredIdentities map[string]struct{},
	droppedKeys map[string]struct{},
) error {
	next := make(map[string]*coreapi.DenyAssignmentExcludedIdentityStatus, len(status.ExcludedIdentities))
	for principalID, identityStatus := range status.ExcludedIdentities {
		if _, dropped := droppedKeys[principalID]; dropped {
			continue
		}
		next[principalID] = identityStatus
	}

	for principalID := range desiredIdentities {
		if _, dropped := droppedKeys[principalID]; dropped {
			continue
		}
		existing := next[principalID]
		existing.DeconfigureTimestamp = nil
		existing.EnsuredIdentity = &coreapi.DenyAssignmentExcludedEnsuredIdentity{
			PrincipalID: principalID,
		}
	}

	if len(next) == 0 {
		status.ExcludedIdentities = nil
		return nil
	}
	status.ExcludedIdentities = next
	return nil
}

func isDenyAssignmentNotFoundError(err error) bool {
	var azErr *azcore.ResponseError
	return errors.As(err, &azErr) && azErr.ErrorCode == "DenyAssignmentNotFound"
}

func denyAssignmentNeedsUpdate(
	existing *armauthorization.DenyAssignment,
	expectedActions []string,
	expectedNotActions []string,
	expectedDataActions []string,
	expectedExcludedPrincipalIDs []string,
) bool {
	if existing.Properties == nil || existing.Properties.Permissions == nil {
		return true
	}
	if len(existing.Properties.Permissions) != 1 {
		return true
	}

	perm := existing.Properties.Permissions[0]
	if !ptrStringSliceEqual(perm.Actions, expectedActions) {
		return true
	}
	if !ptrStringSliceEqual(perm.NotActions, expectedNotActions) {
		return true
	}
	if !ptrStringSliceEqual(perm.DataActions, expectedDataActions) {
		return true
	}
	if !excludedPrincipalsEqual(existing.Properties.ExcludePrincipals, expectedExcludedPrincipalIDs) {
		return true
	}
	return false
}

func applyDenyAssignment(
	ctx context.Context,
	denyAssignmentsClient azureclient.DenyAssignmentsClient,
	genericResourcesClient azureclient.GenericResourcesClient,
	resourceID *azcorearm.ResourceID,
	scope *azcorearm.ResourceID,
	excludedPrincipalIDs []string,
	actions []string,
	notActions []string,
	dataActions []string,
) error {
	if notActions == nil {
		notActions = []string{}
	}
	if dataActions == nil {
		dataActions = []string{}
	}

	existing, err := denyAssignmentsClient.Get(ctx, scope.String(), resourceID.Name, nil)
	if err != nil && !isDenyAssignmentNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get deny assignment: %w", err))
	}
	if err == nil && !denyAssignmentNeedsUpdate(&existing.DenyAssignment, actions, notActions, dataActions, excludedPrincipalIDs) {
		return nil
	}

	excludedPrincipals := make([]any, 0, len(excludedPrincipalIDs))
	for _, id := range excludedPrincipalIDs {
		excludedPrincipals = append(excludedPrincipals, map[string]any{
			"id":   id,
			"type": "ServicePrincipal",
		})
	}

	resource := armresources.GenericResource{
		Location: to.Ptr("global"),
		Properties: map[string]any{
			"DenyAssignmentName": resourceID.Name,
			"Permissions": []any{
				map[string]any{
					"actions":        actions,
					"notActions":     notActions,
					"dataActions":    dataActions,
					"notDataActions": []string{},
				},
			},
			"Scope": scope.String(),
			"Principals": []any{
				map[string]any{
					"id":   allPrincipalsGUID,
					"type": "SystemDefined",
				},
			},
			"ExcludePrincipals": excludedPrincipals,
			"IsSystemProtected": true,
		},
	}

	poller, err := genericResourcesClient.BeginCreateOrUpdateByID(ctx, resourceID.String(), denyAssignmentAzureAPIVersion, resource, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("BeginCreateOrUpdateByID failed: %w", err))
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("polling deny assignment creation failed: %w", err))
	}

	return nil
}

// generateDenyAssignmentUUID deterministically derives a deny assignment's UUID exactly the way
// Cluster Service does, so both the RP and Cluster Service compute the same deny assignment IDs for
// a cluster without having to share them. It MUST stay byte-for-byte identical to Cluster Service's
// uuid.GenerateUuidV5(denyAssignmentNamespaceUuid, clusterID, suffix): a v5 (SHA-1) UUID over the
// shared namespace and the input string "<suffix>$<clusterID>" — Cluster Service joins its salts
// suffix-first with "$". clusterID is the OCM Cluster Service cluster ID (InternalID.ClusterID()),
// and denyAssignmentType is the per-type suffix (e.g. "compute-deny-assignment").
//
// See aro-hcp-clusters-service pkg/azure/denyassignmentcreator/deny_assignment_creator.go
// (generateDenyAssigmentId) and pkg/utils/uuid/generators.go (generateUuidV5WithSeparator).
// TestGenerateDenyAssignmentUUIDMatchesClusterService pins this equivalence.
func generateDenyAssignmentUUID(clusterID, denyAssignmentType string) string {
	namespace := uuid.MustParse(denyAssignmentNamespaceUUID)
	// Equivalent to Cluster Service's strings.Join([]string{denyAssignmentType, clusterID}, "$").
	return uuid.NewSHA1(namespace, []byte(denyAssignmentType+"$"+clusterID)).String()
}

func deleteDenyAssignment(
	ctx context.Context,
	client azureclient.GenericResourcesClient,
	resourceID *azcorearm.ResourceID,
) error {
	poller, err := client.BeginDeleteByID(ctx, resourceID.String(), denyAssignmentAzureAPIVersion, nil)
	if isResourceNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("BeginDeleteByID failed: %w", err))
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if isResourceNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("polling deny assignment deletion failed: %w", err))
	}

	return nil
}

func isResourceNotFoundError(err error) bool {
	var azErr *azcore.ResponseError
	return errors.As(err, &azErr) && azErr.StatusCode == 404
}

func excludedPrincipalsEqual(existing []*armauthorization.Principal, expected []string) bool {
	if len(existing) != len(expected) {
		return false
	}
	set := make(map[string]struct{}, len(expected))
	for _, id := range expected {
		set[id] = struct{}{}
	}
	for _, p := range existing {
		if p == nil || p.ID == nil {
			return false
		}
		if _, ok := set[*p.ID]; !ok {
			return false
		}
		delete(set, *p.ID)
	}
	return len(set) == 0
}

func ptrStringSliceEqual(a []*string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(b))
	for _, s := range b {
		set[s] = struct{}{}
	}
	for _, ptr := range a {
		s := ""
		if ptr != nil {
			s = *ptr
		}
		if _, ok := set[s]; !ok {
			return false
		}
		delete(set, s)
	}
	return len(set) == 0
}
