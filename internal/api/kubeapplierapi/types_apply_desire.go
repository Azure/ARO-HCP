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

package kubeapplierapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// ApplyDesireType is the discriminator for the ApplyDesire union.
// +k8s:union
type ApplyDesireType string

const (
	// ApplyDesireTypeServerSideApply indicates the desire is a server-side-apply
	// of .spec.serverSideApply.kubeContent to the management cluster.
	// +k8s:unionMember
	ApplyDesireTypeServerSideApply ApplyDesireType = "ServerSideApply"

	// ApplyDesireTypeDelete indicates the desire is a deletion of .spec.targetItem
	// from the management cluster.
	// +k8s:unionMember
	ApplyDesireTypeDelete ApplyDesireType = "Delete"
)

// ApplyDesire holds a single intent to either server-side-apply a Kubernetes
// object or delete one from the management cluster's apiserver. The
// .spec.type field discriminates between the two operations.
//
// Each ApplyDesire targets exactly one kube object — there is no list form.
//
// Deleting an ApplyDesire from Cosmos has no effect on the kube object that
// was applied or deleted. To stop reconciliation, remove the desire document.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ApplyDesire struct {
	// CosmosMetadata.ResourceID is nested under an HCPOpenShiftCluster (and
	// optionally a NodePool) so that listing the partition by parent prefix
	// naturally returns the desires associated with that resource — and so
	// that cluster/nodepool deletion can sweep them.
	// PartitionKey holds the lowercased Spec.ManagementCluster resource ID
	// (e.g. "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default")
	// so that all desires for one management cluster live together in one Cosmos
	// partition (and one container, in the per-MC container model).
	coreapi.CosmosMetadata `json:"cosmosMetadata"`

	Spec ApplyDesireSpec `json:"spec"`

	Status ApplyDesireStatus `json:"status"`

	// Tags are optional key-value pairs stored alongside the desire in Cosmos.
	// Callers filter on tag presence Go-side after listing.
	Tags map[string]string `json:"tags,omitempty"`
}

// ApplyDesireSpec is the specification for an ApplyDesire. It uses a
// discriminated union via the Type field:
//
//   - Type=ServerSideApply: the ServerSideApply field must be non-nil and
//     contains the KubeContent to apply.
//   - Type=Delete: the ServerSideApply field must be nil. The controller
//     deletes .spec.targetItem and waits for finalizers.
//
// +k8s:discriminator=Type
type ApplyDesireSpec struct {
	// ManagementCluster names the management cluster whose kube-applier should
	// reconcile this desire. It is the cosmos partition key for the
	// kube-applier container; entries from one management cluster never see
	// entries from another.
	// This is the same resourceID as the fleetapi.ManagementCluster.CosmosMetadata.ResourceID
	// Example: "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"
	ManagementCluster *azcorearm.ResourceID `json:"managementCluster"`

	// Type discriminates the operation: ServerSideApply or Delete.
	// +k8s:union
	Type ApplyDesireType `json:"type"`

	// TargetItem identifies the GVR (and optionally the namespace) of the
	// target Kubernetes resource. For ServerSideApply, the controller uses
	// Group + Resource verbatim rather than guessing a plural form from
	// KubeContent's kind. Name and Namespace must agree with
	// KubeContent.metadata.{name,namespace}; the controller does not
	// re-derive them from the manifest. For Delete, TargetItem identifies
	// the single kube object to delete.
	TargetItem ResourceReference `json:"targetItem"`

	// ServerSideApply holds the configuration for the ServerSideApply variant.
	// Must be non-nil when Type=ServerSideApply; must be nil when Type=Delete.
	// +k8s:unionMember=ServerSideApply
	ServerSideApply *ServerSideApplyConfig `json:"serverSideApply,omitempty"`
}

// ServerSideApplyConfig holds fields specific to the ServerSideApply variant
// of ApplyDesire.
type ServerSideApplyConfig struct {
	// KubeContent is a single Kubernetes object (not a List) to be applied
	// with server-side-apply and Force=true. The object must carry apiVersion,
	// kind, metadata.name, and metadata.namespace if namespaced.
	//
	// A nil pointer (or one with an empty Raw) is treated as a pre-check
	// failure: the kube-applier needs an object to apply.
	//
	// By default the kube-applier issues SSA with
	// FieldManager="aro-hcp-kube-applier", so that every field the kube-applier
	// owns on the cluster traces to that one string and an operator inspecting
	// fieldsV1 metadata can attribute ownership at a glance. That default can be
	// overridden per-desire via the FieldManager field below.
	KubeContent *runtime.RawExtension `json:"kubeContent,omitempty"`

	// FieldManager optionally overrides the server-side-apply field-manager
	// name used for this desire. When nil or empty, the kube-applier uses its
	// default manager name ("aro-hcp-kube-applier"). When set to a non-empty
	// value, that value is used verbatim as the SSA FieldManager.
	//
	// This exists to support migrating field ownership cleanly from another
	// manager (e.g. cluster-service): applying as the manager that currently
	// owns the fields lets the kube-applier adopt them without a conflict,
	// after which subsequent desires can drop back to the default.
	FieldManager *string `json:"fieldManager,omitempty"`
}

type ApplyDesireStatus struct {
	// Conditions reports per-desire reconciliation status. Well-known types:
	//   - "SuccessfullyApplied": set for Type=ServerSideApply. True means the SSA
	//                   succeeded.
	//   - "SuccessfullyDeleted": set for Type=Delete. True means the target is
	//                   gone. While finalizers are running it stays False with
	//                   reason "WaitingForDeletion".
	//   - "Successful": retained for backwards compatibility. It mirrors whichever
	//                   operation-specific condition applies (SuccessfullyApplied
	//                   for ServerSideApply, SuccessfullyDeleted for Delete) with
	//                   the same status/reason/message. Prefer the
	//                   operation-specific condition; see
	//                   kubeapplierapihelpers.IsConditionTruePreferring.
	//   - "Degraded":   the controller is not making progress for an
	//                   out-of-band reason.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AppliedKubeGeneration records the metadata.generation of the
	// Kubernetes object returned by the most recent successful server-side
	// apply call. When the most recent apply attempt failed, this field is
	// nil so that consumers can distinguish "last apply succeeded and the
	// kube object is at generation N" from "last apply failed".
	AppliedKubeGeneration *int64 `json:"appliedKubeGeneration,omitempty"`
}
