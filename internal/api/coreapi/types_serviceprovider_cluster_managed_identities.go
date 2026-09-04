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

package coreapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// Managed identity replacement is tracked on ServiceProviderCluster, not on the
// Cluster ARM document. Read ServiceProviderClusterManagedIdentitiesSpec for
// the slot/generation/instance model and the configure/replace/deconfigure
// flow.
//
// Managed identity condition types used on ServiceProviderCluster.Status.ManagedIdentities
// (subsystem, slot, generation, and instance). Top-level
// ServiceProviderCluster Status.Conditions stay limited to Progressing and Degraded.
const (
	// ManagedIdentityConditionConfigured is True when the object is fully configured.
	// On a generation: every identity-level configure step (metadata, MRG role
	// assignment union, deny-assignment exclude) has succeeded.
	// On a control-plane instance: the generation is Configured plus Cluster
	// Service dispatch (Key Vault later).
	// On a data-plane instance: the generation is Configured plus OIDC federation
	// and Cluster Service dispatch.
	// On an SMI instance: the generation is Configured plus Cluster Service
	// dispatch and customer-scope roles (Key Vault later).
	// On a slot: ActiveInstanceID equals Spec.InstanceID and that instance is
	// Configured.
	// On the subsystem: every spec slot is Configured and no generation or
	// instance is still deconfiguring.
	ManagedIdentityConditionConfigured = "Configured"

	// ManagedIdentityConditionIdentityMetadataResolved is True when ClientID and
	// PrincipalID are non-empty on the generation.
	ManagedIdentityConditionIdentityMetadataResolved = "IdentityMetadataResolved"

	// ManagedIdentityConditionRoleAssignmentsConfigured is True when expected
	// role assignments exist in Azure. On a generation: the identity-level MRG
	// role assignment union. On an SMI instance: customer-scope SMI role
	// assignments.
	ManagedIdentityConditionRoleAssignmentsConfigured = "RoleAssignmentsConfigured"

	// ManagedIdentityConditionDenyAssignmentExcludesPrincipal is True when the
	// cluster deny assignment excludes this generation's PrincipalID.
	ManagedIdentityConditionDenyAssignmentExcludesPrincipal = "DenyAssignmentExcludesPrincipal"

	// ManagedIdentityConditionClusterServiceDispatched is True when Cluster
	// Service has been told to use this identity for this instance (one
	// operator on a control-plane or data-plane instance; the cluster SMI
	// on an SMI instance).
	ManagedIdentityConditionClusterServiceDispatched = "ClusterServiceDispatched"

	// ManagedIdentityConditionOIDCFederationConfigured is True when federated
	// identity credentials for this data-plane instance's operator subjects exist.
	ManagedIdentityConditionOIDCFederationConfigured = "OIDCFederationConfigured"

	// ManagedIdentityConditionDeconfigured is True when the object is fully
	// retired and can be dropped. On a generation: identity-level teardown is
	// done and no instance still names it. On a control-plane instance: Cluster
	// Service dispatch teardown is done. On a data-plane instance: OIDC teardown
	// is done. On an SMI instance: type-particular teardown is done, including
	// customer-scope roles.
	ManagedIdentityConditionDeconfigured = "Deconfigured"
)

// ManagedIdentityReplacementTrigger is why a generation or a type instance
// was created.
type ManagedIdentityReplacementTrigger string

const (
	// ManagedIdentityReplacementTriggerCreate is set when the generation or
	// instance is the first identity for a slot (cluster create or a newly
	// added operator).
	ManagedIdentityReplacementTriggerCreate ManagedIdentityReplacementTrigger = "Create"
	// ManagedIdentityReplacementTriggerUserResourceIDChange is set when the
	// slot's desired ResourceID changed (user PUT/PATCH).
	ManagedIdentityReplacementTriggerUserResourceIDChange ManagedIdentityReplacementTrigger = "UserResourceIDChange"
	// ManagedIdentityReplacementTriggerObservedClientOrPrincipalIDChange is set
	// when fetch observed that ClientID and/or PrincipalID for the same ResourceID
	// no longer match the values stored on the previous generation.
	ManagedIdentityReplacementTriggerObservedClientOrPrincipalIDChange ManagedIdentityReplacementTrigger = "ObservedClientOrPrincipalIDChange"
)

