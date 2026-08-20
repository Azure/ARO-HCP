// Copyright 2025 Microsoft Corporation
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

package coreapi

import (
	"fmt"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// BackupScheduleState represents the desired state of backup scheduling for a cluster.
// Maps to Velero Schedule spec.paused: Enabled → paused: false, Disabled → paused: true.
type BackupScheduleState string

const (
	BackupScheduleStateEnabled  BackupScheduleState = "Enabled"
	BackupScheduleStateDisabled BackupScheduleState = "Disabled"
)

const (
	// ServiceProviderClusterResourceName is the name of the ServiceProviderCluster resource.
	// ServiceProviderCluster is a singleton resource and ARM convention is to
	// use the name "default" for singleton resources.
	ServiceProviderClusterResourceName = "default"
)

// HostedClusterControlPlaneSize enumerates the SRE-selectable control-plane
// sizing tiers we expose for a HostedCluster. Stored as a *string on
// ServiceProviderClusterSpec so unset is distinguishable from "explicitly chosen."
type HostedClusterControlPlaneSize string

const (
	HostedClusterControlPlaneSizeSmall   HostedClusterControlPlaneSize = "Small"
	HostedClusterControlPlaneSizeMedium  HostedClusterControlPlaneSize = "Medium"
	HostedClusterControlPlaneSizeLarge   HostedClusterControlPlaneSize = "Large"
	HostedClusterControlPlaneSizeXlarge  HostedClusterControlPlaneSize = "Xlarge"
	HostedClusterControlPlaneSizeXXlarge HostedClusterControlPlaneSize = "XXlarge"
)

// IsValidHostedClusterControlPlaneSize reports whether s names a known tier.
func IsValidHostedClusterControlPlaneSize(s string) bool {
	switch HostedClusterControlPlaneSize(s) {
	case HostedClusterControlPlaneSizeSmall,
		HostedClusterControlPlaneSizeMedium,
		HostedClusterControlPlaneSizeLarge,
		HostedClusterControlPlaneSizeXlarge,
		HostedClusterControlPlaneSizeXXlarge:
		return true
	}
	return false
}

// ServiceProviderCluster is used internally by controllers to track and pass information between them.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ServiceProviderCluster struct {
	// CosmosMetadata ResourceID is nested under the cluster so that association and cleanup work as expected
	// it will be the ServiceProviderCluster type and the name default.
	// PartitionKey holds the lowercased subscriptionID.
	CosmosMetadata `json:"cosmosMetadata"`

	LoadBalancerResourceID *azcorearm.ResourceID `json:"loadBalancerResourceID,omitempty"`

	Spec ServiceProviderClusterSpec `json:"spec"`

	// Status contains the observed state of the cluster.
	Status ServiceProviderClusterStatus `json:"status,omitempty"`
}

// ServiceProviderClusterSpec contains the desired state of the cluster.
type ServiceProviderClusterSpec struct {
	// ControlPlaneVersion contains the desired control plane version information.
	// Example JSON structure:
	// {
	//   "control_plane_version": {
	//     "desired_version": "4.19.2"
	//   }
	// }
	ControlPlaneVersion ServiceProviderClusterSpecVersion `json:"control_plane_version,omitempty"`

	// DesiredHostedCluster is the HostedCluster that we want to exist on the management cluster.
	// We will only explicitly set the fields we care about, but serialization may store additional empty fields.
	// Once this contains the critical values, we will create it on management clusters.
	// We may or may not choose to store the actual state in status.  We may choose to store the actual state independently.
	DesiredHostedCluster *v1beta1.HostedCluster `json:"desiredHostedCluster,omitempty"`

	// DesiredHostedClusterControlPlaneSize is the SRE-selected control plane
	// sizing tier (one of "Small", "Medium", "Large", "Xlarge", "XXlarge") to
	// apply to the HostedCluster. Stored as *string so unset is distinguishable
	// from any explicit choice; nil means no tier has been requested. Valid
	// values are the HostedClusterControlPlaneSize* constants above.
	DesiredHostedClusterControlPlaneSize *string `json:"desiredHostedClusterControlPlaneSize,omitempty"`

	// BackupScheduleState is the desired backup scheduling state: Enabled or Disabled.
	// Default is Enabled. Set to Disabled via Admin API to pause scheduled backups.
	BackupScheduleState BackupScheduleState `json:"backupScheduleState,omitempty"`
}

