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

package roleassignments

import (
	"context"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ClusterRoleAssignmentsControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const ClusterRoleAssignmentsControllerName = "ClusterRoleAssignments"

const (
	// clusterRoleAssignmentsRecheckInterval is the base interval before
	// re-querying Azure for role assignments that are already ensured.
	// Combined with clusterRoleAssignmentsRecheckJitter via wait.Jitter. The
	// resulting time is stored on
	// Spec.EarliestRecheckTimesByController[ClusterRoleAssignmentsControllerName].
	clusterRoleAssignmentsRecheckInterval = 1 * time.Hour
	// clusterRoleAssignmentsRecheckJitter is the wait.Jitter factor applied to
	// clusterRoleAssignmentsRecheckInterval when setting this controller's
	// Spec.EarliestRecheckTimesByController entry.
	clusterRoleAssignmentsRecheckJitter = 0.5

	// roleAssignmentDeconfigureDelay is how long a live cluster waits after
	// DeconfigureTimestamp before deleting a draining role assignment.
	roleAssignmentDeconfigureDelay = 24 * time.Hour
)

// clusterRoleAssignmentsSyncer creates, repairs, and deletes Azure role
// assignments for entries in ServiceProviderCluster.Status.RoleAssignmentsV2.
//
// Desired keys persist the role assignment resource ID as PendingAzureResource
// before Create ("set pending before Azure"). Ensured keys are rechecked on
// Spec.EarliestRecheckTimesByController[ClusterRoleAssignmentV2], or
// immediately when AzureResource is missing. Get the role assignment and
// Create only if it is missing.
// Keys with DeconfigureTimestamp set delete the resource IDs already tracked
// on AzureResource and PendingAzureResource after the 24h wait, then drop the
// key. Azure work is skipped until the managed resource group is confirmed.
// Cluster deletion (DeletionTimestamp set) skips all work: role assignments
// are scoped to the managed resource group, so Azure deletes them in cascade
// when that resource group is removed.
type clusterRoleAssignmentsSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*clusterRoleAssignmentsSyncer)(nil)

// NewClusterRoleAssignmentsController creates a cluster-watching controller
// that creates, repairs, and deletes Azure role assignments using
// ServiceProviderCluster.Status.RoleAssignmentsV2.
func NewClusterRoleAssignmentsController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()

	syncer := &clusterRoleAssignmentsSyncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		resourcesDBClient:            resourcesDBClient,
		azureFPAClientBuilder:        azureFPAClientBuilder,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterRoleAssignmentsControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *clusterRoleAssignmentsSyncer) needsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	// If the cluster is being deleted, we skip the role assignment work. Because the role assignments are scoped to the managed resource group, when
	// the managed resource group is deleted, the role assignments are also deleted so no need to do anything in this controller in that case.
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}

	// If the managed resource group is not yet created, we skip the role assignment work, as we can't create role assignments
	// over the Managed Resource Group if it doesn't exist.
	if serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource == nil {
		return false
	}

	now := s.clock.Now()
	earliestRecheckTime := serviceProviderCluster.Spec.EarliestRecheckTimesByController[ClusterRoleAssignmentsControllerName]

	// If there are no role assignments to process, we consider there's no need to work.
	if len(serviceProviderCluster.Status.RoleAssignments) == 0 {
		return false
	}

	// If there are role assignments to process, we need to check if there's any immediate work to do. If there is at
	// least one assignment that needs immediate work, we return true.
	for _, status := range serviceProviderCluster.Status.RoleAssignments {
		if s.roleAssignmentNeedsImmediateWork(status, now) {
			return true
		}
	}

	// If there is no immediate work to do, we check if we need to recheck the role assignments based on the earliest recheck time.
	return earliestRecheckTime == nil || now.Compare(earliestRecheckTime.Time) >= 0
}

func (s *clusterRoleAssignmentsSyncer) roleAssignmentNeedsImmediateWork(status *coreapi.RoleAssignmentStatus, now time.Time) bool {
	if status.DeconfigureTimestamp != nil {
		return s.deconfigureCanStartForRoleAssignment(status, now)
	}
	return status.AzureResource == nil
}

// deconfigureCanStartForRoleAssignment reports whether a draining role
// assignment may be deleted. The 24h wait is measured from DeconfigureTimestamp.
func (s *clusterRoleAssignmentsSyncer) deconfigureCanStartForRoleAssignment(status *coreapi.RoleAssignmentStatus, now time.Time) bool {
	return !s.clock.Now().Before(status.DeconfigureTimestamp.Add(roleAssignmentDeconfigureDelay))
}

func (s *clusterRoleAssignmentsSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	// Loop 1: persist role assignment resource IDs that are not yet on the
	// document before any Azure Create. Desired keys record the ID when it is
	// not already AzureResource. Draining keys are skipped: their IDs are
	// already on AzureResource or leftover PendingAzureResource.
	// Cluster deletion is handled by needsWork and never reaches here.
	for assignmentKey, status := range replacement.Status.RoleAssignments {
		if status.DeconfigureTimestamp != nil {
			// If the role assignment is marked for deconfiguration, we skip it, as we will handle it in the next loop.
			continue
		}

		desiredResourceID, err := s.desiredRoleAssignmentResourceID(existingServiceProviderCluster, assignmentKey)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if controllerutil.ResourceIDsEqual(status.AzureResource, desiredResourceID) {
			// Already confirmed at this ID. PendingAzureResource means an
			// unconfirmed Create. Leaving it set would restamp pending on
			// every recheck. Clear a leftover that duplicates AzureResource.
			status.PendingAzureResource = nil
			continue
		}
		status.PendingAzureResource = desiredResourceID
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting pending role assignments onto ServiceProviderCluster")

		persistedServiceProviderCluster, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		existingServiceProviderCluster = persistedServiceProviderCluster
		replacement = existingServiceProviderCluster.DeepCopy()
	}

	roleAssignmentsClientGetter := s.newRoleAssignmentsClientGetter(ctx, key)
	// Loop 2: create, repair, or delete role assignments in Azure, then update
	// each entry in memory. Azure errors on one key do not skip the rest:
	// remaining keys still run so a partial update can be persisted.
	// AzureResource is written only when the Azure call for that key succeeds.
	// Successful key deconfigure removes the map entry. Draining keys wait
	// 24h (deconfigureCanStartForRoleAssignment) before Delete.
	for assignmentKey := range replacement.Status.RoleAssignments {
		status := replacement.Status.RoleAssignments[assignmentKey]
		if status == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("RoleAssignmentsV2 has a nil status for resource ID %s principal ID %s role definition resource ID %s", assignmentKey.ResourceID, assignmentKey.PrincipalID, assignmentKey.RoleDefinitionResourceID)))
			continue
		}

		if status.DeconfigureTimestamp != nil {
			if !s.deconfigureCanStartForRoleAssignment(status, timeNow) {
				continue
			}
			client, err := roleAssignmentsClientGetter()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.deconfigureRoleAssignment(ctx, status, client)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			delete(replacement.Status.RoleAssignments, assignmentKey)
			continue
		}

		client, err := roleAssignmentsClientGetter()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		err = s.ensureRoleAssignment(ctx, existingServiceProviderCluster, assignmentKey, status, client)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(replacement.Status.RoleAssignments) == 0 {
		replacement.Status.RoleAssignments = nil
	}

	if len(errs) == 0 {
		s.syncRoleAssignmentRecheckTime(replacement)
	}

	if controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		logger.Info("persisting role assignment configure/deconfigure result onto ServiceProviderCluster")

		_, err := s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
	}

	return errors.Join(errs...)
}

func (s *clusterRoleAssignmentsSyncer) newRoleAssignmentsClientGetter(ctx context.Context, key controllerutils.HCPClusterKey) func() (azureclient.RoleAssignmentsClient, error) {
	var client azureclient.RoleAssignmentsClient
	return func() (azureclient.RoleAssignmentsClient, error) {
		if client != nil {
			return client, nil
		}

		subscription, err := s.subscriptionLister.Get(ctx, key.SubscriptionID)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		if subscription.Properties == nil || subscription.Properties.TenantId == nil {
			return nil, utils.TrackError(fmt.Errorf("subscription %s has no tenantId", key.SubscriptionID))
		}

		built, err := s.azureFPAClientBuilder.RoleAssignmentsClient(*subscription.Properties.TenantId, key.SubscriptionID)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to create role assignments client: %w", err))
		}
		client = built
		return client, nil
	}
}

// desiredRoleAssignmentResourceID builds the deterministic managed-resource-group
// scoped role assignment resource ID. The scope is
// Status.AzureResources.ManagedResourceGroup.AzureResource. Callers run after
// needsWork, which already requires that field.
func (s *clusterRoleAssignmentsSyncer) desiredRoleAssignmentResourceID(serviceProviderCluster *coreapi.ServiceProviderCluster, key coreapi.RoleAssignmentKey) (*azcorearm.ResourceID, error) {
	scopeID := serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource
	generatedRoleAssignmentResourceIDStr := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scopeID.String(), key.PrincipalID, key.RoleDefinitionResourceID)
	parsedRoleAssignmentResourceID, err := azcorearm.ParseResourceID(generatedRoleAssignmentResourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse role assignment resource ID %q: %w", generatedRoleAssignmentResourceIDStr, err))
	}
	return parsedRoleAssignmentResourceID, nil
}

