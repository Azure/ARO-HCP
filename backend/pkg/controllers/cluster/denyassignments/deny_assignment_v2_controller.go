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
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

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

// ClusterDenyAssignmentV2ControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const ClusterDenyAssignmentV2ControllerName = "ClusterDenyAssignmentV2"

const (
	// clusterDenyAssignmentV2RecheckInterval is the base interval before
	// re-querying Azure for a deny assignment that is already Configured.
	// Combined with clusterDenyAssignmentV2RecheckJitter via wait.Jitter.
	clusterDenyAssignmentV2RecheckInterval = 12 * time.Hour
	clusterDenyAssignmentV2RecheckJitter   = 0.5

	// denyAssignmentExcludedIdentityDeconfigureDelay is how long a live
	// cluster keeps a PendingDeconfigure principal on ExcludePrincipals
	// after DeconfigureTimestamp. ClusterDenyAssignmentV2 may drop older
	// waiters sooner when Azure's 25-principal ExcludePrincipals limit
	// would otherwise block currently desired principals.
	denyAssignmentExcludedIdentityDeconfigureDelay = 24 * time.Hour

	// denyAssignmentExcludePrincipalsLimit is the Azure maximum number of
	// principals on one deny assignment's ExcludePrincipals list.
	denyAssignmentExcludePrincipalsLimit = 25
)

// clusterDenyAssignmentV2Syncer creates and deletes Azure deny assignments for
// entries in ServiceProviderCluster.Status.DenyAssignmentsV2.
//
// PendingConfigure and Configured persist the deny assignment resource ID as
// PendingAzureResource before CreateOrUpdate ("set pending before Azure").
// Configured entries are rechecked on EarliestRecheckTime, or immediately when
// ExcludedIdentities has PendingConfigure or a PendingDeconfigure wait that
// has elapsed. Get the deny assignment and CreateOrUpdate only if it is
// missing or permissions / excluded principals drifted.
// ExcludePrincipals is built from ExcludedIdentities: live identities plus
// PendingDeconfigure principals still inside the 24h wait, capped at 25
// with oldest waiters dropped first.
// PendingDeconfigure deletes the resource IDs already tracked on AzureResource
// and PendingAzureResource.
// Cluster deletion (DeletionTimestamp set) skips all work: deny assignments
// are scoped to the managed resource group, so Azure deletes them in cascade
// when that resource group is removed.
type clusterDenyAssignmentV2Syncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*clusterDenyAssignmentV2Syncer)(nil)

// NewClusterDenyAssignmentV2Controller creates a cluster-watching controller
// that creates and deletes Azure deny assignments using
// ServiceProviderCluster.Status.DenyAssignmentsV2.
func NewClusterDenyAssignmentV2Controller(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()

	syncer := &clusterDenyAssignmentV2Syncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		resourcesDBClient:            resourcesDBClient,
		azureFPAClientBuilder:        azureFPAClientBuilder,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterDenyAssignmentV2ControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *clusterDenyAssignmentV2Syncer) needsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}
	now := s.clock.Now()
	for _, status := range serviceProviderCluster.Status.DenyAssignmentsV2 {
		if status == nil {
			return true
		}
		switch status.Phase {
		case coreapi.DenyAssignmentPhasePendingConfigure,
			coreapi.DenyAssignmentPhasePendingDeconfigure:
			return true
		case coreapi.DenyAssignmentPhaseConfigured:
			if s.excludedIdentityWorkNeeded(status, now) {
				return true
			}
			if status.EarliestRecheckTime != nil && now.Before(status.EarliestRecheckTime.Time) {
				continue
			}
			return true
		}
	}
	return false
}

func (s *clusterDenyAssignmentV2Syncer) skipConfiguredEnsure(status *coreapi.DenyAssignmentStatus, now time.Time) bool {
	if status.Phase != coreapi.DenyAssignmentPhaseConfigured {
		return false
	}
	if s.excludedIdentityWorkNeeded(status, now) {
		return false
	}
	return status.EarliestRecheckTime != nil && now.Before(status.EarliestRecheckTime.Time)
}

func (s *clusterDenyAssignmentV2Syncer) excludedIdentityWorkNeeded(status *coreapi.DenyAssignmentStatus, now time.Time) bool {
	for _, identityStatus := range status.ExcludedIdentities {
		if identityStatus == nil {
			return true
		}
		switch identityStatus.Phase {
		case coreapi.DenyAssignmentExcludedIdentityPhasePendingConfigure:
			return true
		case coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure:
			if s.pendingDeconfigureReady(identityStatus, now) {
				return true
			}
		}
	}
	return false
}

func (s *clusterDenyAssignmentV2Syncer) pendingDeconfigureReady(status *coreapi.DenyAssignmentExcludedIdentityStatus, now time.Time) bool {
	if status.DeconfigureTimestamp == nil {
		return true
	}
	return !now.Before(status.DeconfigureTimestamp.Time.Add(denyAssignmentExcludedIdentityDeconfigureDelay))
}