// ServiceProviderClusterSpecVersion contains the desired version information.
type ServiceProviderClusterSpecVersion struct {
	// DesiredVersion is the full version the controller has resolved and wants to upgrade to (format: x.y.z)
	// This is compared on each sync to detect when a new upgrade should be triggered.
	DesiredVersion *semver.Version `json:"desired_version,omitempty"`
}

// ServiceProviderClusterStatus contains the observed state of the cluster.
type ServiceProviderClusterStatus struct {
	// Conditions are the top-level ServiceProviderCluster status conditions.
	// Each Condition Type represents a condition and it should be unique among all conditions.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// Known condition types are:
	// - "Progressing": True when the cluster is in the process of being created, updated, or deleted. Both end-user initiated actions as
	//   well as internal actions such as upgrades or other maintenance operations are in progress are included in thsi condition.
	// - "Degraded": True when the cluster is in a degraded state
	// Addition of new conditions here should be done only when strictly necessary, sparingly and only done
	// when there is a clear benefit to doing so. We expect the number of conditions at this
	// level to be kept to a minimum. Take into consideration that conditions at other levels can be specified within
	// ServiceProviderClusterStatus too.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// ControlPlaneVersion contains the actual control plane version information.
	// ActiveVersions contains all versions currently active in the control plane.
	//
	// During an upgrade, multiple versions can be active simultaneously. For example:
	// - Simple upgrade: [vNew, vOld]
	// - Sequential upgrades before completion: [vNewest, vNewer, vNew, vOld]
	//
	// The list is ordered with the most recent version first.
	//
	// Example JSON structure (state is "Completed" or "Partial"):
	// {
	//   "control_plane_version": {
	//     "active_versions": [
	//       {"version": "4.19.2", "state": "Partial"},
	//       {"version": "4.19.1", "state": "Completed"}
	//     ]
	//   }
	// }
	ControlPlaneVersion ServiceProviderClusterStatusVersion `json:"control_plane_version,omitempty"`

	// DesiredVersionChannels mirrors the observed HostedCluster's
	// status.version.desired.channels (see hypershift ClusterVersionStatus ->
	// configv1.Release.Channels). Each entry is an OpenShift update channel name
	// such as "stable-4.19" or "candidate-4.20".
	//
	// A channel only appears in status.version.desired.channels when the
	// cluster's current desired release has a valid upgrade edge to a release
	// served by that channel. In other words, this is the set of channels the
	// cluster can currently move along; a channel being absent means there is no
	// supported upgrade path to that channel's release line yet.
	//
	// This list is mirrored here by the backend (which alone can observe the
	// HostedCluster) so that DB-free cluster admission can reject a version.id
	// change whose target channel ("<channelGroup>-<id>") is not reachable,
	// without the frontend ever needing access to the management cluster.
	// Written by: ControlPlaneActiveVersions
	DesiredVersionChannels []string `json:"desiredVersionChannels,omitempty"`

	// Validations is a list of conditions that tracks the status of each cluster validation.
	// Each Condition Type represents a validation and it should be unique among all validations.
	// A Condition Status of True means that the validation passed successfully, and a Condition Status of False means that the validation failed.
	// The Condition Reason and Message are used to provide more details about the validation status.
	// The Condition LastTransitionTime is used to track the last time the validation transitioned from one status to another.
	Validations []metav1.Condition `json:"validations,omitempty"`
	// MaestroReadonlyBundles contains a list of Maestro readonly bundles references.
	// These bundles are used to retrieve particular K8s resources from the Management Cluster.
	// The reference contains a mapping between the logical name we give to the Maestro bundle internally
	// and the Maestro Bundle Name and ID at the Maestro API level.
	MaestroReadonlyBundles MaestroBundleReferenceList `json:"maestroReadonlyBundles,omitempty"`
	// ManagementClusterResourceID is the resource ID of the management cluster
	// this HCP is placed on. Nil means placement has not been resolved yet.
	// Once set, this field is immutable.
	ManagementClusterResourceID *azcorearm.ResourceID `json:"managementClusterResourceID,omitempty"`

	// DesiredHostedClusterControlPlaneSize mirrors the value of
	// Spec.DesiredHostedClusterControlPlaneSize once cluster-service reflects
	// the effective size override (as confirmed by the desired-control-plane-size
	// status reconciler). It exists so NeedsWork can cheaply detect divergence,
	// including the unset transition (Spec nil, Status non-nil), where there
	// is no signal on Spec alone that dispatch still needs to clear the CS
	// property.
	DesiredHostedClusterControlPlaneSize *string `json:"desiredHostedClusterControlPlaneSize,omitempty"`

	// HostedClusterNamespace is the namespace of the actual HostedCluster.  It contains things like
	//  - HostedCluster CR — the primary user-facing API object
	//  - NodePool CRs — user creates these here
	//  - User-provided secrets — pull secret, SSH key, cloud credentials, encryption secrets, etcd encryption key, audit webhook config, additional trust bundles, service account signing key
	//  - Generated kubeconfig secrets — admin-kubeconfig (copied back from HCP namespace), kubeadmin-password
	//  - EtcdBackup CRs (if used)
	// Written by: ServiceProviderClusterPropertiesSync
	HostedClusterNamespace string `json:"hostedClusterNamespace,omitempty"`

	// ControlPlaneNamespace is the namespace containing the pods that manage and run the HostedCluster.  It contains things like
	//  - etcd
	//  - kube-apiserver
	//  - control-plane-operator
	//  - control-plane-pki-operator
	// it is derived as <hostedClusterNamespace>-<hostedClusterName> with dots replaced by dashes.
	// Hypershift uses the HostedControlPlaneNamespace function.
	// Written by: ServiceProviderClusterPropertiesSync
	ControlPlaneNamespace string `json:"controlPlaneNamespace,omitempty"`

	// ServingCABundle is the PEM-encoded serving CA bundle for the cluster's
	// kube-apiserver. Populated from a ReadDesire mirror of the management
	// cluster's serving CA Secret.
	// Written by: ServiceProviderClusterPropertiesSync
	ServingCABundle string `json:"servingCABundle,omitempty"`

	// AzureResources tracks the lifecycle of Azure resources associated with
	// the cluster, including deny assignments and the managed resource group.
	AzureResources AzureResources `json:"azureResources,omitempty"`

	// DataPlaneOperatorsManagedIdentities tracks resolved ClientID/PrincipalID for
	// the Azure User Assigned Managed Identities associated to the cluster's data
	// plane operators, plus when Azure should next be re-queried for that info.
	// A cluster's data plane operator is a kubernetes operator associated to the
	// cluster that runs in the cluster's data plane.
	// For example, the Cluster's CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators map
	// contains the set of required data plane operators associated to a Cluster.
	// Written by: FetchDataPlaneOperatorsManagedIdentitiesInfoController
	DataPlaneOperatorsManagedIdentities ServiceProviderClusterDataPlaneOperatorsManagedIdentities `json:"dataPlaneOperatorsManagedIdentities,omitempty"`
}

