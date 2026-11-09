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
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// MockFPARoleAssignmentIntentControllerName is the single source of truth for
// this controller's name. It is used for the workqueue name (a Prometheus
// label), context/logger controller name, and log fields.
const MockFPARoleAssignmentIntentControllerName = "MockFPARoleAssignmentIntent"

// OwnerRoleDefinitionResourceID is the tenant-level resource ID of the Azure
// built-in Owner role definition. It is the role the mock First Party Application
// principal is granted over each cluster's managed resource group.
// The ID has been obtained from https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/privileged#owner.
const OwnerRoleDefinitionResourceID = "/providers/Microsoft.Authorization/roleDefinitions/8e3af657-a8ff-443c-a75c-2fe8c4bcb635"

// mockFPARoleAssignmentIntentSyncer keeps
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
type mockFPARoleAssignmentIntentSyncer struct {
	clock                        utilsclock.PassiveClock
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	stackDesireLister            corelisters.StackDesireLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	// mockFPAPrincipalID is the principal ID of the mock FPA service principal that
	// is granted Owner over the managed resource group.
	mockFPAPrincipalID string
	// desiredRoleDefinitionResourceID is the role definition resource ID granted to
	// the mock FPA principal over the managed resource group (the Owner role).
	desiredRoleDefinitionResourceID string
}

var _ controllerutils.ClusterSyncer = (*mockFPARoleAssignmentIntentSyncer)(nil)

// NewMockFPARoleAssignmentIntentController creates a cluster-watching controller
// that marks the single mock FPA Owner role assignment key over the managed
// resource group. It must only be instantiated when the backend has no real FPA.
func NewMockFPARoleAssignmentIntentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	backendInformers coreinformers.BackendInformers,
	mockFPAPrincipalID string,
	desiredRoleDefinitionResourceID string,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, stackDesireLister := backendInformers.StackDesires()

	syncer := &mockFPARoleAssignmentIntentSyncer{
		clock:                           clock,
		clusterLister:                   clusterLister,
		serviceProviderClusterLister:    serviceProviderClusterLister,
		stackDesireLister:               stackDesireLister,
		resourcesDBClient:               resourcesDBClient,
		mockFPAPrincipalID:              mockFPAPrincipalID,
		desiredRoleDefinitionResourceID: desiredRoleDefinitionResourceID,
	}

	controller := controllerutils.NewClusterWatchingController(
		MockFPARoleAssignmentIntentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(coreapi.StackDesireResourceType, MockFPARoleAssignmentsDesireName)

	return controller
}

func (s *mockFPARoleAssignmentIntentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := s.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster from cache: %w", err))
	}

	// The assignment is scoped to the managed resource group, so Azure deletes it
	// in cascade when that resource group is removed during cluster teardown.
	// There is no need to stamp DeconfigureTimestamp here.
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
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

	if len(s.mockFPAPrincipalID) == 0 {
		return utils.TrackError(fmt.Errorf("mock FPA principal ID is not configured"))
	}
	if len(s.desiredRoleDefinitionResourceID) == 0 {
		return utils.TrackError(fmt.Errorf("owner role definition resource ID is not configured"))
	}

	// The published StackDesire tells us whether a stamped key has actually been
	// removed from Azure. Not-found means it was never published, so a stamped key
	// is not yet reconciled and is kept.
	var stackDesire *coreapi.StackDesire
	stackDesire, err = s.stackDesireLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, MockFPARoleAssignmentsDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		stackDesire = nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get mock FPA role assignments StackDesire: %w", err))
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
func (s *mockFPARoleAssignmentIntentSyncer) desiredKey() coreapi.RoleAssignmentKey {
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
func (s *mockFPARoleAssignmentIntentSyncer) mergeRoleAssignments(
	existing map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus,
	stackDesire *coreapi.StackDesire,
) map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus {
	desiredKey := s.desiredKey()
	next := make(map[coreapi.RoleAssignmentKey]*coreapi.RoleAssignmentStatus, len(existing)+1)

	// Desired key: keep or add, clearing any deconfigure stamp.
	target := &coreapi.RoleAssignmentTargetIdentity{PrincipalID: s.mockFPAPrincipalID}
	if existingStatus, ok := existing[desiredKey]; ok && existingStatus != nil {
		status := existingStatus.DeepCopy()
		status.DeconfigureTimestamp = nil
		status.TargetIdentity = target
		next[desiredKey] = status
	} else {
		next[desiredKey] = &coreapi.RoleAssignmentStatus{TargetIdentity: target}
	}

	// Whether the desired assignment is actually live in Azure. A superseded
	// assignment is only removed once this is true, so we never lose the Owner
	// grant over the managed resource group during an identity replacement.
	desiredConfigured := coreapihelpers.RoleAssignmentStatusManaged(next[desiredKey], stackDesire)

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
		if coreapihelpers.RoleAssignmentRemovedAndReconciled(existingStatus, stackDesire) {
			continue
		}
		next[existingKey] = existingStatus.DeepCopy()
	}

	return next
}