func (s *clusterRoleAssignmentsSyncer) ensureRoleAssignment(
	ctx context.Context,
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	key coreapi.RoleAssignmentKey,
	status *coreapi.RoleAssignmentStatus,
	client azureclient.RoleAssignmentsClient,
) error {
	desiredResourceID, err := s.desiredRoleAssignmentResourceID(serviceProviderCluster, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to calculate desired role assignment resource ID: %w", err))
	}

	scopeID := serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource

	_, getErr := client.GetByID(ctx, desiredResourceID.String(), nil)

	if getErr != nil && !azureclient.IsRoleAssignmentNotFoundErr(getErr) {
		return utils.TrackError(fmt.Errorf("failed to get role assignment %s: %w", desiredResourceID.String(), getErr))
	}

	if getErr != nil && azureclient.IsRoleAssignmentNotFoundErr(getErr) {
		if err := s.createRoleAssignment(ctx, client, scopeID.String(), desiredResourceID.Name, key); err != nil {
			return err
		}
	}

	status.AzureResource = desiredResourceID
	status.PendingAzureResource = nil
	utils.LoggerFromContext(ctx).Info("Ensured role assignment", "resourceID", desiredResourceID.String(), "principalID", key.PrincipalID, "roleDefinitionResourceID", key.RoleDefinitionResourceID)

	return nil
}

func (s *clusterRoleAssignmentsSyncer) createRoleAssignment(
	ctx context.Context,
	client azureclient.RoleAssignmentsClient,
	scope string,
	roleAssignmentName string,
	key coreapi.RoleAssignmentKey,
) error {
	parameters := armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID:      ptr.To(key.PrincipalID),
			RoleDefinitionID: ptr.To(key.RoleDefinitionResourceID),
			PrincipalType:    ptr.To(armauthorization.PrincipalTypeServicePrincipal),
		},
	}
	_, err := client.Create(ctx, scope, roleAssignmentName, parameters, nil)
	if azureclient.IsRoleAssignmentExistsErr(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create role assignment %s: %w", roleAssignmentName, err))
	}
	return nil
}

func (s *clusterRoleAssignmentsSyncer) deleteRoleAssignment(ctx context.Context, client azureclient.RoleAssignmentsClient, resourceID *azcorearm.ResourceID) error {
	_, err := client.DeleteByID(ctx, resourceID.String(), nil)
	if azureclient.IsRoleAssignmentNotFoundErr(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete role assignment %s: %w", resourceID.String(), err))
	}
	return nil
}

func (s *clusterRoleAssignmentsSyncer) deconfigureRoleAssignment(
	ctx context.Context,
	status *coreapi.RoleAssignmentStatus,
	client azureclient.RoleAssignmentsClient,
) error {
	var errs []error

	remainingPendingAzureResource := status.PendingAzureResource
	remainingAzureResource := status.AzureResource

	if status.PendingAzureResource != nil {
		err := s.deleteRoleAssignment(ctx, client, status.PendingAzureResource)
		if err != nil {
			errs = append(errs, err)
		} else {
			remainingPendingAzureResource = nil
		}
	}

	if status.AzureResource != nil && !controllerutil.ResourceIDsEqual(status.AzureResource, status.PendingAzureResource) {
		if err := s.deleteRoleAssignment(ctx, client, status.AzureResource); err != nil {
			errs = append(errs, err)
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

// syncRoleAssignmentRecheckTime sets Spec.EarliestRecheckTimesByController[ClusterRoleAssignments]
// when remaining keys are idle (desired keys have AzureResource set, and
// draining keys are still inside the 24h wait). An empty role assignment map
// deletes the entry: there is nothing to re-query. Immediate work remaining
// leaves any existing time in place so needsWork stays true until that work
// finishes.
func (s *clusterRoleAssignmentsSyncer) syncRoleAssignmentRecheckTime(replacement *coreapi.ServiceProviderCluster) {
	if len(replacement.Status.RoleAssignments) == 0 {
		delete(replacement.Spec.EarliestRecheckTimesByController, ClusterRoleAssignmentsControllerName)
		return
	}

	if !s.roleAssignmentsAzureIdle(replacement) {
		return
	}

	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(clusterRoleAssignmentsRecheckInterval, clusterRoleAssignmentsRecheckJitter)))
	if replacement.Spec.EarliestRecheckTimesByController == nil {
		replacement.Spec.EarliestRecheckTimesByController = map[string]*metav1.Time{}
	}
	replacement.Spec.EarliestRecheckTimesByController[ClusterRoleAssignmentsControllerName] = &recheckAt
}

func (s *clusterRoleAssignmentsSyncer) roleAssignmentsAzureIdle(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	now := s.clock.Now()
	for _, status := range serviceProviderCluster.Status.RoleAssignments {
		if s.roleAssignmentNeedsImmediateWork(status, now) {
			return false
		}
	}
	return true
}