// ServiceProviderClusterManagedIdentitiesSpec is the desired identity for each
// operator and for the service managed identity (SMI).
//
// # Why this lives on ServiceProviderCluster
//
// Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities
// is the ARM-visible **latest** desired ResourceID per operator. Frontend PUT/PATCH
// overwrites those maps.
//
// This Spec, plus Status.ManagedIdentities, is the internal mechanism used to
// configure the new identity, deconfigure the old one, and survive a sequence
// of replacements before intermediate ones finish (A then B then C) so we can
// clean up the old ones.
//
// # Slot
//
// A slot is one operator (or the SMI) asking "which identity should I use?".
// There is one slot per control-plane operator name, one per data-plane
// operator name, and one SMI slot. Slots are keyed by operator name as stored
// on the Cluster maps (not lowercased, not by ResourceID).
//
// # Generation
//
// A generation is one Azure identity (ResourceID + ClientID + PrincipalID) plus
// identity-level configure/deconfigure progress that is independent of operator
// and of configuration process: metadata fetch, deny-assignment exclude of that
// principal, and the managed-resource-group role-assignment union across every
// operator using that principal.
//
// It is keyed by a UUID (GenerationID), not by ResourceID, because
// ClientID/PrincipalID can change for the same ResourceID and that is a new
// generation. Control-plane, SMI, and data-plane that use the same ResourceID
// share one generation so identity-level work does not run twice.
//
// # Instance
//
// An instance is one slot's occupancy of one generation: configure/deconfigure
// progress for that slot against that Azure identity. Control-plane operators,
// data-plane operators, and SMI each have their own instance map. Two operators
// that Spec the same ResourceID get two instances and share one generation.
// A control-plane slot and the SMI slot never share an instance; they share a
// generation. A control-plane slot and a data-plane slot share only a generation.
//
// Control-plane and data-plane instances store OperatorName (as on Cluster, not
// lowercased) so the instance map is readable without scanning slots. SMI has
// no operator; there is one SMI slot per cluster.
//
// Each instance's GenerationID is assigned at create and is never rewritten.
// Replacement opens a new instance and moves the slot's InstanceID. Two
// instances can share OperatorName during replacement (Active vs Spec).
//
// Spec.InstanceID is the instance this slot wants in service. Status.Active*
// is the instance operators / Cluster Service are actually using. They differ
// during replacement until the desired instance (and its generation) is
// Configured.
//
// # Flow
//
//  1. Frontend writes new desired ResourceIDs on the Cluster and starts an
//     Update operation. It does not write this Spec, generations, or instances.
//  2. ManagedIdentitiesCoordinator copies Cluster ResourceIDs onto these slots.
//     A new ResourceID, or observed ClientID/PrincipalID drift, opens a new
//     generation when none is already Spec-desired for that ResourceID, opens
//     a new instance for this slot pointing at that generation, and points
//     Spec.InstanceID at the instance. Two operators share a generation, not
//     an instance. Control-plane and SMI share a generation, not an instance.
//     Control-plane and data-plane share a generation, not an instance.
//  3. Fetch controllers fill generation ClientID/PrincipalID. If Azure later
//     returns different values they set Observed* instead of overwriting, and
//     the coordinator opens a new generation and a new instance for every slot
//     that Spec'd the old generation.
//  4. On create, Active is set once ClientID and PrincipalID are known. On
//     replacement, Active moves only when the desired instance is Configured
//     (generation plus this slot's steps). Until then the previous instance
//     stays Active.
//  5. An instance with no slot Spec and no slot Active starts deconfiguring.
//     When Deconfigured it is dropped from its type map.
//  6. A generation with no remaining instance starts identity-level
//     deconfiguring. When Deconfigured it is dropped from Generations.
//
// Overlapping replacements (user changes A to B, then B to C before B is
// ready): Spec points at C. B is abandoned and deconfigures immediately. A
// stays Active until C is Configured for this slot.
//
// Cluster.Status.OperatorIdentities is a distilled copy of this ledger for
// Cluster-document readers. It is not an ARM ResourceStatus field.
type ServiceProviderClusterManagedIdentitiesSpec struct {
	// ControlPlaneOperators is keyed by operator name as stored on
	// Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators
	// (for example "cloud-controller-manager"). Keys are not lowercased; they
	// keep the Cluster map's casing. Lookups must use that same name, not a
	// lowercased form and not the identity ResourceID.
	// Written by: ManagedIdentitiesCoordinator
	ControlPlaneOperators map[string]*ManagedIdentitySlotSpec `json:"controlPlaneOperators,omitempty"`
	// DataPlaneOperators is keyed by operator name as stored on
	// Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators
	// (for example "image-registry"). Keys are not lowercased; they keep the
	// Cluster map's casing. Lookups must use that same name, not a lowercased
	// form and not the identity ResourceID.
	// Written by: ManagedIdentitiesCoordinator
	DataPlaneOperators map[string]*ManagedIdentitySlotSpec `json:"dataPlaneOperators,omitempty"`
	// ServiceManagedIdentity is the desired identity for the cluster's service
	// managed identity. Nil when the Cluster has no ServiceManagedIdentity ResourceID.
	// Written by: ManagedIdentitiesCoordinator
	ServiceManagedIdentity *ManagedIdentitySlotSpec `json:"serviceManagedIdentity,omitempty"`
}

