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
	"math/rand/v2"
	"strings"
	"time"

	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const ClusterDenyAssignmentControllerName = "ClusterDenyAssignment"

type clusterDenyAssignmentSyncer struct {
	clock                 utilsclock.PassiveClock
	resourcesDBClient     corecosmosstorage.ResourcesDBClient
	clusterLister         corelisters.ClusterLister
	subscriptionLister    corelisters.SubscriptionLister
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*clusterDenyAssignmentSyncer)(nil)

func NewClusterDenyAssignmentController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers coreinformers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	syncer := &clusterDenyAssignmentSyncer{
		clock:                 clock,
		resourcesDBClient:     resourcesDBClient,
		clusterLister:         clusterLister,
		subscriptionLister:    subscriptionLister,
		azureFPAClientBuilder: azureFPAClientBuilder,
	}

	return controllerutils.NewClusterWatchingController(
		ClusterDenyAssignmentControllerName,
		resourcesDBClient,
		backendInformers,
		nil,
		time.Minute,
		syncer,
	)
}

func (c *clusterDenyAssignmentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	// Nothing to do while the cluster is being deleted. The deny assignments are scoped to the
	// managed resource group, so Azure deletes them in cascade when that resource group is removed
	// during cluster teardown; there is no need to issue ARM deletions or otherwise reconcile them
	// here. (Per Manyanda Chitimbo's note on https://github.com/Azure/ARO-HCP/pull/6269#discussion_r3656341978.)
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	return c.syncDenyAssignmentUpsert(ctx, key, cluster)
}

func (c *clusterDenyAssignmentSyncer) syncDenyAssignmentNeedsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	if len(controllerutils.ClusterServiceIDForCluster(cluster)) == 0 {
		return false
	}
	// Deny assignments are scoped to the managed resource group, which is derived from the
	// cluster's ManagedResourceGroup name. (Status.AzureResources.ManagedResourceGroup.AzureResource
	// is not populated by any controller, so it cannot be relied on here.)
	if len(cluster.CustomerProperties.Platform.ManagedResourceGroup) == 0 {
		return false
	}

	// we need these identities to exclude them from deny assignments.
	identities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
	if len(identities.ControlPlaneOperators) == 0 || len(identities.DataPlaneOperators) == 0 || identities.ServiceManagedIdentity == nil {
		return false
	}
	for _, v := range identities.ControlPlaneOperators {
		if v == nil {
			return false
		}
	}
	for _, v := range identities.DataPlaneOperators {
		if v == nil {
			return false
		}
	}

	if len(serviceProviderCluster.Status.AzureResources.DenyAssignments.PendingAzureResources) > 0 {
		return true
	}
	if len(serviceProviderCluster.Status.AzureResources.DenyAssignments.AzureResources) == 0 {
		return true
	}
	if t := serviceProviderCluster.Status.AzureResources.DenyAssignments.EarliestRecheckTime; t != nil && c.clock.Now().Before(t.Time) {
		return false
	}
	return true
}