// ServiceProviderClusterDataPlaneOperatorsManagedIdentities holds the resolved
// managed-identity metadata for all data plane operators on a cluster, together
// with a single EarliestRecheckTime that applies to every entry in Identities.
type ServiceProviderClusterDataPlaneOperatorsManagedIdentities struct {
	// Identities is a map containing resolved ClientID/PrincipalID for the Azure
	// User Assigned Managed Identities associated to the cluster's data plane
	// operators. The key is the fully lowercased Azure Resource ID of the
	// identity. Which operators reference each identity is tracked on
	// Cluster.CustomerProperties, not here. Multiple operators may share one
	// identity entry.
	// Written by: FetchDataPlaneOperatorsManagedIdentitiesInfoController
	Identities map[string]*ServiceProviderClusterDataPlaneOperatorManagedIdentity `json:"identities,omitempty"`
	// EarliestRecheckTime is the earliest time at which the controller should
	// re-query Azure for ClientID/PrincipalID of Identities. Nil means recheck
	// immediately. The same recheck time applies across all elements of Identities.
	// This allows the controller to avoid repeatedly hitting an Azure API to
	// recheck that the desired state is true.
	// Controllers should set this field with substantial jitter: without another
	// concern, jitter of 50% is considered normal so that any storms are quickly
	// dissipated. Additionally, long recheck times are recommended for resources
	// outside of their active phases. Order of at least six hours is, with
	// durations up to 24 hours considered normal.
	// Written by: FetchDataPlaneOperatorsManagedIdentitiesInfoController
	EarliestRecheckTime *metav1.Time `json:"earliestRecheckTime,omitempty"`
}