func (s *clusterDenyAssignmentV2Syncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	replacement := existingServiceProviderCluster.DeepCopy()
	timeNow := s.clock.Now()
	var errs []error

	definitionsByType := denyAssignmentDefinitionsByType(existingCluster)

	// Loop 1: persist deny assignment resource IDs that are not yet on the
	// document before any Azure CreateOrUpdate. PendingConfigure and Configured
	// record the ID when it is not already AzureResource. PendingDeconfigure is
	// skipped: its IDs are already on AzureResource or leftover PendingAzureResource.
	// Cluster deletion is handled by needsWork and never reaches here.
	for denyAssignmentType := range replacement.Status.DenyAssignmentsV2 {
		status := replacement.Status.DenyAssignmentsV2[denyAssignmentType]
		if status == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("DenyAssignmentsV2 has a nil status for type %s", denyAssignmentType)))
			continue
		}

		if s.skipConfiguredEnsure(status, timeNow) {
			continue
		}

		switch status.Phase {
		case coreapi.DenyAssignmentPhasePendingConfigure,
			coreapi.DenyAssignmentPhaseConfigured:
			desiredResourceID, err := s.desiredResourceID(existingCluster, denyAssignmentType)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if desiredResourceID == nil {
				continue
			}
			if resourceIDsEqual(status.AzureResource, desiredResourceID) {
				status.PendingAzureResource = nil
				continue
			}
			status.PendingAzureResource = desiredResourceID
		}
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting pending deny assignments onto ServiceProviderCluster")

		persistedServiceProviderCluster, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		existingServiceProviderCluster = persistedServiceProviderCluster
		replacement = existingServiceProviderCluster.DeepCopy()
	}

	// Loop 2: create, update, or delete deny assignments in Azure, then update
	// each entry in memory. Azure errors on one type do not skip the rest:
	// remaining types still run so a partial update can be persisted. Phase
	// becomes Configured or Deconfigured only when the Azure call for that type
	// succeeds.
	getClients := s.newAzureClientsGetter(ctx, existingCluster, key)

	for denyAssignmentType := range replacement.Status.DenyAssignmentsV2 {
		status := replacement.Status.DenyAssignmentsV2[denyAssignmentType]
		if status == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("DenyAssignmentsV2 has a nil status for type %s", denyAssignmentType)))
			continue
		}

		if s.skipConfiguredEnsure(status, timeNow) {
			continue
		}

		switch status.Phase {
		case coreapi.DenyAssignmentPhasePendingConfigure,
			coreapi.DenyAssignmentPhaseConfigured:
			err := s.ensureType(ctx, existingCluster, denyAssignmentType, status, definitionsByType, getClients, timeNow)
			if err != nil {
				errs = append(errs, err)
			}
		case coreapi.DenyAssignmentPhasePendingDeconfigure:
			clients, err := getClients()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.deconfigureType(ctx, status, clients.genericResourcesClient)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting deny assignment configure/deconfigure result onto ServiceProviderCluster")

		_, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
	}

	return errors.Join(errs...)
}

type denyAssignmentAzureClients struct {
	denyAssignmentsClient  azureclient.DenyAssignmentsClient
	genericResourcesClient azureclient.GenericResourcesClient
	managedResourceGroupID *azcorearm.ResourceID
}

func (s *clusterDenyAssignmentV2Syncer) newAzureClientsGetter(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, key controllerutils.HCPClusterKey) func() (*denyAssignmentAzureClients, error) {
	var clients *denyAssignmentAzureClients
	return func() (*denyAssignmentAzureClients, error) {
		if clients != nil {
			return clients, nil
		}

		subscription, err := s.subscriptionLister.Get(ctx, key.SubscriptionID)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		if subscription.Properties == nil || subscription.Properties.TenantId == nil {
			return nil, utils.TrackError(fmt.Errorf("subscription %s has no tenantId", key.SubscriptionID))
		}
		tenantID := *subscription.Properties.TenantId

		managedResourceGroupID, err := coreapi.ToResourceGroupResourceID(key.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to build managed resource group resource ID: %w", err))
		}

		genericResourcesClient, err := s.azureFPAClientBuilder.GenericResourcesClient(tenantID, key.SubscriptionID)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to create generic resources client: %w", err))
		}
		denyAssignmentsClient, err := s.azureFPAClientBuilder.DenyAssignmentsClient(tenantID, key.SubscriptionID)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to create deny assignments client: %w", err))
		}

		clients = &denyAssignmentAzureClients{
			denyAssignmentsClient:  denyAssignmentsClient,
			genericResourcesClient: genericResourcesClient,
			managedResourceGroupID: managedResourceGroupID,
		}
		return clients, nil
	}
}

func (s *clusterDenyAssignmentV2Syncer) desiredResourceID(cluster *coreapi.HCPOpenShiftCluster, denyAssignmentType string) (*azcorearm.ResourceID, error) {
	if len(controllerutils.ClusterServiceIDForCluster(cluster)) == 0 {
		return nil, nil
	}
	if len(cluster.CustomerProperties.Platform.ManagedResourceGroup) == 0 {
		return nil, nil
	}
	resourceID, err := denyAssignmentResourceID(cluster, denyAssignmentType)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return resourceID, nil
}

