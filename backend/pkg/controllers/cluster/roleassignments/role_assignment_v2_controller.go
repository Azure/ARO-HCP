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
	"strings"
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

// ClusterRoleAssignmentV2ControllerName is the single source of truth for this
// controller's name. It is used for the workqueue name (a Prometheus label),
// context/logger controller name, and log fields.
const ClusterRoleAssignmentV2ControllerName = "ClusterRoleAssignmentV2"

const (
	// clusterRoleAssignmentV2RecheckInterval is the base interval before
	// re-querying Azure for a role assignment that is already Configured.
	// Combined with clusterRoleAssignmentV2RecheckJitter via wait.Jitter.
	clusterRoleAssignmentV2RecheckInterval = 12 * time.Hour
	clusterRoleAssignmentV2RecheckJitter   = 0.5

	// roleAssignmentDeconfigureDelay is how long a live cluster waits after
	// DeconfigureTimestamp before deleting a PendingDeconfigure role assignment.
	roleAssignmentDeconfigureDelay = 24 * time.Hour
)

// clusterRoleAssignmentV2Syncer creates, repairs, and deletes Azure role
// assignments for entries in ServiceProviderCluster.Status.RoleAssignmentsV2.
//
// PendingConfigure and Configured persist the role assignment resource ID as
// PendingAzureResource before Create ("set pending before Azure").
// Configured entries are rechecked on EarliestRecheckTime. Get the role
// assignment and Create only if it is missing. If it exists with a different
// principal or role definition, Delete then Create.
// PendingDeconfigure deletes the resource IDs already tracked on
// AzureResource and PendingAzureResource after the 24h wait.
// Azure work is skipped until the managed resource group is confirmed.
// Cluster deletion (DeletionTimestamp set) skips all work: role assignments
// are scoped to the managed resource group, so Azure deletes them in cascade
// when that resource group is removed.
type clusterRoleAssignmentV2Syncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	subscriptionLister           corelisters.SubscriptionLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*clusterRoleAssignmentV2Syncer)(nil)

// NewClusterRoleAssignmentV2Controller creates a cluster-watching controller
// that creates, repairs, and deletes Azure role assignments using
// ServiceProviderCluster.Status.RoleAssignmentsV2.
func NewClusterRoleAssignmentV2Controller(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()

	syncer := &clusterRoleAssignmentV2Syncer{
		clock:                        clock,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		resourcesDBClient:            resourcesDBClient,
		azureFPAClientBuilder:        azureFPAClientBuilder,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterRoleAssignmentV2ControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (s *clusterRoleAssignmentV2Syncer) needsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}
	if serviceProviderCluster.Status.AzureResources.ManagedResourceGroup.AzureResource == nil {
		return false
	}
	now := s.clock.Now()
	for _, status := range serviceProviderCluster.Status.RoleAssignmentsV2 {
		if status == nil {
			return true
		}
		switch status.Phase {
		case coreapi.RoleAssignmentPhasePendingConfigure:
			return true
		case coreapi.RoleAssignmentPhasePendingDeconfigure:
			if s.pendingDeconfigureReady(status, now) {
				return true
			}
		case coreapi.RoleAssignmentPhaseConfigured:
			if status.EarliestRecheckTime != nil && now.Before(status.EarliestRecheckTime.Time) {
				continue
			}
			return true
		}
	}
	return false
}

func (s *clusterRoleAssignmentV2Syncer) skipAzureWork(status *coreapi.RoleAssignmentStatus, now time.Time) bool {
	switch status.Phase {
	case coreapi.RoleAssignmentPhaseConfigured:
		return status.EarliestRecheckTime != nil && now.Before(status.EarliestRecheckTime.Time)
	case coreapi.RoleAssignmentPhasePendingDeconfigure:
		return !s.pendingDeconfigureReady(status, now)
	default:
		return false
	}
}

func (s *clusterRoleAssignmentV2Syncer) pendingDeconfigureReady(status *coreapi.RoleAssignmentStatus, now time.Time) bool {
	if status.DeconfigureTimestamp == nil {
		return true
	}
	return !now.Before(status.DeconfigureTimestamp.Time.Add(roleAssignmentDeconfigureDelay))
}

