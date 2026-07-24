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

	"github.com/google/uuid"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const ClusterDenyAssignmentControllerName = "ClusterDenyAssignment"

type clusterDenyAssignmentSyncer struct {
	resourcesDBClient            database.ResourcesDBClient
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	subscriptionLister           listers.SubscriptionLister
	azureFPAClientBuilder        azureclient.FirstPartyApplicationClientBuilder
}

var _ controllerutils.ClusterSyncer = (*clusterDenyAssignmentSyncer)(nil)

func NewClusterDenyAssignmentController(
	resourcesDBClient database.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, subscriptionLister := backendInformers.Subscriptions()
	syncer := &clusterDenyAssignmentSyncer{
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		subscriptionLister:           subscriptionLister,
		azureFPAClientBuilder:        azureFPAClientBuilder,
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

func (c *clusterDenyAssignmentSyncer) deletionNeedsWork(serviceProviderCluster *api.ServiceProviderCluster) bool {
	return len(serviceProviderCluster.Status.DenyAssignments) > 0 || len(serviceProviderCluster.Status.PendingDenyAssignments) > 0
}

func (c *clusterDenyAssignmentSyncer) creationNeedsWork(cluster *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}
	if cluster.ServiceProviderProperties.ClusterServiceID != nil &&
		len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0 {
		return false
	}
	if len(serviceProviderCluster.Status.PendingDenyAssignments) == 0 && len(serviceProviderCluster.Status.DenyAssignments) > 0 {
		return false
	}
	return true
}

func (c *clusterDenyAssignmentSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return c.syncDeletion(ctx, key, cluster)
	}

	if cluster.ServiceProviderProperties.ClusterServiceID != nil &&
		len(cluster.ServiceProviderProperties.ClusterServiceID.String()) > 0 {
		return nil
	}

	return c.syncCreation(ctx, key, cluster)
}

func (c *clusterDenyAssignmentSyncer) syncCreation(ctx context.Context, key controllerutils.HCPClusterKey, cluster *api.HCPOpenShiftCluster) error {
	logger := utils.LoggerFromContext(ctx)

	serviceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, cluster.ID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if !c.creationNeedsWork(cluster, serviceProviderCluster) {
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

	if len(serviceProviderCluster.Status.PendingDenyAssignments) == 0 && len(serviceProviderCluster.Status.DenyAssignments) == 0 {
		refs, err := allDenyAssignmentReferences(cluster)
		if err != nil {
			return utils.TrackError(err)
		}
		serviceProviderCluster.Status.PendingDenyAssignments = refs
		serviceProviderCluster, err = serviceProviderClusterCRUD.Replace(ctx, serviceProviderCluster, nil)
		if database.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		logger.Info("Initialized PendingDenyAssignments", "count", len(serviceProviderCluster.Status.PendingDenyAssignments))
	}

	genericResourcesClient, err := c.azureFPAClientBuilder.GenericResourcesClient(tenantID, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create generic resources client: %w", err))
	}

	denyAssignmentsClient, err := c.azureFPAClientBuilder.DenyAssignmentsClient(tenantID, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create deny assignments client: %w", err))
	}

	definitions := denyAssignmentDefinitions(cluster)
	defByType := make(map[string]denyAssignmentDefinition, len(definitions))
	for _, d := range definitions {
		defByType[d.denyAssignmentType] = d
	}

	managedResourceGroupID, err := api.ToResourceGroupResourceID(key.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build managed resource group resource ID: %w", err))
	}

	var ensured []api.DenyAssignmentReference
	var errs []error
	for _, denyAssignmentReference := range serviceProviderCluster.Status.PendingDenyAssignments {
		definition, ok := defByType[denyAssignmentReference.DenyAssignmentType]
		if !ok {
			logger.Error(nil, "Skipping unknown deny assignment type", "denyAssignmentType", denyAssignmentReference.DenyAssignmentType)
			continue
		}

		excludedIdentityResourceIDs, err := collectExcludedPrincipalIDs(cluster, definition)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to collect excluded identity resource IDs for %s: %w", denyAssignmentReference.DenyAssignmentType, err)))
			continue
		}

		err = c.ensureDenyAssignment(ctx, cluster, denyAssignmentsClient, genericResourcesClient, denyAssignmentReference.DenyAssignmentResourceID,
			managedResourceGroupID, excludedIdentityResourceIDs, definition.actions, definition.notActions, definition.dataActions)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to ensure deny assignment %s: %w", denyAssignmentReference.DenyAssignmentType, err)))
			continue
		}

		logger.Info("Ensured deny assignment", "denyAssignmentType", denyAssignmentReference.DenyAssignmentType, "resourceID", denyAssignmentReference.DenyAssignmentResourceID.String())
		ensured = append(ensured, denyAssignmentReference)
	}

	if len(ensured) > 0 {
		for _, denyAssignmentReference := range ensured {
			serviceProviderCluster.Status.DenyAssignments = append(serviceProviderCluster.Status.DenyAssignments, denyAssignmentReference)
			serviceProviderCluster.Status.PendingDenyAssignments = removeDenyAssignmentRef(serviceProviderCluster.Status.PendingDenyAssignments, denyAssignmentReference.DenyAssignmentType)
		}
		_, err = serviceProviderClusterCRUD.Replace(ctx, serviceProviderCluster, nil)
		if err != nil && !database.IsPreconditionFailedError(err) {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))
		}
	}

	return errors.Join(errs...)
}

