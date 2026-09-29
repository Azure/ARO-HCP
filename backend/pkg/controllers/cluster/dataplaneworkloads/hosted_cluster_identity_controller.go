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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/roleassignment"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/denyassignments"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// HostedClusterDataPlaneIdentitiesControllerName is the canonical name used
	// as the workqueue name (Prometheus label), context controller name, and
	// TagControllerName tag on the ApplyDesire document.
	HostedClusterDataPlaneIdentitiesControllerName = "HostedClusterDataPlaneIdentities"

	// DesireNameHostedClusterDataPlaneIdentities is the well-known ApplyDesire
	// document name for the HostedCluster data plane identity SSA patch.
	DesireNameHostedClusterDataPlaneIdentities = "HostedClusterDataPlaneIdentities"

	// fieldManagerDataPlaneIdentities is the SSA field manager name used when
	// applying HostedCluster data plane identity fields. It is intentionally
	// distinct from the base HostedCluster desire field manager (currently
	// "work-agent" during the ACM/Maestro migration; "aro-hcp-kube-applier"
	// once ARO-27507 completes) so that Kubernetes SSA field ownership is
	// partitioned: non-identity fields trace to the base manager; the three
	// data plane ClientID fields trace to this one.
	// Inspect ownership with:
	//   kubectl get hc -n <ns> <name> -o json | jq '.metadata.managedFields'
	fieldManagerDataPlaneIdentities = "aro-hcp-mi-controller"

	// HyperShift HostedCluster GVR — stable under the v1beta1 API contract.
	hostedClusterAPIVersion = "hypershift.openshift.io/v1beta1"
	hostedClusterKind       = "HostedCluster"
	hostedClusterGroup      = "hypershift.openshift.io"
	hostedClusterVersion    = "v1beta1"
	hostedClusterResource   = "hostedclusters"
)

// hostedClusterDataPlanePatch is a minimal HostedCluster representation for
// SSA. Only the fields this controller owns are declared; fields absent from
// the struct are not included in the marshaled payload and therefore never
// claimed by this field manager. This avoids zero-initializing required fields
// from v1beta1.HostedCluster (Location, VnetID, etc.) that would inadvertently
// steal SSA field ownership from the base HostedCluster desire.
//
// JSON tags match the v1beta1.HostedCluster API exactly. The struct hierarchy
// follows spec.platform.azure.azureAuthenticationConfig.managedIdentities.dataPlane.
type hostedClusterDataPlanePatch struct {
	APIVersion string                     `json:"apiVersion"`
	Kind       string                     `json:"kind"`
	Metadata   hostedClusterPatchMetadata `json:"metadata"`
	Spec       hostedClusterDataPlaneSpec `json:"spec"`
}

type hostedClusterPatchMetadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type hostedClusterDataPlaneSpec struct {
	Platform hostedClusterDataPlanePlatform `json:"platform"`
}

type hostedClusterDataPlanePlatform struct {
	Azure hostedClusterDataPlaneAzure `json:"azure"`
}

type hostedClusterDataPlaneAzure struct {
	AzureAuthenticationConfig hostedClusterDataPlaneAuthConfig `json:"azureAuthenticationConfig"`
}

// hostedClusterDataPlaneAuthConfig includes the union discriminator so that
// HyperShift's CEL validation accepts the partial object. Including the
// discriminator does not claim the other union branch (workloadIdentities).
type hostedClusterDataPlaneAuthConfig struct {
	AzureAuthenticationConfigType string                         `json:"azureAuthenticationConfigType"`
	ManagedIdentities             hostedClusterDataPlaneMIConfig `json:"managedIdentities"`
}

type hostedClusterDataPlaneMIConfig struct {
	DataPlane hostedClusterDataPlaneClientIDs `json:"dataPlane"`
}

// hostedClusterDataPlaneClientIDs holds the three ClientID fields that this
// controller owns on the HostedCluster. omitempty ensures unset fields are
// excluded from the SSA payload and never claimed.
type hostedClusterDataPlaneClientIDs struct {
	ImageRegistryMSIClientID string `json:"imageRegistryMSIClientID,omitempty"`
	DiskMSIClientID          string `json:"diskMSIClientID,omitempty"`
	FileMSIClientID          string `json:"fileMSIClientID,omitempty"`
}

func (ids hostedClusterDataPlaneClientIDs) complete() bool {
	return ids.ImageRegistryMSIClientID != "" && ids.DiskMSIClientID != "" && ids.FileMSIClientID != ""
}

