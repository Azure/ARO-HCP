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
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	applyDesireLister            kubeapplierlisters.ApplyDesireLister
	readDesireLister             kubeapplierlisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*hostedClusterDataPlaneIdentitySyncer)(nil)

// NewHostedClusterDataPlaneIdentitiesController creates a controller that keeps
// the HostedCluster data plane identity ClientIDs in sync with the resolved
// values from ServiceProviderCluster.Status.
//
// On each sync it:
//  1. Skips clusters not yet placed on a management cluster.
//  2. On cluster deletion: drops the identity ApplyDesire document from Cosmos.
//     We do not flip it to Type=Delete — we do not want to unset the identity
//     fields, HyperShift reads them during teardown.
//  3. Resolves ClientIDs from SPC.Status.ManagedIdentityDetails
//     (MetadataFromARMUserAssignedIdentitiesAPI), returning a transient error
//     if any is missing, nil, or has a RetrievalError so the workqueue retries.
//  4. Gates on OIDC federation completion for each operator via
//     SPC.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation:
//     the HostedCluster is only updated once every operator's FIC is ensured
//     for the current target identity, preventing Azure auth failures.
//  5. Reads the HC name and namespace from the ReadDesire-cached HostedCluster;
//     returns nil (not an error) if the cache is not yet populated, relying on
//     the ReadDesire informer re-trigger when the HC is first observed.
//  6. Writes a typed, minimal HostedCluster SSA patch containing only the
//     three dataPlane ClientID fields under field manager "aro-hcp-mi-controller".
func NewHostedClusterDataPlaneIdentitiesController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()

	syncer := &hostedClusterDataPlaneIdentitySyncer{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		kubeApplierDBClients:         kubeApplierDBClients,
		applyDesireLister:            applyDesireLister,
		readDesireLister:             readDesireLister,
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

	clientIDs, err := c.resolveAndGateClientIDs(cluster, spc)
	if err != nil {
		// Transient: identities not yet resolved or OIDC federation not yet
		// complete. The SPC informer re-triggers when those fields are updated.
		return err
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

// resolveAndGateClientIDs resolves the ClientID for each data plane operator
// from SPC.Status.ManagedIdentityDetails and gates on OIDC federation
// completion via SPC.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation.
//
// It returns a populated hostedClusterDataPlaneClientIDs on success, or a
// transient error when any gate is not satisfied. Transient errors are not
// wrapped with TrackError so they do not pollute error tracking dashboards;
// they are expected steady-state during provisioning.
func (c *hostedClusterDataPlaneIdentitySyncer) resolveAndGateClientIDs(
	cluster *coreapi.HCPOpenShiftCluster,
	spc *coreapi.ServiceProviderCluster,
) (hostedClusterDataPlaneClientIDs, error) {
	dpOperators := cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
	identityDetails := spc.Status.ManagedIdentityDetails
	oidcFederation := spc.Status.ManagedIdentitiesWithDataPlaneWorkloadsOIDCFederation

	var clientIDs hostedClusterDataPlaneClientIDs
	for _, slot := range dataplaneIdentitySlots {
		resourceID, ok := dpOperators[string(slot.operator)]
		if !ok || resourceID == nil {
			return hostedClusterDataPlaneClientIDs{}, fmt.Errorf(
				"data plane operator %q has no identity assigned in CustomerProperties; will retry",
				slot.operator,
			)
		}

		identityKey := strings.ToLower(resourceID.String())

		// Gate 1: ClientID must be resolved from the ARM API.
		metadata, ok := identityDetails[identityKey]
		if !ok || metadata == nil {
			return hostedClusterDataPlaneClientIDs{}, fmt.Errorf(
				"ManagedIdentityDetails entry for operator %q (identity %s) not yet populated; will retry",
				slot.operator, identityKey,
			)
		}
		armMetadata := metadata.MetadataFromARMUserAssignedIdentitiesAPI
		if armMetadata == nil {
			return hostedClusterDataPlaneClientIDs{}, fmt.Errorf(
				"ARM metadata for operator %q (identity %s) not yet populated; will retry",
				slot.operator, identityKey,
			)
		}
		if armMetadata.RetrievalError != nil {
			return hostedClusterDataPlaneClientIDs{}, fmt.Errorf(
				"ARM metadata retrieval failed for operator %q (identity %s): %s; will retry",
				slot.operator, identityKey, *armMetadata.RetrievalError,
			)
		}
		if armMetadata.ClientID == nil {
			return hostedClusterDataPlaneClientIDs{}, fmt.Errorf(
				"ClientID for operator %q (identity %s) is nil; will retry",
				slot.operator, identityKey,
			)
		}

		// Gate 2: the ARM-resolved ClientID must match the ClientID that OIDC
		// federation is targeting (TargetIdentity.ClientID). The two sources
		// are written by independent controllers and can transiently diverge:
		// FetchManagedIdentitiesInfo can refresh ARM to a new ClientID before
		// DataPlaneOIDCFederationIntent has run to update TargetIdentity. If we
		// wrote the new ARM ClientID in that window, the HostedCluster would
		// reference an identity for which no FIC has been created yet, causing
		// Azure token exchange failures. We wait until both sources agree.
		oidcStatus := oidcFederation[identityKey]
		var oidcTargetClientID string
		if oidcStatus != nil {
			oidcTargetClientID = oidcStatus.TargetIdentity.ClientID
		}
		if *armMetadata.ClientID != oidcTargetClientID {
			return hostedClusterDataPlaneClientIDs{}, fmt.Errorf(
				"ARM ClientID %q for operator %q does not yet match OIDC TargetIdentity ClientID %q; will retry",
				*armMetadata.ClientID, slot.operator, oidcTargetClientID,
			)
		}

		// Gate 3: OIDC federation must be complete for this operator on the
		// current TargetIdentity. At this point we know ARM and OIDC agree on
		// the ClientID; OperatorEnsured confirms the FIC exists for it.
		if !oidcStatus.OperatorEnsured(string(slot.operator)) {
			return hostedClusterDataPlaneClientIDs{}, fmt.Errorf(
				"OIDC federation not yet complete for operator %q (identity %s); will retry",
				slot.operator, identityKey,
			)
		}

		slot.set(&clientIDs, *armMetadata.ClientID)
	}
	return clientIDs, nil
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
