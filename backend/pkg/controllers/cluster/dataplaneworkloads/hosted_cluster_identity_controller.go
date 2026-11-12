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

package dataplaneworkloads

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/denyassignments"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// HostedClusterDataPlaneIdentitiesControllerName is the canonical name used
	// as the workqueue name (Prometheus label), context controller name, and
	// name for the ServiceProviderCluster identity-intent writer.
	HostedClusterDataPlaneIdentitiesControllerName = "HostedClusterDataPlaneIdentities"
)

type hostedClusterDataPlaneClientIDs = kubeapplierhelpers.HostedClusterDataPlaneClientIDs

func clientIDsFromHostedCluster(hc *v1beta1.HostedCluster) hostedClusterDataPlaneClientIDs {
	return kubeapplierhelpers.DataPlaneClientIDsFromHostedCluster(hc)
}

// dataplaneIdentitySlot binds a ClusterOperatorIdentifier (the key used in
// CustomerProperties.DataPlaneOperators) to a setter that writes the resolved
// ClientID into the correct field of hostedClusterDataPlaneClientIDs.
// Iteration order is deterministic.
var dataplaneIdentitySlots = []struct {
	operator internalazure.ClusterOperatorIdentifier
	set      func(*hostedClusterDataPlaneClientIDs, string)
}{
	{
		internalazure.ClusterOperatorIdentifierImageRegistry,
		func(dp *hostedClusterDataPlaneClientIDs, id string) { dp.ImageRegistryMSIClientID = id },
	},
	{
		internalazure.ClusterOperatorIdentifierDiskCSIDriver,
		func(dp *hostedClusterDataPlaneClientIDs, id string) { dp.DiskMSIClientID = id },
	},
	{
		internalazure.ClusterOperatorIdentifierFileCSIDriver,
		func(dp *hostedClusterDataPlaneClientIDs, id string) { dp.FileMSIClientID = id },
	},
}

// hostedClusterDataPlaneIdentitySyncer writes the three managed identity
// ClientIDs into ServiceProviderCluster.Spec.DesiredHostedCluster. ClientIDs are read from
// ServiceProviderCluster.Status.ManagedIdentityDetails
// (MetadataFromARMUserAssignedIdentitiesAPI), populated by
// FetchManagedIdentitiesInfo. OIDC federation completion is checked via
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation
// (populated by DataPlaneWorkloadIdentityOIDCFederation). The ensured stamp
// confirms completed configuration for the current identity instance; it is
// historical evidence, not a live Azure health check.
type hostedClusterDataPlaneIdentitySyncer struct {
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	readDesireLister              kubeapplierlisters.ReadDesireLister
	clusterScopedIdentitiesConfig *internalazure.ClusterScopedIdentitiesConfig
	enabled                       bool
}

var _ controllerutils.ClusterSyncer = (*hostedClusterDataPlaneIdentitySyncer)(nil)

// NewHostedClusterDataPlaneIdentitiesController creates a controller that keeps
// the HostedCluster data plane identity ClientIDs in sync with the resolved
// values from ServiceProviderCluster.Status.
//
// It first seeds the three observed ClientIDs and the ownership marker in one
// Cosmos replace. Subsequent syncs advance ready operators independently.
func NewHostedClusterDataPlaneIdentitiesController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	clusterScopedIdentitiesConfig *internalazure.ClusterScopedIdentitiesConfig,
	enabled bool,
) controllerutils.Controller {
	_, readDesireLister := kubeApplierInformers.ReadDesires()

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		resourcesDBClient:             resourcesDBClient,
		readDesireLister:              readDesireLister,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
		enabled:                       enabled,
	}

	// Pass kubeApplierInformers so the controller is also re-triggered on
	// ReadDesire updates — the signal that the kube-applier first observed the
	// HostedCluster and we can safely seed the working ClientIDs.
	return controllerutils.NewClusterWatchingController(
		HostedClusterDataPlaneIdentitiesControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		30*time.Second,
		syncer,
	)
}

func (c *hostedClusterDataPlaneIdentitySyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	if !c.enabled {
		return nil
	}
	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	crud := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	for attempt := 0; attempt < 3; attempt++ {
		spc, err := crud.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("get ServiceProviderCluster: %w", err))
		}
		if spc.Status.ManagementClusterResourceID == nil {
			return nil
		}
		// A failed SPC replace retries against fresh intent and the current
		// operator-to-identity mapping. The SPC ETag does not version Cluster.
		cluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("get cluster: %w", err))
		}
		if cluster.ServiceProviderProperties.DeletionTimestamp != nil ||
			cluster.ServiceProviderProperties.ClusterServiceID == nil {
			return nil
		}
		var ids hostedClusterDataPlaneClientIDs
		if spc.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP {
			ids = clientIDsFromHostedCluster(spc.Spec.DesiredHostedCluster)
			if !ids.Complete() {
				return fmt.Errorf("ServiceProviderCluster identity ownership is set but desired HostedCluster ClientIDs are incomplete")
			}
			ids = c.resolveReadyClientIDs(ctx, cluster, spc, ids)
		} else {
			// A full, observed HostedCluster supplies safe working values. The
			// marker and IDs are persisted atomically; until then CS owns them.
			observed, readSuccessful, err := kubeapplierhelpers.GetCachedHostedClusterForClusterOnManagementCluster(
				ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
				spc.Status.ManagementClusterResourceID)
			if err != nil {
				return utils.TrackError(fmt.Errorf("read observed HostedCluster: %w", err))
			}
			if !readSuccessful || observed == nil {
				return nil
			}
			if observed.Spec.Platform.Azure == nil ||
				observed.Spec.Platform.Azure.AzureAuthenticationConfig.AzureAuthenticationConfigType !=
					v1beta1.AzureAuthenticationTypeManagedIdentities {
				return nil
			}
			ids = clientIDsFromHostedCluster(observed)
			if !ids.Complete() {
				return fmt.Errorf("observed HostedCluster has incomplete data-plane ClientIDs; will retry")
			}
		}
		next := spc.DeepCopy()
		if next.Spec.DesiredHostedCluster == nil {
			next.Spec.DesiredHostedCluster = &v1beta1.HostedCluster{}
		}
		if err := kubeapplierhelpers.SetDesiredHostedClusterDataPlaneClientIDs(next.Spec.DesiredHostedCluster, ids); err != nil {
			return err
		}
		if spc.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP &&
			clientIDsFromHostedCluster(spc.Spec.DesiredHostedCluster) == ids {
			return nil
		}
		next.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP = true
		_, err = crud.Replace(ctx, next, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			continue
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("replace ServiceProviderCluster identity intent: %w", err))
		}
		return nil
	}
	return fmt.Errorf("ServiceProviderCluster identity intent changed concurrently after three attempts; will retry")
}

// resolveReadyClientIDs advances each operator independently. A dependency
// failure leaves that operator on its observed working identity while ready
// neighbors can move forward.
func (c *hostedClusterDataPlaneIdentitySyncer) resolveReadyClientIDs(
	ctx context.Context,
	cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster,
	current hostedClusterDataPlaneClientIDs,
) hostedClusterDataPlaneClientIDs {
	result := current
	for _, slot := range dataplaneIdentitySlots {
		id, principalID, err := c.resolveAndGateOperatorClientID(cluster, spc, slot.operator)
		if err == nil {
			err = c.operatorAzureAccessReady(cluster, spc, slot.operator, id, principalID)
		}
		if err != nil {
			utils.LoggerFromContext(ctx).V(1).Info("data-plane identity cutover waiting for dependencies", "operator", slot.operator, "reason", err.Error())
			continue
		}
		slot.set(&result, id)
	}
	// Persist progress even when a different operator is waiting. The informer
	// and periodic requeue will revisit the stalled slots.
	return result
}