func (c *clusterDenyAssignmentSyncer) syncDenyAssignmentUpsert(ctx context.Context, key controllerutils.HCPClusterKey, cluster *coreapi.HCPOpenShiftCluster) error {
	logger := utils.LoggerFromContext(ctx)

	serviceProviderCluster, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, cluster.ID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if !c.syncDenyAssignmentNeedsWork(cluster, serviceProviderCluster) {
		return nil
	}

	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	if subscription.Properties == nil || subscription.Properties.TenantId == nil {
		return utils.TrackError(fmt.Errorf("subscription %s has no tenantId", key.SubscriptionID))
	}
	tenantID := *subscription.Properties.TenantId

	serviceProviderClusterCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)

	// The managed resource group is the scope for the cluster's deny assignments. Build its resource
	// ID from the cluster's ManagedResourceGroup name (the same source allDenyAssignmentReferences
	// uses for the deny assignment resource IDs).
	managedResourceGroupID, err := coreapi.ToResourceGroupResourceID(key.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build managed resource group resource ID: %w", err))
	}

	requiredDenyAssignmentReferences, err := allDenyAssignmentReferences(cluster)
	if err != nil {
		return utils.TrackError(err)
	}
	requiredDenyAssignmentReferenceByType := make(map[string]coreapi.DenyAssignmentReference, len(requiredDenyAssignmentReferences))
	for _, ref := range requiredDenyAssignmentReferences {
		requiredDenyAssignmentReferenceByType[ref.DenyAssignmentType] = ref
	}
	denyAssignmentDefs := denyAssignmentDefinitions(cluster)
	denyAssignmentDefinitionsByType := make(map[string]denyAssignmentDefinition, len(denyAssignmentDefs))
	for _, d := range denyAssignmentDefs {
		denyAssignmentDefinitionsByType[d.denyAssignmentType] = d
	}

	genericResourcesClient, err := c.azureFPAClientBuilder.GenericResourcesClient(tenantID, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create generic resources client: %w", err))
	}
	denyAssignmentsClient, err := c.azureFPAClientBuilder.DenyAssignmentsClient(tenantID, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create deny assignments client: %w", err))
	}

	// Delete any deny assignments whose type is no longer required.
	replacement := serviceProviderCluster.DeepCopy()
	var staleDeletionErrs []error
	for _, existing := range serviceProviderCluster.Status.AzureResources.DenyAssignments.AzureResources {
		if _, isRequired := requiredDenyAssignmentReferenceByType[existing.DenyAssignmentType]; !isRequired {
			if err := c.deleteDenyAssignment(ctx, genericResourcesClient, existing.DenyAssignmentResourceID); err != nil {
				staleDeletionErrs = append(staleDeletionErrs, utils.TrackError(fmt.Errorf("failed to delete stale deny assignment %s: %w", existing.DenyAssignmentType, err)))
				continue
			}
			logger.Info("Deleted stale deny assignment from Azure", "denyAssignmentType", existing.DenyAssignmentType)
			replacement.Status.AzureResources.DenyAssignments.AzureResources = removeDenyAssignmentRef(replacement.Status.AzureResources.DenyAssignments.AzureResources, existing.DenyAssignmentType)
		}
	}
	serviceProviderCluster, replacement, err = replaceServiceProviderClusterIfChanged(ctx, serviceProviderClusterCRUD, serviceProviderCluster, replacement, staleDeletionErrs)
	if serviceProviderCluster == nil || err != nil {
		return err
	}

	// Ensure all existing deny assignments have correct content.
	// Succeeded stay in AzureResources; failed move to pending for retry.
	ensureExistingSucceeded, ensureExistingFailed, ensureExistingErr := c.ensureDenyAssignmentReferences(ctx, cluster, denyAssignmentsClient, genericResourcesClient,
		managedResourceGroupID, denyAssignmentDefinitionsByType, replacement.Status.AzureResources.DenyAssignments.AzureResources)
	replacement.Status.AzureResources.DenyAssignments.AzureResources = ensureExistingSucceeded
	replacement.Status.AzureResources.DenyAssignments.PendingAzureResources = appendDenyAssignmentReference(replacement.Status.AzureResources.DenyAssignments.PendingAzureResources, ensureExistingFailed...)
	serviceProviderCluster, replacement, err = replaceServiceProviderClusterIfChanged(ctx, serviceProviderClusterCRUD, serviceProviderCluster, replacement, []error{ensureExistingErr})
	if serviceProviderCluster == nil || err != nil {
		return err
	}

	// Add any required types not already in AzureResources or pending.
	for _, ref := range requiredDenyAssignmentReferences {
		found := false
		for _, existing := range replacement.Status.AzureResources.DenyAssignments.AzureResources {
			if existing.DenyAssignmentType == ref.DenyAssignmentType {
				found = true
				break
			}
		}
		if !found {
			replacement.Status.AzureResources.DenyAssignments.PendingAzureResources = appendDenyAssignmentReference(replacement.Status.AzureResources.DenyAssignments.PendingAzureResources, ref)
		}
	}
	serviceProviderCluster, replacement, err = replaceServiceProviderClusterIfChanged(ctx, serviceProviderClusterCRUD, serviceProviderCluster, replacement, nil)
	if serviceProviderCluster == nil || err != nil {
		return err
	}

	// Ensure all pending deny assignments exist in Azure with correct content.
	// Succeeded move to AzureResources; failed stay in pending.
	ensurePendingSucceeded, ensurePendingFailed, ensurePendingErr := c.ensureDenyAssignmentReferences(ctx, cluster, denyAssignmentsClient, genericResourcesClient,
		managedResourceGroupID, denyAssignmentDefinitionsByType, replacement.Status.AzureResources.DenyAssignments.PendingAzureResources)
	replacement.Status.AzureResources.DenyAssignments.AzureResources = appendDenyAssignmentReference(replacement.Status.AzureResources.DenyAssignments.AzureResources, ensurePendingSucceeded...)
	replacement.Status.AzureResources.DenyAssignments.PendingAzureResources = ensurePendingFailed
	serviceProviderCluster, replacement, err = replaceServiceProviderClusterIfChanged(ctx, serviceProviderClusterCRUD, serviceProviderCluster, replacement, []error{ensurePendingErr})
	if serviceProviderCluster == nil || err != nil {
		return err
	}

	if len(replacement.Status.AzureResources.DenyAssignments.PendingAzureResources) == 0 {
		replacement.Status.AzureResources.DenyAssignments.EarliestRecheckTime = c.recheckTime()
	} else {
		// we're unlikely to reach here with PendingAzureResource, but if we do, running again is probably a good idea.
		replacement.Status.AzureResources.DenyAssignments.EarliestRecheckTime = nil
	}
	_, _, err = replaceServiceProviderClusterIfChanged(ctx, serviceProviderClusterCRUD, serviceProviderCluster, replacement, nil)
	return err
}