func (c *clusterDenyAssignmentSyncer) syncDeletion(ctx context.Context, key controllerutils.HCPClusterKey, cluster *api.HCPOpenShiftCluster) error {
	logger := utils.LoggerFromContext(ctx)

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !c.deletionNeedsWork(serviceProviderCluster) {
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

	genericResourcesClient, err := c.azureFPAClientBuilder.GenericResourcesClient(tenantID, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create generic resources client: %w", err))
	}

	serviceProviderClusterCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)

	allRefs := make([]api.DenyAssignmentReference, 0, len(serviceProviderCluster.Status.DenyAssignments)+len(serviceProviderCluster.Status.PendingDenyAssignments))
	allRefs = append(allRefs, serviceProviderCluster.Status.DenyAssignments...)
	allRefs = append(allRefs, serviceProviderCluster.Status.PendingDenyAssignments...)

	var deletedDenyAssignmentTypes []string
	var errs []error
	for _, denyAssignmentReference := range allRefs {
		err = c.deleteDenyAssignment(ctx, genericResourcesClient, denyAssignmentReference.DenyAssignmentResourceID)
		if err != nil {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to delete deny assignment %s: %w", denyAssignmentReference.DenyAssignmentType, err)))
			continue
		}

		logger.Info("Deleted deny assignment", "denyAssignmentType", denyAssignmentReference.DenyAssignmentType, "resourceID", denyAssignmentReference.DenyAssignmentResourceID.String())
		deletedDenyAssignmentTypes = append(deletedDenyAssignmentTypes, denyAssignmentReference.DenyAssignmentType)
	}

	if len(deletedDenyAssignmentTypes) > 0 {
		for _, denyAssignmentType := range deletedDenyAssignmentTypes {
			serviceProviderCluster.Status.DenyAssignments = removeDenyAssignmentRef(serviceProviderCluster.Status.DenyAssignments, denyAssignmentType)
			serviceProviderCluster.Status.PendingDenyAssignments = removeDenyAssignmentRef(serviceProviderCluster.Status.PendingDenyAssignments, denyAssignmentType)
		}
		_, err = serviceProviderClusterCRUD.Replace(ctx, serviceProviderCluster, nil)
		if err != nil && !database.IsPreconditionFailedError(err) {
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err)))
		}
	}

	return errors.Join(errs...)
}

func (c *clusterDenyAssignmentSyncer) ensureDenyAssignment(
	ctx context.Context,
	cluster *api.HCPOpenShiftCluster,
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

func generateDenyAssignmentUUID(clusterID, denyAssignmentType string) string {
	namespace := uuid.MustParse(denyAssignmentNamespaceUUID)
	return uuid.NewSHA1(namespace, []byte(denyAssignmentType+"$"+clusterID)).String()
}

func collectExcludedPrincipalIDs(cluster *api.HCPOpenShiftCluster, definition denyAssignmentDefinition) ([]*azcorearm.ResourceID, error) {
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

func resolvePrincipalIDs(cluster *api.HCPOpenShiftCluster, identityResourceIDs []*azcorearm.ResourceID) ([]string, error) {
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

func removeDenyAssignmentRef(slice []api.DenyAssignmentReference, denyAssignmentType string) []api.DenyAssignmentReference {
	result := make([]api.DenyAssignmentReference, 0, len(slice))
	for _, denyAssignmentReference := range slice {
		if denyAssignmentReference.DenyAssignmentType != denyAssignmentType {
			result = append(result, denyAssignmentReference)
		}
	}
	return result
}