// AzureResources groups the Azure resource references associated with a cluster.
type AzureResources struct {
	// DenyAssignments tracks the deny assignments applied to the cluster's resources.
	DenyAssignments AzureMultiReference `json:"denyAssignments,omitempty"`
	// ManagedResourceGroup tracks the managed resource group for the cluster.
	ManagedResourceGroup AzureReference `json:"managedResourceGroup,omitempty"`
}

// AzureMultiReference tracks a set of Azure resources through their creation lifecycle.
// PendingAzureResources holds resource IDs that have been requested but not yet confirmed;
// AzureResources holds resource IDs that have been confirmed to exist.
type AzureMultiReference struct {
	// PendingAzureResources contains resource IDs that have been requested but
	// not yet confirmed to exist in Azure.
	PendingAzureResources []*azcorearm.ResourceID `json:"pendingAzureResources,omitempty"`
	// AzureResources contains resource IDs that have been confirmed to exist in Azure.
	AzureResources []*azcorearm.ResourceID `json:"azureResources,omitempty"`
	// EarliestRecheckTime is the earliest time at which the controller should
	// re-check the pending resources. Nil means recheck immediately.
	// This allows for controllers to avoid repeatedly hitting an Azure API to recheck that the desired state is true.
	// Controllers should set this field with substantial jitter: without another concern, jitter of 50% is considered normal
	// so that any storms are quickly dissipated.
	// Additionally, long recheck times are recommended for resources outside of their active phases. Order of at least
	// six hours is, with durations up to 24 hours considered normal.
	EarliestRecheckTime *metav1.Time `json:"earliestRecheckTime,omitempty"`
}

// AzureReference tracks a single Azure resource through its creation lifecycle.
// PendingAzureResources holds a resource ID that has been requested but not yet confirmed;
// AzureResources holds a resource ID that has been confirmed to exist.
type AzureReference struct {
	// PendingAzureResource is the resource ID that has been requested but
	// not yet confirmed to exist in Azure.
	PendingAzureResource *azcorearm.ResourceID `json:"pendingAzureResource,omitempty"`
	// AzureResource is the resource ID that has been confirmed to exist in Azure.
	AzureResource *azcorearm.ResourceID `json:"azureResource,omitempty"`
	// EarliestRecheckTime is the earliest time at which the controller should
	// re-check the pending resources. Nil means recheck immediately.
	// This allows for controllers to avoid repeatedly hitting an Azure API to recheck that the desired state is true.
	// Controllers should set this field with substantial jitter: without another concern, jitter of 50% is considered normal
	// so that any storms are quickly dissipated.
	// Additionally, long recheck times are recommended for resources outside of their active phases. Order of at least
	// six hours is, with durations up to 24 hours considered normal.
	EarliestRecheckTime *metav1.Time `json:"earliestRecheckTime,omitempty"`
}