func (s *clusterDenyAssignmentV2Syncer) ensureType(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	denyAssignmentType string,
	status *coreapi.DenyAssignmentStatus,
	definitionsByType map[string]denyAssignmentDefinition,
	getClients func() (*denyAssignmentAzureClients, error),
	now time.Time,
) error {
	definition, ok := definitionsByType[denyAssignmentType]
	if !ok {
		return utils.TrackError(fmt.Errorf("no definition for deny assignment type %q", denyAssignmentType))
	}

	desiredResourceID, err := s.desiredResourceID(cluster, denyAssignmentType)
	if err != nil {
		return err
	}
	if desiredResourceID == nil {
		return nil
	}

	excludedPrincipalIDs, droppedKeys, err := selectExcludedPrincipalsForPUT(status, now, denyAssignmentExcludePrincipalsLimit)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to select excluded principals for %s: %w", denyAssignmentType, err))
	}

	clients, err := getClients()
	if err != nil {
		return err
	}

	err = applyDenyAssignment(ctx, clients.denyAssignmentsClient, clients.genericResourcesClient,
		desiredResourceID, clients.managedResourceGroupID, excludedPrincipalIDs,
		definition.actions, definition.notActions, definition.dataActions)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to ensure deny assignment %s: %w", denyAssignmentType, err))
	}

	includedPrincipals := make(map[string]struct{}, len(excludedPrincipalIDs))
	for _, principalID := range excludedPrincipalIDs {
		includedPrincipals[principalID] = struct{}{}
	}
	droppedKeySet := make(map[coreapi.DenyAssignmentExcludedIdentityKey]struct{}, len(droppedKeys))
	for _, key := range droppedKeys {
		droppedKeySet[key] = struct{}{}
	}
	s.advanceExcludedIdentityPhases(status, includedPrincipals, droppedKeySet)

	status.AzureResource = desiredResourceID
	status.PendingAzureResource = nil
	status.Phase = coreapi.DenyAssignmentPhaseConfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(clusterDenyAssignmentV2RecheckInterval, clusterDenyAssignmentV2RecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	utils.LoggerFromContext(ctx).Info("Ensured deny assignment", "denyAssignmentType", denyAssignmentType, "resourceID", desiredResourceID.String())
	return nil
}

// advanceExcludedIdentityPhases updates identity phases after a successful
// Azure PUT. It does not write ObservedIdentity.
func (s *clusterDenyAssignmentV2Syncer) advanceExcludedIdentityPhases(
	status *coreapi.DenyAssignmentStatus,
	includedPrincipals map[string]struct{},
	droppedKeys map[coreapi.DenyAssignmentExcludedIdentityKey]struct{},
) {
	for key, identityStatus := range status.ExcludedIdentities {
		if identityStatus == nil {
			continue
		}
		switch identityStatus.Phase {
		case coreapi.DenyAssignmentExcludedIdentityPhasePendingConfigure:
			identityStatus.Phase = coreapi.DenyAssignmentExcludedIdentityPhaseConfigured
			identityStatus.DeconfigureTimestamp = nil
		case coreapi.DenyAssignmentExcludedIdentityPhasePendingDeconfigure:
			_, included := includedPrincipals[key.PrincipalID]
			_, dropped := droppedKeys[key]
			if !included || dropped {
				identityStatus.Phase = coreapi.DenyAssignmentExcludedIdentityPhaseDeconfigured
			}
		}
	}
}

func (s *clusterDenyAssignmentV2Syncer) deconfigureType(
	ctx context.Context,
	status *coreapi.DenyAssignmentStatus,
	genericResourcesClient azureclient.GenericResourcesClient,
) error {
	var errs []error
	remainingPending := status.PendingAzureResource
	remainingAzure := status.AzureResource

	if status.PendingAzureResource != nil {
		if err := deleteDenyAssignment(ctx, genericResourcesClient, status.PendingAzureResource); err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to delete pending deny assignment %s: %w", status.PendingAzureResource.String(), err)))
		} else {
			remainingPending = nil
		}
	}
	if status.AzureResource != nil && !resourceIDsEqual(status.AzureResource, status.PendingAzureResource) {
		if err := deleteDenyAssignment(ctx, genericResourcesClient, status.AzureResource); err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to delete deny assignment %s: %w", status.AzureResource.String(), err)))
		} else {
			remainingAzure = nil
		}
	}

	status.PendingAzureResource = remainingPending
	status.AzureResource = remainingAzure
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	status.PendingAzureResource = nil
	status.AzureResource = nil
	status.Phase = coreapi.DenyAssignmentPhaseDeconfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(clusterDenyAssignmentV2RecheckInterval, clusterDenyAssignmentV2RecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	return nil
}

func denyAssignmentDefinitionsByType(cluster *coreapi.HCPOpenShiftCluster) map[string]denyAssignmentDefinition {
	defs := denyAssignmentDefinitions(cluster)
	byType := make(map[string]denyAssignmentDefinition, len(defs))
	for _, definition := range defs {
		byType[definition.denyAssignmentType] = definition
	}
	return byType
}

func resourceIDsEqual(a, b *azcorearm.ResourceID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.String(), b.String())
}