// ManagedIdentitySlotSpec is the desired Azure identity for one slot (one
// control-plane operator, one data-plane operator, or the SMI). See
// ServiceProviderClusterManagedIdentitiesSpec for what a slot is.
type ManagedIdentitySlotSpec struct {
	// ResourceID is the latest desired Azure user-assigned identity ResourceID,
	// mirrored from Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.
	// Written by: ManagedIdentitiesCoordinator
	ResourceID *azcorearm.ResourceID `json:"resourceID,omitempty"`
	// InstanceID is the type-map key this slot wants in service. Control-plane
	// slots resolve it in Status.ManagedIdentities.ControlPlaneInstances. SMI
	// slots resolve it in Status.ManagedIdentities.ServiceManagedIdentityInstances.
	// Data-plane slots resolve it in Status.ManagedIdentities.DataPlaneInstances.
	// It is the instance UUID as generated (uuid.NewString), not a ResourceID
	// and not lowercased. The instance's GenerationID is the generation-map key.
	// Written by: ManagedIdentitiesCoordinator
	InstanceID string `json:"instanceID,omitempty"`
}

// ServiceProviderClusterManagedIdentitiesStatus is the observed identity ledger:
// which instance is Active on each slot, the shared generation map, the three
// type instance maps, nested conditions, and the two fetch recheck times
// (MSI dataplane vs data-plane ARM). See
// ServiceProviderClusterManagedIdentitiesSpec for the
// slot/generation/instance model.
type ServiceProviderClusterManagedIdentitiesStatus struct {
	// Conditions for the identity subsystem as a whole.
	// Known types: Configured.
	// Written by: ManagedIdentitiesStatusAggregator
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// MSIIdentitiesEarliestRecheckTime is when FetchMSIIdentitiesInfo should next
	// query the Managed Identities Data Plane for ClientID/PrincipalID of
	// Spec-desired control-plane and SMI generations. Nil means recheck immediately.
	// One timestamp for that whole fetch (one dataplane call), not per generation.
	// Honor it only when the desired MSI ResourceID set is unchanged; on ResourceID
	// set change, query immediately. Jitter ~50%; idle recheck on the order of
	// 6-24 hours.
	// Written by: FetchMSIIdentitiesInfo
	MSIIdentitiesEarliestRecheckTime *metav1.Time `json:"msiIdentitiesEarliestRecheckTime,omitempty"`
	// DataPlaneOperatorsIdentitiesEarliestRecheckTime is when
	// FetchDataPlaneOperatorsManagedIdentitiesInfo should next query Azure ARM
	// for ClientID/PrincipalID of Spec-desired data-plane generations. Nil means
	// recheck immediately. Separate from MSIIdentitiesEarliestRecheckTime because
	// that is a different controller and API. Same honor/jitter rules as MSI.
	// Written by: FetchDataPlaneOperatorsManagedIdentitiesInfo
	DataPlaneOperatorsIdentitiesEarliestRecheckTime *metav1.Time `json:"dataPlaneOperatorsIdentitiesEarliestRecheckTime,omitempty"`

	// ControlPlaneOperators is keyed by operator name as stored on
	// Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.ControlPlaneOperators.
	// Keys are not lowercased; they keep the Cluster map's casing and match
	// Spec.ManagedIdentities.ControlPlaneOperators. Lookups must use that same
	// name, not a lowercased form and not the identity ResourceID.
	// Written by: ManagedIdentitiesCoordinator (Active* fields), ManagedIdentitiesStatusAggregator (Conditions)
	ControlPlaneOperators map[string]*ManagedIdentitySlotStatus `json:"controlPlaneOperators,omitempty"`
	// DataPlaneOperators is keyed by operator name as stored on
	// Cluster.CustomerProperties.Platform.OperatorsAuthentication.UserAssignedIdentities.DataPlaneOperators.
	// Keys are not lowercased; they keep the Cluster map's casing and match
	// Spec.ManagedIdentities.DataPlaneOperators. Lookups must use that same
	// name, not a lowercased form and not the identity ResourceID.
	// Written by: ManagedIdentitiesCoordinator (Active* fields), ManagedIdentitiesStatusAggregator (Conditions)
	DataPlaneOperators map[string]*ManagedIdentitySlotStatus `json:"dataPlaneOperators,omitempty"`
	// ServiceManagedIdentity is the observed Active identity for the cluster's
	// service managed identity. Nil when Spec.ServiceManagedIdentity is absent.
	// Written by: ManagedIdentitiesCoordinator (Active* fields), ManagedIdentitiesStatusAggregator (Conditions)
	ServiceManagedIdentity *ManagedIdentitySlotStatus `json:"serviceManagedIdentity,omitempty"`

	// Generations is the durable set of Azure identity generations
	// (ResourceID + ClientID + PrincipalID) we have started configuring and
	// have not finished deconfiguring. The key is ManagedIdentityGeneration.GenerationID
	// (the UUID assigned when the generation is created). It is not a ResourceID
	// and is not lowercased. Control-plane, SMI, and data-plane instances that
	// use the same ResourceID share one generation.
	// Written by: ManagedIdentitiesCoordinator (membership, Trigger, DeconfigurationStarted),
	// FetchMSIIdentitiesInfo, FetchDataPlaneOperatorsManagedIdentitiesInfo
	// (ClientID/PrincipalID/Observed*/RetrievalError)
	Generations map[string]*ManagedIdentityGeneration `json:"generations,omitempty"`

	// ControlPlaneInstances is the durable set of control-plane instances we
	// have started configuring and have not finished deconfiguring. The key is
	// ControlPlaneManagedIdentityInstance.InstanceID (the UUID assigned when
	// the instance is created). It is not an operator name, not a ResourceID,
	// and is not lowercased. One Spec control-plane slot names one live
	// instance; the map also holds instances still deconfiguring. Two operators
	// that Spec the same ResourceID get two instances and share one generation.
	// OperatorName on the instance is the slot it belongs to. Each instance
	// points at a GenerationID.
	// Written by: ManagedIdentitiesCoordinator (membership, Trigger, DeconfigurationStarted)
	ControlPlaneInstances map[string]*ControlPlaneManagedIdentityInstance `json:"controlPlaneInstances,omitempty"`

	// ServiceManagedIdentityInstances is the durable set of SMI type instances
	// we have started configuring and have not finished deconfiguring. The key
	// is ServiceManagedIdentityInstance.InstanceID. There is at most one Spec
	// SMI slot, so this map is typically one live instance plus any still
	// deconfiguring. Each instance points at a GenerationID.
	// Written by: ManagedIdentitiesCoordinator (membership, Trigger, DeconfigurationStarted)
	ServiceManagedIdentityInstances map[string]*ServiceManagedIdentityInstance `json:"serviceManagedIdentityInstances,omitempty"`

	// DataPlaneInstances is the durable set of data-plane instances we have
	// started configuring and have not finished deconfiguring. The key is
	// DataPlaneManagedIdentityInstance.InstanceID. One Spec data-plane slot
	// names one live instance; the map also holds instances still deconfiguring.
	// Two operators that Spec the same ResourceID get two instances and share
	// one generation. OperatorName on the instance is the slot it belongs to.
	// Each instance points at a GenerationID.
	// Written by: ManagedIdentitiesCoordinator (membership, Trigger, DeconfigurationStarted)
	DataPlaneInstances map[string]*DataPlaneManagedIdentityInstance `json:"dataPlaneInstances,omitempty"`
}