func replaceServiceProviderClusterIfChanged(
	ctx context.Context,
	serviceProviderClusterCRUD cosmosstorageutils.ResourceCRUD[coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster],
	serviceProviderCluster *coreapi.ServiceProviderCluster,
	replacement *coreapi.ServiceProviderCluster,
	priorErrs []error,
) (*coreapi.ServiceProviderCluster, *coreapi.ServiceProviderCluster, error) {
	joinedPriorErr := errors.Join(priorErrs...)
	if equality.Semantic.DeepEqual(serviceProviderCluster, replacement) {
		if joinedPriorErr != nil {
			return nil, nil, joinedPriorErr
		}
		return serviceProviderCluster, replacement, nil
	}
	updated, err := serviceProviderClusterCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		if joinedPriorErr != nil {
			return nil, nil, joinedPriorErr
		}
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, errors.Join(joinedPriorErr, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))
	}
	return updated, updated.DeepCopy(), nil
}

func (c *clusterDenyAssignmentSyncer) ensureDenyAssignmentReferences(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	denyAssignmentsClient azureclient.DenyAssignmentsClient,
	genericResourcesClient azureclient.GenericResourcesClient,
	scope *azcorearm.ResourceID,
	denyAssignmentDefinitionsByType map[string]denyAssignmentDefinition,
	refs []coreapi.DenyAssignmentReference,
) (succeeded, failed []coreapi.DenyAssignmentReference, err error) {
	logger := utils.LoggerFromContext(ctx)
	var errs []error

	for _, ref := range refs {
		definition, ok := denyAssignmentDefinitionsByType[ref.DenyAssignmentType]
		if !ok {
			// A pending/tracked type with no matching definition can never be reconciled and would
			// otherwise keep the cluster blocked while the controller reports success. Surface it as
			// an error so the sync degrades and the problem is visible.
			errs = append(errs, utils.TrackError(fmt.Errorf("no definition for deny assignment type %q", ref.DenyAssignmentType)))
			failed = append(failed, ref)
			continue
		}

		excludedIdentityResourceIDs, err := collectExcludedPrincipalIDs(cluster, definition)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to collect excluded identity resource IDs for %s: %w", ref.DenyAssignmentType, err)))
			failed = append(failed, ref)
			continue
		}

		err = c.ensureDenyAssignment(ctx, cluster, denyAssignmentsClient, genericResourcesClient,
			ref.DenyAssignmentResourceID, scope, excludedIdentityResourceIDs,
			definition.actions, definition.notActions, definition.dataActions)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to ensure deny assignment %s: %w", ref.DenyAssignmentType, err)))
			failed = append(failed, ref)
			continue
		}

		logger.Info("Ensured deny assignment", "denyAssignmentType", ref.DenyAssignmentType, "resourceID", ref.DenyAssignmentResourceID.String())
		succeeded = append(succeeded, ref)
	}

	return succeeded, failed, errors.Join(errs...)
}