func clientIDsFromHostedCluster(hc *v1beta1.HostedCluster) hostedClusterDataPlaneClientIDs {
	mi := hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities
	if mi == nil {
		return hostedClusterDataPlaneClientIDs{}
	}
	return hostedClusterDataPlaneClientIDs{
		ImageRegistryMSIClientID: string(mi.DataPlane.ImageRegistryMSIClientID),
		DiskMSIClientID:          string(mi.DataPlane.DiskMSIClientID),
		FileMSIClientID:          string(mi.DataPlane.FileMSIClientID),
	}
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

// hostedClusterDataPlaneIdentitySyncer writes a targeted SSA patch for the
// three data plane managed identity ClientIDs on the cluster's HostedCluster
// object. ClientIDs are read from
// ServiceProviderCluster.Status.ManagedIdentityDetails
// (MetadataFromARMUserAssignedIdentitiesAPI), populated by
// FetchManagedIdentitiesInfo. OIDC federation completion is checked via
// ServiceProviderCluster.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation
// (populated by DataPlaneOIDCFederation), so that a ClientID is never written
// to the HostedCluster before the corresponding FIC exists on Azure.
type hostedClusterDataPlaneIdentitySyncer struct {
	clusterLister                 corelisters.ClusterLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	kubeApplierDBClients          kubeappliercosmosstorage.KubeApplierDBClients
	applyDesireLister             kubeapplierlisters.ApplyDesireLister
	readDesireLister              kubeapplierlisters.ReadDesireLister
	clusterScopedIdentitiesConfig *internalazure.ClusterScopedIdentitiesConfig
	enabled                       bool
}

var _ controllerutils.ClusterSyncer = (*hostedClusterDataPlaneIdentitySyncer)(nil)

// NewHostedClusterDataPlaneIdentitiesController creates a controller that keeps
// the HostedCluster data plane identity ClientIDs in sync with the resolved
// values from ServiceProviderCluster.Status.
//
// On cluster deletion it removes its ApplyDesire without clearing the live
// fields that HyperShift needs for teardown. On live clusters it is inactive
// until enabled after the Cluster Service/Maestro HostedCluster writer is
// retired. It first claims the observed ClientIDs with its own SSA field
// manager, waits for the base desire to release and successfully omit those
// fields, then advances each operator after its own Azure dependencies are
// confirmed. Missing or stalled operators retain their previous ClientID.
func NewHostedClusterDataPlaneIdentitiesController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	clusterScopedIdentitiesConfig *internalazure.ClusterScopedIdentitiesConfig,
	enabled bool,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                 clusterLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		kubeApplierDBClients:          kubeApplierDBClients,
		applyDesireLister:             applyDesireLister,
		readDesireLister:              readDesireLister,
		clusterScopedIdentitiesConfig: clusterScopedIdentitiesConfig,
		enabled:                       enabled,
	}

	// Pass kubeApplierInformers so the controller is also re-triggered on
	// ReadDesire updates — the signal that the kube-applier first observed the
	// HostedCluster, giving us the name and namespace for the SSA target.
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
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}

	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	managementCluster := spc.Status.ManagementClusterResourceID
	if managementCluster == nil {
		return nil // not yet placed
	}

	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return c.removeIdentityDesire(ctx, key, managementCluster)
	}

	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil // not yet provisioned in Cluster Service
	}
	if !c.enabled {
		return nil
	}

	// The HC name and namespace on the management cluster come from the
	// ReadDesire cache populated by the kube-applier. If the HC has not yet
	// been applied (base desire pending), we skip silently — the ReadDesire
	// informer re-triggers us when the HC is first observed.
	cachedHC, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(
		ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to read cached HostedCluster: %w", err))
	}
	if cachedHC == nil {
		return nil
	}

	// Only ManagedIdentities clusters carry data plane MSI ClientIDs. The SSA
	// patch sets the azureAuthenticationConfigType discriminator, so applying
	// it to a WorkloadIdentities cluster would conflict with the base desire.
	if cachedHC.Spec.Platform.Azure == nil ||
		cachedHC.Spec.Platform.Azure.AzureAuthenticationConfig.AzureAuthenticationConfigType !=
			v1beta1.AzureAuthenticationTypeManagedIdentities {
		return nil
	}
	currentIDs := clientIDsFromHostedCluster(cachedHC)
	if !currentIDs.complete() {
		return fmt.Errorf("observed HostedCluster has incomplete data-plane ClientIDs; will retry")
	}

	// Claim the live values first. Base desire may omit dataPlane only after
	// this desire has been applied. Do not change values until the new base
	// desire is also confirmed applied, or its final SSA omission could remove
	// the fields before this controller owns them.
	baseDesire, err := c.applyDesireLister.GetForCluster(ctx,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		kubeapplierhelpers.HostedClusterBaseDesireName)
	if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("read base HostedCluster desire: %w", err))
	}
	baseOmitsDataPlane, err := kubeapplierhelpers.HostedClusterBaseDesireOmitsDataPlane(baseDesire)
	if err != nil {
		return utils.TrackError(err)
	}
	clientIDs := currentIDs
	if baseOmitsDataPlane && baseDesire.Spec.TargetItem.Name == cachedHC.Name &&
		baseDesire.Spec.TargetItem.Namespace == cachedHC.Namespace &&
		baseDesire.Spec.ManagementCluster != nil &&
		strings.EqualFold(baseDesire.Spec.ManagementCluster.String(), managementCluster.String()) &&
		kubeapplierhelpers.ApplyDesireSuccessfullyApplied(baseDesire) {
		previous, getErr := c.applyDesireLister.GetForCluster(ctx,
			key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
			kubeapplierhelpers.HostedClusterDataPlaneIdentityDesireName)
		if getErr != nil && !cosmosstorageutils.IsNotFoundError(getErr) {
			return utils.TrackError(fmt.Errorf("read previous identity desire: %w", getErr))
		}
		fallback, valid, parseErr := clientIDsFromIdentityDesire(previous, managementCluster, cachedHC.Name, cachedHC.Namespace)
		if parseErr != nil {
			return utils.TrackError(parseErr)
		}
		if valid {
			clientIDs = fallback
			if kubeapplierhelpers.ApplyDesireSuccessfullyApplied(previous) {
				clientIDs = c.resolveReadyClientIDs(ctx, cluster, spc, fallback)
			}
		}
	}

	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}
	applyDesireCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ApplyDesire CRUD: %w", err))
	}

	desire, err := buildDataPlaneIdentityDesire(
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		managementCluster,
		cachedHC.Name, cachedHC.Namespace,
		clientIDs,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build identity desire: %w", err))
	}

	return kubeapplierhelpers.EnsureApplyDesire(ctx, applyDesireCRUD, c.applyDesireLister, desire)
}