// ManagedIdentitySlotStatus is what is actually in service for one slot.
// Active* is the identity operators / Cluster Service are using. During
// replacement it stays on the previous instance until the Spec-desired
// instance is Configured (generation plus this slot's steps). See
// ServiceProviderClusterManagedIdentitiesSpec.
type ManagedIdentitySlotStatus struct {
	// Conditions:
	// - "Configured": ActiveInstanceID equals Spec.InstanceID and that instance
	//   is Configured.
	// Written by: ManagedIdentitiesStatusAggregator
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ActiveInstanceID is the type-map key of the identity Cluster Service /
	// operators are using for this slot. Same form as Spec.InstanceID: the instance
	// UUID, not a ResourceID and not lowercased. Empty means nothing has been
	// activated yet (create path). Control-plane slots resolve it in
	// ControlPlaneInstances; SMI slots in ServiceManagedIdentityInstances;
	// data-plane slots in DataPlaneInstances.
	// Written by: ManagedIdentitiesCoordinator
	ActiveInstanceID string `json:"activeInstanceID,omitempty"`
	// ActiveResourceID is the Azure ResourceID of the Active instance. Copied
	// from the Active instance's generation so consumers need not look up
	// the generation. Empty until ActiveInstanceID is set.
	// Written by: ManagedIdentitiesCoordinator
	ActiveResourceID *azcorearm.ResourceID `json:"activeResourceID,omitempty"`
	// ClientID is the Client ID of the Active instance. Copied from
	// the Active instance's generation. Empty until metadata is resolved
	// and ActiveInstanceID is set.
	// Written by: ManagedIdentitiesCoordinator
	ClientID *string `json:"clientID,omitempty"`
	// PrincipalID is the Principal ID of the Active instance. Copied from
	// the Active instance's generation. Empty until metadata is resolved
	// and ActiveInstanceID is set.
	// Written by: ManagedIdentitiesCoordinator
	PrincipalID *string `json:"principalID,omitempty"`
}