func (s *clusterRoleAssignmentV2Syncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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
	// document before any Azure Create. PendingConfigure and Configured
	// record the ID when it is not already AzureResource. PendingDeconfigure is
	// skipped: its IDs are already on AzureResource or leftover PendingAzureResource.
	// Cluster deletion is handled by needsWork and never reaches here.
	for assignmentKey, status := range replacement.Status.RoleAssignmentsV2 {
		if status == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("RoleAssignmentsV2 has a nil status for resource ID %s principal ID %s role definition resource ID %s", assignmentKey.ResourceID, assignmentKey.PrincipalID, assignmentKey.RoleDefinitionResourceID)))
			continue
		}

		if s.skipAzureWork(status, timeNow) {
			continue
		}

		switch status.Phase {
		case coreapi.RoleAssignmentPhasePendingConfigure,
			coreapi.RoleAssignmentPhaseConfigured:
			desiredResourceID, err := s.desiredResourceID(existingCluster, assignmentKey)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if desiredResourceID == nil {
				continue
			}
			if roleAssignmentResourceIDsEqual(status.AzureResource, desiredResourceID) {
				status.PendingAzureResource = nil
				continue
			}
			status.PendingAzureResource = desiredResourceID
		}
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

	// Loop 2: create, repair, or delete role assignments in Azure, then update
	// each entry in memory. Azure errors on one key do not skip the rest:
	// remaining keys still run so a partial update can be persisted. Phase
	// becomes Configured or Deconfigured only when the Azure call for that key
	// succeeds.
	getClient := s.newRoleAssignmentsClientGetter(ctx, key)

	for assignmentKey, status := range replacement.Status.RoleAssignmentsV2 {
		if status == nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("RoleAssignmentsV2 has a nil status for resource ID %s principal ID %s role definition resource ID %s", assignmentKey.ResourceID, assignmentKey.PrincipalID, assignmentKey.RoleDefinitionResourceID)))
			continue
		}

		if s.skipAzureWork(status, timeNow) {
			continue
		}

		switch status.Phase {
		case coreapi.RoleAssignmentPhasePendingConfigure,
			coreapi.RoleAssignmentPhaseConfigured:
			client, err := getClient()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.ensureRoleAssignment(ctx, existingCluster, assignmentKey, status, client)
			if err != nil {
				errs = append(errs, err)
			}
		case coreapi.RoleAssignmentPhasePendingDeconfigure:
			client, err := getClient()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			err = s.deconfigureRoleAssignment(ctx, status, client)
			if err != nil {
				errs = append(errs, err)
			}
		}
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

func (s *clusterRoleAssignmentV2Syncer) newRoleAssignmentsClientGetter(ctx context.Context, key controllerutils.HCPClusterKey) func() (azureclient.RoleAssignmentsClient, error) {
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

func (s *clusterRoleAssignmentV2Syncer) desiredResourceID(cluster *coreapi.HCPOpenShiftCluster, key coreapi.RoleAssignmentKey) (*azcorearm.ResourceID, error) {
	if len(cluster.CustomerProperties.Platform.ManagedResourceGroup) == 0 {
		return nil, nil
	}
	scopeID, err := coreapi.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to build managed resource group resource ID: %w", err))
	}
	fullID := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scopeID.String(), key.PrincipalID, key.RoleDefinitionResourceID)
	parsed, err := azcorearm.ParseResourceID(fullID)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse role assignment resource ID %q: %w", fullID, err))
	}
	return parsed, nil
}