func clientIDsFromIdentityDesire(desire *kubeapplierapi.ApplyDesire, managementCluster *azcorearm.ResourceID, hcName, hcNamespace string) (hostedClusterDataPlaneClientIDs, bool, error) {
	if desire == nil || desire.Spec.ServerSideApply == nil || desire.Spec.ServerSideApply.KubeContent == nil ||
		desire.Spec.ServerSideApply.FieldManager == nil ||
		*desire.Spec.ServerSideApply.FieldManager != fieldManagerDataPlaneIdentities ||
		desire.Spec.ManagementCluster == nil ||
		!strings.EqualFold(desire.Spec.ManagementCluster.String(), managementCluster.String()) ||
		desire.Spec.TargetItem.Name != hcName || desire.Spec.TargetItem.Namespace != hcNamespace {
		return hostedClusterDataPlaneClientIDs{}, false, nil
	}
	var patch hostedClusterDataPlanePatch
	if err := json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &patch); err != nil {
		return hostedClusterDataPlaneClientIDs{}, false, fmt.Errorf("decode previous HostedCluster identity desire: %w", err)
	}
	ids := patch.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane
	return ids, ids.complete(), nil
}

// resolveReadyClientIDs advances each operator independently. A dependency
// failure leaves that operator on its observed working identity while ready
// neighbors can move forward.
func (c *hostedClusterDataPlaneIdentitySyncer) resolveReadyClientIDs(
	ctx context.Context,
	cluster *coreapi.HCPOpenShiftCluster, spc *coreapi.ServiceProviderCluster,
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
	cluster *coreapi.HCPOpenShiftCluster, spc *coreapi.ServiceProviderCluster,
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
	oidcStatus := spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation[identityKey]
	if oidcStatus == nil || oidcStatus.TargetIdentity.ClientID != *arm.ClientID ||
		oidcStatus.TargetIdentity.PrincipalID != *arm.PrincipalID ||
		oidcStatus.TargetIdentity.TenantID != *arm.TenantID ||
		!oidcStatus.OperatorEnsured(string(operator)) {
		return "", "", fmt.Errorf("OIDC federation for operator %q (%s) is not ready for the current identity instance; will retry", operator, identityKey)
	}
	return *arm.ClientID, *arm.PrincipalID, nil
}