// ManagedIdentityGeneration is one Azure identity (ResourceID + ClientID +
// PrincipalID) plus identity-level configure/deconfigure progress. Control-plane,
// SMI, and data-plane instances that use this identity share this object so
// metadata fetch, deny-assignment exclude, and the MRG role-assignment union
// run once. See ServiceProviderClusterManagedIdentitiesSpec.
type ManagedIdentityGeneration struct {
	// GenerationID is this generation's UUID and the key of Status.ManagedIdentities.Generations.
	// It is assigned at creation (uuid.NewString). It is not a ResourceID and is
	// not lowercased.
	// Written by: ManagedIdentitiesCoordinator
	GenerationID string `json:"generationID"`

	// ResourceID is the Azure user-assigned identity this generation represents.
	// The same ResourceID can appear on more than one generation across replacements
	// (ClientID/PrincipalID drift creates a new generation of the same ResourceID).
	// Written by: ManagedIdentitiesCoordinator
	ResourceID *azcorearm.ResourceID `json:"resourceID,omitempty"`
	// ClientID is the Client ID stored for this generation. Filled by fetch when
	// empty. Never overwritten in place when Azure later returns a different
	// value; that difference is written to ObservedClientID instead. On a
	// ClientID/PrincipalID-drift generation the coordinator copies the observed
	// value in at creation.
	// Written by: ManagedIdentitiesCoordinator (drift create), FetchMSIIdentitiesInfo,
	// FetchDataPlaneOperatorsManagedIdentitiesInfo
	ClientID *string `json:"clientID,omitempty"`
	// PrincipalID is the Principal ID stored for this generation. Filled by fetch
	// when empty. Never overwritten in place when Azure later returns a different
	// value; that difference is written to ObservedPrincipalID instead. On a
	// ClientID/PrincipalID-drift generation the coordinator copies the observed
	// value in at creation.
	// Written by: ManagedIdentitiesCoordinator (drift create), FetchMSIIdentitiesInfo,
	// FetchDataPlaneOperatorsManagedIdentitiesInfo
	PrincipalID *string `json:"principalID,omitempty"`

	// ObservedClientID is set by fetch controllers when Azure returns a ClientID
	// that differs from ClientID. The coordinator opens a new generation rather
	// than overwriting ClientID in place. Cleared on the old generation after the
	// new generation is created.
	// Written by: FetchMSIIdentitiesInfo, FetchDataPlaneOperatorsManagedIdentitiesInfo,
	// ManagedIdentitiesCoordinator (clears after retarget)
	ObservedClientID *string `json:"observedClientID,omitempty"`
	// ObservedPrincipalID is set by fetch controllers when Azure returns a
	// PrincipalID that differs from PrincipalID. Cleared on the old generation
	// after the new generation is created.
	// Written by: FetchMSIIdentitiesInfo, FetchDataPlaneOperatorsManagedIdentitiesInfo,
	// ManagedIdentitiesCoordinator (clears after retarget)
	ObservedPrincipalID *string `json:"observedPrincipalID,omitempty"`

	// RetrievalError, when non-nil, is the error (truncated to the first 1024
	// characters) from the most recent attempt to retrieve this generation's
	// metadata from Azure. When set, the last retrieval failed (not found or
	// Get error) and ClientID/PrincipalID from that attempt are untrustworthy.
	// Nil when the last retrieval succeeded. Successful retrieval with different
	// IDs uses Observed* instead of this field.
	// Written by: FetchMSIIdentitiesInfo, FetchDataPlaneOperatorsManagedIdentitiesInfo
	RetrievalError *string `json:"retrievalError,omitempty"`

	// Trigger is why this generation was created. See ManagedIdentityReplacementTrigger.
	// Written by: ManagedIdentitiesCoordinator
	Trigger ManagedIdentityReplacementTrigger `json:"trigger,omitempty"`

	Status ManagedIdentityGenerationStatus `json:"status,omitempty"`
}