// ServiceProviderClusterDataPlaneOperatorManagedIdentity contains resolved
// ClientID/PrincipalID for an Azure User Assigned Managed Identity used by one
// or more of a cluster's data plane operators.
// A cluster's data plane operator is a customer operator associated to the cluster that runs in the cluster's data plane.
// Which operators reference this identity is tracked on
// Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators.
type ServiceProviderClusterDataPlaneOperatorManagedIdentity struct {
	// ResourceID is the Azure Resource ID of the Azure User Assigned Managed Identity.
	// Its value comes from the Cluster's CustomerProperties.
	ResourceID *azcorearm.ResourceID `json:"resourceID,omitempty"`
	// ClientID is Client ID of the Azure User Assigned Managed Identity represented by ResourceID.
	// Fetched from Azure and written here by the FetchDataPlaneOperatorsManagedIdentitiesInfoController.
	ClientID *string `json:"clientID,omitempty"`
	// PrincipalID Principal ID of the Azure User Assigned Managed Identity represented by ResourceID.
	// Fetched from Azure and written here by the FetchDataPlaneOperatorsManagedIdentitiesInfoController.
	PrincipalID *string `json:"principalID,omitempty"`
}

// ServiceProviderClusterStatusVersion contains the actual version information.
type ServiceProviderClusterStatusVersion struct {
	// ActiveVersions is an array of versions currently active in the control plane, ordered with the most recent first.
	// During upgrades, multiple versions can be active simultaneously.
	ActiveVersions []HCPClusterActiveVersion `json:"active_versions,omitempty"`
}

// HCPClusterActiveVersion represents a single version active in the control plane.
type HCPClusterActiveVersion struct {
	// Version is the full version in x.y.z format (e.g., "4.19.2")
	Version *semver.Version `json:"version,omitempty"`
	// State is the update state from OpenShift (e.g. configv1.CompletedUpdate or configv1.PartialUpdate).
	State configv1.UpdateState `json:"state,omitempty"`
}

type MaestroBundleReference struct {
	// Name is a logical name that represents the Maestro Bundle conceptually.
	Name MaestroBundleInternalName `json:"name"`
	// MaestroAPIMaestroBundleName is the name of the Maestro Bundle in the Maestro API.
	// It must be unique within a given Maestro Consumer Name and Maestro Source ID.
	// Maestro's ManifestWorks Go client abstraction uses Maestro Bundle Names to
	// identify the Maestro Bundle.
	MaestroAPIMaestroBundleName string `json:"maestroAPIMaestroBundleName"`
	// MaestroAPIMaestroBundleID is the ID of the Maestro Bundle in the Maestro API.
	// Returned by the Maestro API when the Maestro Bundle is first created.
	// This attribute can be unset if the Maestro Bundle reference has been created
	// but the Maestro Bundle has not been created yet.
	// Maestro's REST API Go client abstraction uses Maestro Bundle IDs to identify the Maestro Bundle.
	MaestroAPIMaestroBundleID string `json:"maestroAPIMaestroBundleID"`
	// ResourceIdentifiers contains the identifiers for all resources within the Maestro Bundle.
	// Each entry corresponds to a ResourceIdentifier in the ManifestWork's ManifestConfigs.
	ResourceIdentifiers []MaestroBundleResourceIdentifier `json:"resourceIdentifiers,omitempty"`
}

// MaestroBundleResourceIdentifier identifies a single resource within a Maestro Bundle.
// This corresponds to a ResourceIdentifier in the ManifestWork's ManifestConfigs.
type MaestroBundleResourceIdentifier struct {
	// APIVersion is the API version of the resource (e.g. "hypershift.openshift.io/v1beta1").
	APIVersion string `json:"apiVersion"`
	// Kind is the kind of the resource (e.g. "HostedCluster").
	Kind string `json:"kind"`
	// Resource is the resource type (e.g. "hostedclusters").
	// This corresponds to the ResourceIdentifier.Resource in the ManifestWork's ManifestConfigs.
	Resource string `json:"resource"`
	// Name is the name of the resource.
	// For example, for a HostedCluster bundle this is the HostedCluster name on the management cluster.
	Name string `json:"name"`
	// Namespace is the namespace of the resource.
	// For example, for a HostedCluster bundle this is the HostedCluster namespace on the management cluster.
	Namespace string `json:"namespace"`
}