func (s *clusterRoleAssignmentV2Syncer) ensureRoleAssignment(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	key coreapi.RoleAssignmentKey,
	status *coreapi.RoleAssignmentStatus,
	client azureclient.RoleAssignmentsClient,
) error {
	desiredResourceID, err := s.desiredResourceID(cluster, key)
	if err != nil {
		return err
	}
	if desiredResourceID == nil {
		return nil
	}

	scopeID, err := coreapi.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build managed resource group resource ID: %w", err))
	}

	getResp, getErr := client.GetByID(ctx, desiredResourceID.String(), nil)
	switch {
	case azureclient.IsRoleAssignmentNotFoundErr(getErr):
		if err := s.createRoleAssignment(ctx, client, scopeID.String(), desiredResourceID.Name, key); err != nil {
			return err
		}
	case getErr != nil:
		return utils.TrackError(fmt.Errorf("failed to get role assignment %s: %w", desiredResourceID.String(), getErr))
	case !roleAssignmentMatches(getResp.RoleAssignment, key):
		if err := s.deleteRoleAssignment(ctx, client, desiredResourceID); err != nil {
			return err
		}
		if err := s.createRoleAssignment(ctx, client, scopeID.String(), desiredResourceID.Name, key); err != nil {
			return err
		}
	}

	status.AzureResource = desiredResourceID
	status.PendingAzureResource = nil
	status.Phase = coreapi.RoleAssignmentPhaseConfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(clusterRoleAssignmentV2RecheckInterval, clusterRoleAssignmentV2RecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	utils.LoggerFromContext(ctx).Info("Ensured role assignment", "resourceID", desiredResourceID.String(), "principalID", key.PrincipalID, "roleDefinitionResourceID", key.RoleDefinitionResourceID)
	return nil
}

func (s *clusterRoleAssignmentV2Syncer) createRoleAssignment(
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

func (s *clusterRoleAssignmentV2Syncer) deleteRoleAssignment(
	ctx context.Context,
	client azureclient.RoleAssignmentsClient,
	resourceID *azcorearm.ResourceID,
) error {
	if resourceID == nil {
		return nil
	}
	_, err := client.DeleteByID(ctx, resourceID.String(), nil)
	if azureclient.IsRoleAssignmentNotFoundErr(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete role assignment %s: %w", resourceID.String(), err))
	}
	return nil
}

func (s *clusterRoleAssignmentV2Syncer) deconfigureRoleAssignment(
	ctx context.Context,
	status *coreapi.RoleAssignmentStatus,
	client azureclient.RoleAssignmentsClient,
) error {
	var errs []error
	remainingPending := status.PendingAzureResource
	remainingAzure := status.AzureResource

	if status.PendingAzureResource != nil {
		if err := s.deleteRoleAssignment(ctx, client, status.PendingAzureResource); err != nil {
			errs = append(errs, err)
		} else {
			remainingPending = nil
		}
	}
	if status.AzureResource != nil && !roleAssignmentResourceIDsEqual(status.AzureResource, status.PendingAzureResource) {
		if err := s.deleteRoleAssignment(ctx, client, status.AzureResource); err != nil {
			errs = append(errs, err)
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
	status.Phase = coreapi.RoleAssignmentPhaseDeconfigured
	recheckAt := metav1.NewTime(s.clock.Now().Add(wait.Jitter(clusterRoleAssignmentV2RecheckInterval, clusterRoleAssignmentV2RecheckJitter)))
	status.EarliestRecheckTime = &recheckAt
	return nil
}

func roleAssignmentMatches(assignment armauthorization.RoleAssignment, key coreapi.RoleAssignmentKey) bool {
	if assignment.Properties == nil {
		return false
	}
	if !strings.EqualFold(ptr.Deref(assignment.Properties.PrincipalID, ""), key.PrincipalID) {
		return false
	}
	return roleDefinitionIDsEqual(ptr.Deref(assignment.Properties.RoleDefinitionID, ""), key.RoleDefinitionResourceID)
}

func roleDefinitionIDsEqual(observed, desired string) bool {
	if strings.EqualFold(observed, desired) {
		return true
	}
	return strings.EqualFold(roleDefinitionGUID(observed), roleDefinitionGUID(desired))
}

func roleDefinitionGUID(roleDefinitionID string) string {
	i := strings.LastIndex(roleDefinitionID, "/")
	if i < 0 || i == len(roleDefinitionID)-1 {
		return strings.ToLower(roleDefinitionID)
	}
	return strings.ToLower(roleDefinitionID[i+1:])
}

func roleAssignmentResourceIDsEqual(a, b *azcorearm.ResourceID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.String(), b.String())
}