// ManagedIdentityGenerationStatus is identity-level configure/deconfigure
// progress. Configured means metadata, MRG role assignments, and
// deny-assignment exclude are True. Slot-particular progress lives on
// instances.
type ManagedIdentityGenerationStatus struct {
	// Conditions:
	// - IdentityMetadataResolved
	// - RoleAssignmentsConfigured
	// - DenyAssignmentExcludesPrincipal
	// - Configured (all identity-level configure steps True)
	// - Deconfigured (identity-level retirement complete and no instances remain)
	// Written by: ManagedIdentitiesStatusAggregator
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// DeconfigurationStarted is set when no type instance still names this
	// generation. Identity-level teardown (deny exclude, MRG role assignments)
	// runs after that.
	// Written by: ManagedIdentitiesCoordinator
	DeconfigurationStarted *metav1.Time `json:"deconfigurationStarted,omitempty"`

	// RoleAssignments tracks managed-resource-group role assignments created
	// for this generation's PrincipalID. This is the union of role definitions of
	// every operator currently using this principal, across control-plane, SMI,
	// and data-plane instances. PendingAzureResources are requested but not yet
	// confirmed in Azure; AzureResources are confirmed.
	// Written by: (not yet; role-assignment controller will write per generation)
	RoleAssignments AzureMultiReference `json:"roleAssignments,omitempty"`
}

// ControlPlaneManagedIdentityInstance is one control-plane operator's occupancy
// of one generation. Cluster Service dispatch lives on Status. The Azure
// generation lives on Status.ManagedIdentities.Generations[GenerationID]. See
// ServiceProviderClusterManagedIdentitiesSpec.
type ControlPlaneManagedIdentityInstance struct {
	// InstanceID is this instance's UUID and the key of ControlPlaneInstances.
	// Written by: ManagedIdentitiesCoordinator
	InstanceID string `json:"instanceID"`
	// GenerationID is the key of Status.ManagedIdentities.Generations this
	// instance uses. Assigned at create and never rewritten. ClientID or
	// PrincipalID drift, or a slot ResourceID change, opens a new instance
	// (and a new generation when needed) and moves Spec.InstanceID.
	// Written by: ManagedIdentitiesCoordinator
	GenerationID string `json:"generationID,omitempty"`
	// OperatorName is the control-plane operator this instance belongs to,
	// as stored on Cluster (not lowercased). Assigned at create. Two instances
	// can share this name during replacement (Active vs Spec).
	// Written by: ManagedIdentitiesCoordinator
	OperatorName string `json:"operatorName,omitempty"`
	// Trigger is why this instance was created. See
	// ManagedIdentityReplacementTrigger. Distinct from the generation Trigger
	// when a later slot starts using an already-created generation.
	// Written by: ManagedIdentitiesCoordinator
	Trigger ManagedIdentityReplacementTrigger         `json:"trigger,omitempty"`
	Status  ControlPlaneManagedIdentityInstanceStatus `json:"status,omitempty"`
}

