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
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/azureresourcesapihelpers"
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

	if len(mockFPAPrincipalID) == 0 {
		panic("mock FPA principal ID is not configured")
	}
	if len(desiredRoleDefinitionResourceID) == 0 {
		panic("desired role definition resource ID is not configured")
	}

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

	// The published DeploymentStackDesire tells us whether a stamped key has actually been
	// removed from Azure. Not-found means it was never published, so a stamped key
	// is not yet reconciled and is kept.
	var existingDeploymentStackDesire *azureresourcesapi.DeploymentStackDesire
	existingDeploymentStackDesire, err = s.deploymentStackDesireLister.GetForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, MockFPARoleAssignmentsDesireName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get mock FPA role assignments DeploymentStackDesire: %w", err))
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		existingDeploymentStackDesire = nil
	}

	desiredMockFPARoleAssignmentsStatus, err := s.mergeRoleAssignments(
		existingServiceProviderCluster.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup,
		existingDeploymentStackDesire,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to calculate desired mock FPA role assignments: %w", err))
	}

	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.MockFirstPartyApplicationRoleAssignmentsOverManagedResourceGroup = desiredMockFPARoleAssignmentsStatus

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

// desiredRoleAssignmentKey is the single role assignment key this controller wants: the mock
// FPA principal paired with the desired role definition.
func (s *mockFPARoleAssignmentsIntentSyncer) desiredRoleAssignmentKey() coreapi.RoleAssignmentKey {
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
// without the desired grant:
//   - It is kept rendered (not stamped) until the desired key is actually
//     configured in Azure, i.e. its reserved assignment is managed by the stack.
//     This keeps the old grant live until the replacement exists.
//   - Once the desired key is configured, the superseded key is stamped with
//     DeconfigureTimestamp, which drops it from the published template so the
//     stack deletes it.
//   - Once the stack has reconciled that removal (RoleAssignmentRemovedAndReconciled),
//     the key is dropped from the map.
//
// existingMockFPARoleAssignments: the existing role assignments statuses. It can be nil.
// existingDeploymentStackDesire: the published DeploymentStackDesire. It can be nil if the DeploymentStackDesire is not yet stored in the database.
func (s *mockFPARoleAssignmentsIntentSyncer) mergeRoleAssignments(
	existingMockFPARoleAssignments map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus,
	existingDeploymentStackDesire *azureresourcesapi.DeploymentStackDesire,
) (map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus, error) {
	desiredRoleAssignmentKey := s.desiredRoleAssignmentKey()
	desiredMockFPARoleAssignments := make(map[coreapi.RoleAssignmentKey]*coreapi.MockFPARoleAssignmentStatus)

	// Desired key: add or keep. If it's keep, clear any deconfigure timestamp.
	existingMockFPARoleAssignmentStatus, ok := existingMockFPARoleAssignments[desiredRoleAssignmentKey]
	desiredMockFPARoleAssignments[desiredRoleAssignmentKey] = &coreapi.MockFPARoleAssignmentStatus{}
	if ok {
		newStatus := existingMockFPARoleAssignmentStatus.DeepCopy()
		newStatus.DeconfigureTimestamp = nil
		desiredMockFPARoleAssignments[desiredRoleAssignmentKey] = newStatus
	}

	// Whether the desired assignment is confirmed live in Azure per the current,
	// successful stack reconcile (not merely a stale or in-flight "managed"
	// observation). A superseded assignment is only removed once this is true, so we
	// never lose the Owner grant over the managed resource group during an identity
	// replacement, and we only begin that removal from a confirmed, current state.
	desiredConfigured, err := s.roleAssignmentSuccessfullyConfigured(desiredMockFPARoleAssignments[desiredRoleAssignmentKey], existingDeploymentStackDesire)
	if err != nil {
		return nil, err
	}

	timeForEntriesToDeconfigure := s.clock.Now()

	// Any other key has left the desired set.
	for existingKey, existingStatus := range existingMockFPARoleAssignments {
		// If we are processing the currently desired key we skip the iteration as we don't want to deconfigure it.
		if existingKey == desiredRoleAssignmentKey {
			continue
		}

		if existingStatus.DeconfigureTimestamp != nil {
			// Already stamped: drop only once the stack has reconciled the removal.
			removedAndReconciled, err := s.roleAssignmentRemovedAndReconciled(existingStatus, existingDeploymentStackDesire)
			if err != nil {
				return nil, utils.TrackError(fmt.Errorf("failed to check if mock FPA role assignment %+v has been removed and reconciled: %w", existingKey, err))
			}
			if removedAndReconciled {
				continue
			}
			desiredMockFPARoleAssignments[existingKey] = existingStatus.DeepCopy()
			continue
		}

		// At this point we know the assignment is not stamped yet.

		if !desiredConfigured {
			// Keep the superseded assignment live until the replacement is
			// configured in Azure.
			desiredMockFPARoleAssignments[existingKey] = existingStatus.DeepCopy()
			continue
		}
		desiredStatus := existingStatus.DeepCopy()
		stamp := metav1.NewTime(timeForEntriesToDeconfigure)
		desiredStatus.DeconfigureTimestamp = &stamp
		desiredMockFPARoleAssignments[existingKey] = desiredStatus
	}

	return desiredMockFPARoleAssignments, nil
}

// roleAssignmentSuccessfullyConfigured reports whether status's reserved assignment is
// managed per a current, successful reconciliation of desire: the currently published
// spec was applied successfully (ObservedSpecHash matches and SuccessfullyApplied is true)
// and that spec manages the assignment. Unlike the bare "last-observed managed" check
// (MockFPARoleAssignmentStatusManaged), this gates on the observation being current and
// the apply having succeeded, so the intent controller begins draining a superseded
// assignment only once the replacement is confirmed live - not off a stale or in-flight
// observation.
func (s *mockFPARoleAssignmentsIntentSyncer) roleAssignmentSuccessfullyConfigured(status *coreapi.MockFPARoleAssignmentStatus, desire *azureresourcesapi.DeploymentStackDesire) (bool, error) {
	if desire == nil {
		return false, nil
	}
	hash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	if err != nil {
		return false, fmt.Errorf("failed to hash mock FPA role assignments DeploymentStackDesire spec: %w", err)
	}
	if desire.Status.ObservedSpecHash != hash {
		return false, nil
	}
	if !meta.IsStatusConditionTrue(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) {
		return false, nil
	}
	return coreapihelpers.MockFPARoleAssignmentStatusManaged(status, desire), nil
}

// roleAssignmentRemovedAndReconciled reports whether a role assignment that has
// left the desired set (its DeconfigureTimestamp is stamped) has actually been
// removed from Azure by a successful, current reconciliation of desire. It is the
// drop criterion for reconciliation-based deconfigure (no time-based cooldown):
// the new desired set must be published in the stack and that stack must have
// reconciled successfully with it.
//
//   - A status whose ReservedAzureResourceID is nil was never materialized into a
//     stack, so there is nothing in Azure to remove: removed.
//   - Otherwise desire must exist, its observed outcome must correspond to the
//     currently published spec (ObservedSpecHash equals StackDesireSpecHash of
//     desire.Spec), and that outcome must be a success: a confirmed Delete, or an
//     Apply after which the reserved assignment is no longer managed by the stack
//     (dropped from the template and deleted via ActionOnUnmanage).
//
// desire may be nil: the caller passes nil when the mock FPA DeploymentStackDesire has
// not been published yet (a lister NotFound). With no published/observed stack a stamped
// assignment cannot have been reconciled, so a nil desire returns (false, nil).
func (s *mockFPARoleAssignmentsIntentSyncer) roleAssignmentRemovedAndReconciled(status *coreapi.MockFPARoleAssignmentStatus, desire *azureresourcesapi.DeploymentStackDesire) (bool, error) {
	// No reserved name was ever assigned, so the assignment was never rendered into a
	// stack and never created in Azure: there is nothing to remove, treat it as removed.
	if status.ReservedAzureResourceID == nil {
		return true, nil
	}
	// The StackDesire has not been published/observed yet, so we have no evidence the
	// removal reconciled: not removed.
	if desire == nil {
		return false, nil
	}

	specHash, err := azureresourcesapihelpers.DeploymentStackDesireSpecHash(desire.Spec)
	if err != nil {
		return false, fmt.Errorf("failed to hash mock FPA role assignments DeploymentStackDesire spec: %w", err)
	}
	// The observed spec belongs to an older spec than the one currently published, so
	// it says nothing about whether the current desired set (which drops this assignment)
	// has reconciled: not removed until a fresh observation lands.
	if desire.Status.ObservedSpecHash != specHash {
		return false, nil
	}

	// Delete intent: the assignment is removed only once the stack is confirmed deleted.
	if desire.Spec.Action == azureresourcesapi.DeploymentStackDesireActionTypeDelete {
		return meta.IsStatusConditionTrue(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyDeleted), nil
	}

	// Apply intent: nothing can be concluded until the current apply has succeeded.
	if !meta.IsStatusConditionTrue(desire.Status.Conditions, azureresourcesapi.DeploymentStackDesireConditionTypeSuccessfullyApplied) {
		return false, nil
	}
	// Apply succeeded but the observation carries no managed-resource list, so our
	// assignment is not among the managed resources: removed.
	if desire.Status.Stack == nil || desire.Status.Stack.Properties == nil {
		return true, nil
	}
	// Apply succeeded: look for our reserved assignment among the stack's resources.
	for _, resource := range desire.Status.Stack.Properties.Resources {
		if resource == nil || resource.ID == nil {
			continue
		}
		if strings.EqualFold(*resource.ID, status.ReservedAzureResourceID.String()) {
			// Still present in the stack's resource set, in ANY state. managed means it
			// is still owned; deleteFailed and removeDenyFailed mean Azure could not
			// remove it yet and will retry. None of these mean removed - a successfully
			// removed resource drops out of the list entirely (the return below). So if
			// we find it at all, it is not yet removed.
			return false, nil
		}
	}
	// Absent from the stack's resource set after a successful apply: removed.
	return true, nil
}