func (c *clusterDenyAssignmentSyncer) ensureDenyAssignment(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster,
	denyAssignmentsClient azureclient.DenyAssignmentsClient,
	genericResourcesClient azureclient.GenericResourcesClient,
	resourceID *azcorearm.ResourceID,
	scope *azcorearm.ResourceID,
	excludedIdentityResourceIDs []*azcorearm.ResourceID,
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

	excludedPrincipalIDs, err := resolvePrincipalIDs(cluster, excludedIdentityResourceIDs)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to resolve principal IDs: %w", err))
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

func (c *clusterDenyAssignmentSyncer) deleteDenyAssignment(
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

func (c *clusterDenyAssignmentSyncer) recheckTime() *metav1.Time {
	recheckDuration := 12 * time.Hour
	jitter := time.Duration(rand.Int64N(int64(recheckDuration)))
	t := metav1.NewTime(c.clock.Now().Add(recheckDuration/2 + jitter))
	return &t
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

func collectExcludedPrincipalIDs(cluster *coreapi.HCPOpenShiftCluster, definition denyAssignmentDefinition) ([]*azcorearm.ResourceID, error) {
	var identityResourceIDs []*azcorearm.ResourceID

	identities := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities

	for _, operatorName := range definition.controlPlaneOperators {
		resourceID, ok := identities.ControlPlaneOperators[operatorName]
		if !ok || resourceID == nil {
			return nil, fmt.Errorf("control plane operator %q not found in cluster identity configuration", operatorName)
		}
		identityResourceIDs = append(identityResourceIDs, resourceID)
	}

	for _, operatorName := range definition.dataPlaneOperators {
		resourceID, ok := identities.DataPlaneOperators[operatorName]
		if !ok || resourceID == nil {
			return nil, fmt.Errorf("data plane operator %q not found in cluster identity configuration", operatorName)
		}
		identityResourceIDs = append(identityResourceIDs, resourceID)
	}

	if definition.includeServiceManagedID {
		if identities.ServiceManagedIdentity == nil {
			return nil, fmt.Errorf("service managed identity not found in cluster identity configuration")
		}
		identityResourceIDs = append(identityResourceIDs, identities.ServiceManagedIdentity)
	}

	return identityResourceIDs, nil
}

func resolvePrincipalIDs(cluster *coreapi.HCPOpenShiftCluster, identityResourceIDs []*azcorearm.ResourceID) ([]string, error) {
	if cluster.Identity == nil {
		return nil, fmt.Errorf("cluster has no identity configuration")
	}

	lookup := make(map[string]string, len(cluster.Identity.UserAssignedIdentities))
	for resourceID, identity := range cluster.Identity.UserAssignedIdentities {
		if identity != nil && identity.PrincipalID != nil {
			lookup[strings.ToLower(resourceID)] = *identity.PrincipalID
		}
	}

	principalIDs := make([]string, 0, len(identityResourceIDs))
	for _, identityResourceID := range identityResourceIDs {
		principalID, ok := lookup[strings.ToLower(identityResourceID.String())]
		if !ok {
			return nil, fmt.Errorf("principal ID not found for identity %s", identityResourceID.String())
		}
		principalIDs = append(principalIDs, principalID)
	}
	return principalIDs, nil
}

func appendDenyAssignmentReference(slice []coreapi.DenyAssignmentReference, refs ...coreapi.DenyAssignmentReference) []coreapi.DenyAssignmentReference {
	existing := make(map[string]struct{}, len(slice))
	for _, ref := range slice {
		existing[ref.DenyAssignmentType] = struct{}{}
	}
	for _, ref := range refs {
		if _, ok := existing[ref.DenyAssignmentType]; !ok {
			slice = append(slice, ref)
			existing[ref.DenyAssignmentType] = struct{}{}
		}
	}
	return slice
}

func removeDenyAssignmentRef(slice []coreapi.DenyAssignmentReference, denyAssignmentType string) []coreapi.DenyAssignmentReference {
	result := make([]coreapi.DenyAssignmentReference, 0, len(slice))
	for _, denyAssignmentReference := range slice {
		if denyAssignmentReference.DenyAssignmentType != denyAssignmentType {
			result = append(result, denyAssignmentReference)
		}
	}
	return result
}