// MaestroBundleReferenceList is a list of Maestro Bundle references.
type MaestroBundleReferenceList []*MaestroBundleReference

// Get returns a copy to the Maestro Bundle reference for a given Maestro Bundle internal name. It returns a pointer
// for a clear indication of "not found", it doesn't return a reference intended for mutation of the original list.
// If the Maestro Bundle reference identifies by name does not exist, it returns nil.
// If multiple Maestro Bundle references are found for the same internal name, it returns an error.
func (l MaestroBundleReferenceList) Get(name MaestroBundleInternalName) (*MaestroBundleReference, error) {
	var bundleReference *MaestroBundleReference

	for _, bundle := range l {
		if bundle.Name == name {
			if bundleReference != nil {
				return nil, utils.TrackError(fmt.Errorf("multiple Maestro Bundle references found for the same internal name: %s", name))
			}
			bundleReference = bundle.DeepCopy()
		}
	}
	return bundleReference, nil
}

// Set sets the Maestro Bundle reference for a given Maestro Bundle internal name.
// If the Maestro Bundle reference identifies by name does not exist, it is added.
// If the Maestro Bundle reference identifies by name already exists, it is updated.
func (l *MaestroBundleReferenceList) Set(maestroBundleReference *MaestroBundleReference) error {
	existingMaestroBundleReference, err := l.Get(maestroBundleReference.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Maestro Bundle reference: %w", err))
	}
	if existingMaestroBundleReference == nil {
		*l = append(*l, maestroBundleReference)
		return nil
	}

	newMaestroBundleReference := maestroBundleReference.DeepCopy()

	for i := range *l {
		if (*l)[i].Name == maestroBundleReference.Name {
			(*l)[i] = newMaestroBundleReference
			return nil
		}
	}

	return nil
}

// Remove removes the Maestro Bundle reference for a given Maestro Bundle internal name.
// If the Maestro Bundle reference identified by name does not exist, it is a no-op.
// If multiple Maestro Bundle references are found for the same internal name, it returns an error.
func (l *MaestroBundleReferenceList) Remove(name MaestroBundleInternalName) error {
	filtered := make(MaestroBundleReferenceList, 0, len(*l))
	matched := 0
	for _, bundle := range *l {
		if bundle.Name == name {
			matched++
		} else {
			filtered = append(filtered, bundle)
		}
	}
	if matched > 1 {
		return utils.TrackError(fmt.Errorf("multiple Maestro Bundle references found for the same internal name: %s", name))
	}
	*l = filtered
	return nil
}

// MaestroBundleInternalName is a type that represents the internal name of a Maestro Bundle.
// It is used to identify the Maestro Bundle internally and to retrieve it from the MaestroBundleReferenceList.
type MaestroBundleInternalName string

const (
	// MaestroBundleInternalNameReadonlyHypershiftHostedCluster is the internal name of the Maestro Bundle that represents
	// the Cluster's Hypershift's HostedCluster K8s resource.
	MaestroBundleInternalNameReadonlyHypershiftHostedCluster MaestroBundleInternalName = "readonlyHypershiftHostedCluster"

	// ReadonlyHypershiftControlPlaneComponentClusterAutoscaler is the internal name of the ReadDesire that mirrors
	// the cluster-autoscaler ControlPlaneComponent on the management cluster control plane namespace.
	ReadonlyHypershiftControlPlaneComponentClusterAutoscaler MaestroBundleInternalName = "readonlyHypershiftControlPlaneComponentClusterAutoscaler"
)