// ServiceManagedIdentityInstance is one SMI occupancy of one generation plus
// SMI configure/deconfigure progress. The SMI is cluster-scoped, not an
// operator. The Azure generation lives on
// Status.ManagedIdentities.Generations[GenerationID]. See
// ServiceProviderClusterManagedIdentitiesSpec.
type ServiceManagedIdentityInstance struct {
	// InstanceID is this instance's UUID and the key of ServiceManagedIdentityInstances.
	// Written by: ManagedIdentitiesCoordinator
	InstanceID string `json:"instanceID"`
	// GenerationID is the key of Status.ManagedIdentities.Generations this
	// instance uses. Assigned at create and never rewritten.
	// Written by: ManagedIdentitiesCoordinator
	GenerationID string `json:"generationID,omitempty"`
	// Trigger is why this instance was created. See
	// ManagedIdentityReplacementTrigger.
	// Written by: ManagedIdentitiesCoordinator
	Trigger ManagedIdentityReplacementTrigger    `json:"trigger,omitempty"`
	Status  ServiceManagedIdentityInstanceStatus `json:"status,omitempty"`
}

// DataPlaneManagedIdentityInstance is one data-plane operator's occupancy of
// one generation. OIDC federation and Cluster Service dispatch live on Status.
// The Azure generation lives on Status.ManagedIdentities.Generations[GenerationID].
// See ServiceProviderClusterManagedIdentitiesSpec.
type DataPlaneManagedIdentityInstance struct {
	// InstanceID is this instance's UUID and the key of DataPlaneInstances.
	// Written by: ManagedIdentitiesCoordinator
	InstanceID string `json:"instanceID"`
	// GenerationID is the key of Status.ManagedIdentities.Generations this
	// instance uses. Assigned at create and never rewritten. ClientID or
	// PrincipalID drift, or a slot ResourceID change, opens a new instance
	// (and a new generation when needed) and moves Spec.InstanceID.
	// Written by: ManagedIdentitiesCoordinator
	GenerationID string `json:"generationID,omitempty"`
	// OperatorName is the data-plane operator this instance belongs to,
	// as stored on Cluster (not lowercased). Assigned at create. Two instances
	// can share this name during replacement (Active vs Spec).
	// Written by: ManagedIdentitiesCoordinator
	OperatorName string `json:"operatorName,omitempty"`
	// Trigger is why this instance was created. See
	// ManagedIdentityReplacementTrigger.
	// Written by: ManagedIdentitiesCoordinator
	Trigger ManagedIdentityReplacementTrigger      `json:"trigger,omitempty"`
	Status  DataPlaneManagedIdentityInstanceStatus `json:"status,omitempty"`
}

// ControlPlaneManagedIdentityInstanceStatus is this operator's configure/deconfigure
// progress. Configured means the generation is Configured and Cluster Service
// has been told to use this identity for this operator (Key Vault later).
type ControlPlaneManagedIdentityInstanceStatus struct {
	// Conditions:
	// - ClusterServiceDispatched
	// - Configured (generation Configured, Cluster Service dispatched)
	// - Deconfigured (Cluster Service dispatch teardown complete)
	// Written by: ManagedIdentitiesStatusAggregator
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// DeconfigurationStarted is set when no slot Spec or Active still names this
	// instance. For a previously Active instance that is after the successor is
	// Active. For an abandoned intermediate that is as soon as no slot desires it.
	// Written by: ManagedIdentitiesCoordinator
	DeconfigurationStarted *metav1.Time `json:"deconfigurationStarted,omitempty"`
}