func (c *hostedClusterDataPlaneIdentitySyncer) resolveAndGateOperatorClientID(
	cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster,
	operator internalazure.ClusterOperatorIdentifier,
) (clientID, principalID string, err error) {
	resourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[string(operator)]
	if resourceID == nil {
		return "", "", fmt.Errorf("data plane operator %q has no identity assigned; will retry", operator)
	}
	identityKey := strings.ToLower(resourceID.String())
	metadata := spc.Status.ManagedIdentityDetails[identityKey]
	if metadata == nil || metadata.MetadataFromARMUserAssignedIdentitiesAPI == nil {
		return "", "", fmt.Errorf("ARM identity metadata for operator %q (%s) is missing; will retry", operator, identityKey)
	}
	arm := metadata.MetadataFromARMUserAssignedIdentitiesAPI
	if arm.RetrievalError != nil || arm.ClientID == nil || arm.PrincipalID == nil || arm.TenantID == nil ||
		*arm.ClientID == "" || *arm.PrincipalID == "" || *arm.TenantID == "" {
		return "", "", fmt.Errorf("ARM identity metadata for operator %q (%s) is unresolved; will retry", operator, identityKey)
	}
	assignmentKey := coreapi.DataplaneOIDCFederationAssignmentKey{
		IdentityResourceID: identityKey,
		OperatorName:       string(operator),
	}
	oidcStatus := spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[assignmentKey]
	if oidcStatus == nil || oidcStatus.TargetIdentity.ClientID != *arm.ClientID ||
		oidcStatus.TargetIdentity.PrincipalID != *arm.PrincipalID ||
		oidcStatus.TargetIdentity.TenantID != *arm.TenantID ||
		!coreapihelpers.IsDataplaneOIDCFederationAssignmentTargetIdentityEnsured(oidcStatus) {
		return "", "", fmt.Errorf("OIDC federation for operator %q (%s) is not ready for the current identity instance; will retry", operator, identityKey)
	}
	return *arm.ClientID, *arm.PrincipalID, nil
}

func (c *hostedClusterDataPlaneIdentitySyncer) operatorAzureAccessReady(
	cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster,
	operator internalazure.ClusterOperatorIdentifier, clientID, principalID string,
) error {
	if c.clusterScopedIdentitiesConfig == nil {
		return fmt.Errorf("cluster-scoped identity configuration is missing")
	}
	resourceID := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators[string(operator)]
	identityKey := strings.ToLower(resourceID.String())
	confirmedIdentity := spc.Status.DataPlaneOperatorsManagedIdentities.Identities[identityKey]
	if confirmedIdentity == nil || confirmedIdentity.RetrievalError != nil ||
		confirmedIdentity.ClientID == nil || *confirmedIdentity.ClientID != clientID ||
		confirmedIdentity.PrincipalID == nil || *confirmedIdentity.PrincipalID != principalID {
		return fmt.Errorf("data-plane identity principal for operator %q is not confirmed; will retry", operator)
	}
	opConfig := c.clusterScopedIdentitiesConfig.DataPlaneOperatorsIdentities[operator]
	if opConfig == nil || len(opConfig.KubernetesServiceAccounts) == 0 {
		return fmt.Errorf("data-plane identity configuration for operator %q is missing", operator)
	}
	for _, account := range opConfig.KubernetesServiceAccounts {
		if account == nil || account.Name == "" || account.Namespace == "" {
			return fmt.Errorf("data-plane operator %q has an invalid Kubernetes service account configuration", operator)
		}
	}
	scope, err := coreapihelpers.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
	if err != nil {
		return fmt.Errorf("managed resource group for operator %q: %w", operator, err)
	}
	confirmedRoles := make(map[string]struct{}, len(spc.Status.AzureResources.RoleAssignments.AzureResources))
	for _, id := range spc.Status.AzureResources.RoleAssignments.AzureResources {
		if id != nil {
			confirmedRoles[strings.ToLower(id.String())] = struct{}{}
		}
	}
	roles := opConfig.RoleDefinitionsResourceIDs()
	if len(roles) == 0 {
		return fmt.Errorf("data-plane operator %q has no configured role definitions", operator)
	}
	for _, role := range roles {
		expected := roleassignment.ManagedResourceGroupScopedRoleAssignmentResourceID(scope.String(), principalID, role.String())
		if _, ok := confirmedRoles[strings.ToLower(expected)]; !ok {
			return fmt.Errorf("role assignment for operator %q and principal %s is not confirmed; will retry", operator, principalID)
		}
	}
	denyTypes := denyassignments.DataPlaneOperatorDenyAssignmentTypes(cluster, string(operator))
	if len(denyTypes) == 0 {
		return fmt.Errorf("data-plane operator %q has no deny-assignment exclusions", operator)
	}
	for _, assignmentType := range denyTypes {
		confirmed := false
		for _, ref := range spc.Status.AzureResources.DenyAssignments.AzureResources {
			if ref.DenyAssignmentType == assignmentType && ref.DenyAssignmentResourceID != nil {
				for _, excluded := range ref.ExcludedPrincipalIDs {
					if strings.EqualFold(excluded, principalID) {
						confirmed = true
						break
					}
				}
			}
		}
		if !confirmed {
			return fmt.Errorf("deny assignment %q does not yet exclude operator %q principal %s; will retry", assignmentType, operator, principalID)
		}
	}
	return nil
}