func (c *hostedClusterDataPlaneIdentitySyncer) operatorAzureAccessReady(
	cluster *coreapi.HCPOpenShiftCluster, spc *coreapi.ServiceProviderCluster,
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
	scope, err := coreapi.ToResourceGroupResourceID(cluster.ID.SubscriptionID, cluster.CustomerProperties.Platform.ManagedResourceGroup)
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

// removeIdentityDesire removes the identity ApplyDesire document from Cosmos.
// We delete the document directly (do not flip to Type=Delete) because we want
// the existing ClientID values to remain on the live HostedCluster during
// teardown — HyperShift reads them to de-provision Azure resources.
func (c *hostedClusterDataPlaneIdentitySyncer) removeIdentityDesire(
	ctx context.Context,
	key controllerutils.HCPClusterKey,
	managementCluster *azcorearm.ResourceID,
) error {
	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementCluster)
	if kubeApplierDBClient == nil {
		return nil
	}
	applyDesireCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ApplyDesire CRUD for cleanup: %w", err))
	}
	desireName := strings.ToLower(DesireNameHostedClusterDataPlaneIdentities)
	if err := applyDesireCRUD.Delete(ctx, desireName); err != nil && !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to delete identity desire: %w", err))
	}
	return nil
}

// buildDataPlaneIdentityDesire constructs the ApplyDesire that SSA-patches the
// three data plane ClientID fields on the HostedCluster. The patch is a typed
// minimal struct (hostedClusterDataPlanePatch) that only declares the fields
// this controller owns; all other HostedCluster fields are owned by
// ClusterResourcesController under the base desire. Including
// azureAuthenticationConfigType satisfies HyperShift's union discriminator
// validation without claiming the other union branch (workloadIdentities).
func buildDataPlaneIdentityDesire(
	subscriptionID, resourceGroupName, clusterName string,
	managementCluster *azcorearm.ResourceID,
	hcName, hcNamespace string,
	clientIDs hostedClusterDataPlaneClientIDs,
) (*kubeapplierapi.ApplyDesire, error) {
	// Guard against an empty clientIDs struct. If all three fields are empty
	// strings, omitempty would exclude them from the payload while still
	// declaring "managedIdentities": {"dataPlane": {}} in the SSA patch, which
	// would cause SSA to own — and effectively clear — the dataPlane sub-object.
	// resolveAndGateClientIDs always populates all three before calling here,
	// but this check makes the invariant explicit.
	if clientIDs.ImageRegistryMSIClientID == "" ||
		clientIDs.DiskMSIClientID == "" ||
		clientIDs.FileMSIClientID == "" {
		return nil, utils.TrackError(fmt.Errorf(
			"all three data plane ClientIDs must be set before building the identity desire",
		))
	}

	patch := hostedClusterDataPlanePatch{
		APIVersion: hostedClusterAPIVersion,
		Kind:       hostedClusterKind,
		Metadata:   hostedClusterPatchMetadata{Name: hcName, Namespace: hcNamespace},
		Spec: hostedClusterDataPlaneSpec{
			Platform: hostedClusterDataPlanePlatform{
				Azure: hostedClusterDataPlaneAzure{
					AzureAuthenticationConfig: hostedClusterDataPlaneAuthConfig{
						AzureAuthenticationConfigType: string(v1beta1.AzureAuthenticationTypeManagedIdentities),
						ManagedIdentities: hostedClusterDataPlaneMIConfig{
							DataPlane: clientIDs,
						},
					},
				},
			},
		},
	}

	rawJSON, err := json.Marshal(patch)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal identity patch: %w", err))
	}

	resourceIDStr := kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, DesireNameHostedClusterDataPlaneIdentities,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse desire resource ID %q: %w", resourceIDStr, err))
	}

	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem: kubeapplierapi.ResourceReference{
				Group:     hostedClusterGroup,
				Version:   hostedClusterVersion,
				Resource:  hostedClusterResource,
				Name:      hcName,
				Namespace: hcNamespace,
			},
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent:  &k8sruntime.RawExtension{Raw: rawJSON},
				FieldManager: ptr.To(fieldManagerDataPlaneIdentities),
			},
		},
		Tags: map[string]string{
			kubeapplierapi.TagControllerName: HostedClusterDataPlaneIdentitiesControllerName,
		},
	}, nil
}