// ServiceManagedIdentityInstanceStatus is SMI configure/deconfigure progress.
// Configured means the generation is Configured, Cluster Service has been
// told to use this identity, and customer-scope SMI role assignments exist
// (Key Vault later; none today).
type ServiceManagedIdentityInstanceStatus struct {
	// Conditions:
	// - ClusterServiceDispatched
	// - RoleAssignmentsConfigured
	// - Configured (generation Configured, Cluster Service dispatched, customer-scope roles)
	// - Deconfigured (type-particular retirement complete, including customer-scope roles)
	// Written by: ManagedIdentitiesStatusAggregator
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// DeconfigurationStarted is set when no slot Spec or Active still names this
	// instance. For a previously Active instance that is after the successor is
	// Active. For an abandoned intermediate that is as soon as no slot desires it.
	// Written by: ManagedIdentitiesCoordinator
	DeconfigurationStarted *metav1.Time `json:"deconfigurationStarted,omitempty"`

	// ServiceManagedIdentityRoleAssignments tracks customer-scope role
	// assignments for the service managed identity.
	// Written by: (not yet; SMI role-assignment controller will write per instance)
	ServiceManagedIdentityRoleAssignments AzureMultiReference `json:"serviceManagedIdentityRoleAssignments,omitempty"`
}

// DataPlaneManagedIdentityInstanceStatus is this operator's configure/deconfigure
// progress. Configured means the generation is Configured, OIDC federation for
// this operator's subjects exists, and Cluster Service has been told to use
// this identity for this operator.
type DataPlaneManagedIdentityInstanceStatus struct {
	// Conditions:
	// - OIDCFederationConfigured
	// - ClusterServiceDispatched
	// - Configured (generation Configured, OIDC, Cluster Service dispatched)
	// - Deconfigured (OIDC teardown complete)
	// Written by: ManagedIdentitiesStatusAggregator
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// DeconfigurationStarted is set when no slot Spec or Active still names this
	// instance. For a previously Active instance that is after the successor is
	// Active. For an abandoned intermediate that is as soon as no slot desires it.
	// Written by: ManagedIdentitiesCoordinator
	DeconfigurationStarted *metav1.Time `json:"deconfigurationStarted,omitempty"`

	// OIDCFederation tracks federated identity credentials for this operator's
	// Kubernetes service account subjects.
	// Written by: (not yet; OIDC federation controller will write per instance)
	OIDCFederation DataPlaneOIDCFederationStatus `json:"oidcFederation,omitempty"`
}

// DataPlaneOIDCFederationStatus is OIDC federation progress for one data-plane
// instance. Subjects are that operator's KubernetesServiceAccounts.
type DataPlaneOIDCFederationStatus struct {
	// FederatedIdentityCredentials tracks federated identity credentials created
	// on the user-assigned identity for this operator's subjects.
	// Written by: (not yet; OIDC federation controller will write per instance)
	FederatedIdentityCredentials AzureMultiReference `json:"federatedIdentityCredentials,omitempty"`
}

// IdentityMetadataResolved reports whether ClientID and PrincipalID are both non-empty.
func (g *ManagedIdentityGeneration) IdentityMetadataResolved() bool {
	if g == nil {
		return false
	}
	return nonEmptyStringPtr(g.ClientID) && nonEmptyStringPtr(g.PrincipalID)
}

// HasObservedIdentityDrift reports whether fetch observed ClientID or PrincipalID
// values that differ from the ones stored on this generation.
func (g *ManagedIdentityGeneration) HasObservedIdentityDrift() bool {
	if g == nil {
		return false
	}
	if nonEmptyStringPtr(g.ObservedClientID) && !stringPtrsEqual(g.ClientID, g.ObservedClientID) {
		return true
	}
	if nonEmptyStringPtr(g.ObservedPrincipalID) && !stringPtrsEqual(g.PrincipalID, g.ObservedPrincipalID) {
		return true
	}
	return false
}

// nonEmptyStringPtr reports whether s points at a non-empty string.
func nonEmptyStringPtr(s *string) bool {
	return s != nil && len(*s) > 0
}

// stringPtrsEqual reports whether a and b are both nil or both point at equal strings.
func stringPtrsEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
