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

package mockfparoleassignments

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/azureresourcesinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/azureresourceslisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// MockFPARoleAssignmentsIntentControllerName is the single source of truth for
// this controller's name. It is used for the workqueue name (a Prometheus
// label), context/logger controller name, and log fields.
const MockFPARoleAssignmentsIntentControllerName = "MockFPARoleAssignmentsIntent"

// OwnerRoleDefinitionResourceID is the tenant-level resource ID of the Azure
// built-in Owner role definition. It is the role the mock First Party Application
// principal is granted over each cluster's managed resource group.
// The ID has been obtained from https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/privileged#owner.
const OwnerRoleDefinitionResourceID = "/providers/Microsoft.Authorization/roleDefinitions/8e3af657-a8ff-443c-a75c-2fe8c4bcb635"

// mockFPARoleAssignmentsIntentSyncer keeps
// ServiceProviderCluster.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup
// in sync with the single role assignment that grants the mock First Party
// Application (FPA) principal the Owner role over the managed resource group.
//
// It only runs when the backend has no real FPA (HasRealFPA is false), where the
// mock FPA stands in for the real FPA. It does not call Azure.
//   - The desired set is exactly one key: the configured mock FPA principal ID
//     paired with the Owner role definition. TargetIdentity carries only the
//     PrincipalID, as the mock FPA service principal has no managed identity.
//   - A key that leaves the desired set (for example when the mock FPA principal
//     ID changes) is only removed once the replacement is live, so the managed
//     resource group never loses the Owner grant: the superseded key is kept
//     rendered until the desired key is configured in Azure, then stamped with
//     DeconfigureTimestamp (which excludes it from the published template so the
//     stack deletes it), then dropped once that StackDesire has reconciled the
//     removal (RoleAssignmentRemovedAndReconciled). Unlike the operator role
//     assignments controller there is no 24h wait; the drop is reconciliation
//     based.
//   - Cluster deletion is a no-op. The assignment is scoped to the managed
//     resource group, so Azure deletes it in cascade when that resource group is
//     removed.
type mockFPARoleAssignmentsIntentSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	deploymentStackDesireLister  azureresourceslisters.DeploymentStackDesireLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	// mockFPAPrincipalID is the principal ID of the mock FPA service principal that
	// is granted Owner over the managed resource group.
	mockFPAPrincipalID string
	// desiredRoleDefinitionResourceID is the role definition resource ID granted to
	// the mock FPA principal over the managed resource group (the Owner role).
	desiredRoleDefinitionResourceID string
}

var _ controllerutils.ClusterSyncer = (*mockFPARoleAssignmentsIntentSyncer)(nil)

// NewMockFPARoleAssignmentsIntentController creates a cluster-watching controller
// that marks the single mock FPA Owner role assignment key over the managed
// resource group. It must only be instantiated when the backend has no real FPA.
func NewMockFPARoleAssignmentsIntentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	azureResourcesInformers azureresourcesinformers.AzureResourcesInformers,
	mockFPAPrincipalID string,
	desiredRoleDefinitionResourceID string,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	deploymentStackDesireInformer, deploymentStackDesireLister := azureResourcesInformers.DeploymentStackDesires()

	syncer := &mockFPARoleAssignmentsIntentSyncer{
		clock:                           clock,
		clusterLister:                   clusterLister,
		serviceProviderClusterLister:    serviceProviderClusterLister,
		deploymentStackDesireLister:     deploymentStackDesireLister,
		resourcesDBClient:               resourcesDBClient,
		mockFPAPrincipalID:              mockFPAPrincipalID,
		desiredRoleDefinitionResourceID: desiredRoleDefinitionResourceID,
	}

	controller := controllerutils.NewClusterWatchingController(
		MockFPARoleAssignmentsIntentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)

	// AddInterestingResource only filters events, it does not subscribe to them.
	// Wire the DeploymentStackDesire informer as an actual event source so that
	// the stack desires controller writing an observed Azure status back onto the
	// desire re-triggers this controller, which consumes that status to advance its
	// stamp/drop state machine. Resync is 0: the periodic resync is done once on the
	// cluster (key) resource inside NewClusterWatchingController. This watch only
	// needs to fire on actual DeploymentStackDesire changes.
	if err := controller.QueueForChildInformers(0, deploymentStackDesireInformer); err != nil {
		panic(err) // coding error
	}

	controller.AddInterestingResource(azureresourcesapi.ClusterScopedDeploymentStackDesireResourceType, MockFPARoleAssignmentsDesireName)

	return controller
}

func (s *mockFPARoleAssignmentsIntentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	existingServiceProviderCluster, err := s.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// The dedicated CreateServiceProviderCluster controller creates it; we pick
		// it up on a later requeue once it exists.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	// Deletion must NOT short-circuit while the managed resource group still exists.
	// The mock FPA deletes the MRG using the role assignment(s) this controller marks,
	// so we must keep the desired key present during cluster teardown - otherwise the
	// role assignments controller sees no renderable keys and tears the assignment
	// down (or never creates it, if this controller only now caught up), leaving the
	// MRG undeletable and orphaned. Once the MRG is gone there is nothing left to keep
	// deletable, and the map is removed with the ServiceProviderCluster document during
	// teardown.
	//
	// The MRG is present while either reference is set (see managedResourceGroupScope):
	// during deletion the managed resource group controller never promotes pending to
	// confirmed (it only deletes), and it deletes a pending-only MRG too, so pending must
	// count as present here or a deletion that begins before confirmation would strand it.
	managedResourceGroupPresent := managedResourceGroupScope(existingServiceProviderCluster.Status.AzureResources.ManagedResourceGroup) != nil
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil && !managedResourceGroupPresent {
		return nil
	}

	if len(s.mockFPAPrincipalID) == 0 {
		return utils.TrackError(fmt.Errorf("mock FPA principal ID is not configured"))
	}
	if len(s.desiredRoleDefinitionResourceID) == 0 {
		return utils.TrackError(fmt.Errorf("owner role definition resource ID is not configured"))
	}

	// The published StackDesire tells us whether a stamped key has actually been
	// removed from Azure. Not-found means it was never published, so a stamped key
	// is not yet reconciled and is kept.
	var stackDesire *azureresourcesapi.DeploymentStackDesire
	stackDesire, err = s.deploymentStackDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, MockFPARoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		stackDesire = nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get mock FPA role assignments DeploymentStackDesire: %w", err))
	}

	desired := s.mergeRoleAssignments(
		existingServiceProviderCluster.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup,
		stackDesire,
	)

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = desired

	if !controllerutil.NeedsUpdate(existingServiceProviderCluster, replacement) {
		return nil
	}

	_, err = s.resourcesDBClient.ServiceProviderClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName, existingCluster.ID.Name).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// desiredKey is the single role assignment key this controller wants: the mock
// FPA principal paired with the Owner role definition.
func (s *mockFPARoleAssignmentsIntentSyncer) desiredKey() coreapi.RoleAssignmentKey {
	return coreapi.RoleAssignmentKey{
		PrincipalID:              s.mockFPAPrincipalID,
		RoleDefinitionResourceID: s.desiredRoleDefinitionResourceID,
	}
}

// mergeRoleAssignments builds the next map from the existing statuses and the
// published StackDesire. The desired key is kept or added with DeconfigureTimestamp
// cleared.
//
// A superseded key (one that has left the desired set, for example after an
// identity replacement) is handled so the managed resource group is never left
// without the Owner grant:
//   - It is kept rendered (not stamped) until the desired key is actually
//     configured in Azure, i.e. its reserved assignment is managed by the stack.
//     This keeps the old grant live until the replacement exists.
//   - Once the desired key is configured, the superseded key is stamped with
//     DeconfigureTimestamp, which drops it from the published template so the
//     stack deletes it.
//   - Once the stack has reconciled that removal (RoleAssignmentRemovedAndReconciled),
//     the key is dropped from the map.
func (s *mockFPARoleAssignmentsIntentSyncer) mergeRoleAssignments(
	existing map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus,
	stackDesire *azureresourcesapi.DeploymentStackDesire,
) map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus {
	desiredKey := s.desiredKey()
	next := make(map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus, len(existing)+1)

	// Desired key: keep or add, clearing any deconfigure stamp.
	if existingStatus, ok := existing[desiredKey]; ok && existingStatus != nil {
		status := existingStatus.DeepCopy()
		status.DeconfigureTimestamp = nil
		next[desiredKey] = status
	} else {
		next[desiredKey] = &coreapi.MockFPARoleAssignmentStatus{}
	}

	// Whether the desired assignment is actually live in Azure. A superseded
	// assignment is only removed once this is true, so we never lose the Owner
	// grant over the managed resource group during an identity replacement.
	desiredConfigured := coreapihelpers.MockFPARoleAssignmentStatusManaged(next[desiredKey], stackDesire)

	// Any other key has left the desired set.
	for existingKey, existingStatus := range existing {
		if existingKey == desiredKey || existingStatus == nil {
			continue
		}
		if existingStatus.DeconfigureTimestamp == nil {
			if !desiredConfigured {
				// Keep the superseded assignment live until the replacement is
				// configured in Azure.
				next[existingKey] = existingStatus.DeepCopy()
				continue
			}
			status := existingStatus.DeepCopy()
			stamp := metav1.NewTime(s.clock.Now())
			status.DeconfigureTimestamp = &stamp
			next[existingKey] = status
			continue
		}
		// Already stamped: drop only once the stack has reconciled the removal.
		if coreapihelpers.MockFPARoleAssignmentRemovedAndReconciled(existingStatus, stackDesire) {
			continue
		}
		next[existingKey] = existingStatus.DeepCopy()
	}

	return next
}
