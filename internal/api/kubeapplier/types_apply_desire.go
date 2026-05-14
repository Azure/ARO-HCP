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

package kubeapplier

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

// ApplyDesire holds a single Kubernetes object to be server-side-applied to
// the management cluster's apiserver. Each ApplyDesire targets exactly one
// kube object — there is no list form.
//
// Deleting an ApplyDesire from Cosmos has no effect on the kube object that
// was applied. To remove the kube object, create a corresponding DeleteDesire.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ApplyDesire struct {
	// CosmosMetadata.ResourceID is nested under an HCPOpenShiftCluster (and
	// optionally a NodePool) so that listing the partition by parent prefix
	// naturally returns the desires associated with that resource — and so
	// that cluster/nodepool deletion can sweep them.
	api.CosmosMetadata `json:"cosmosMetadata"`

	Spec ApplyDesireSpec `json:"spec"`

	Status ApplyDesireStatus `json:"status"`
}

type ApplyDesireSpec struct {
	// ManagementCluster names the management cluster whose kube-applier should
	// reconcile this desire. It is the cosmos partition key for the
	// kube-applier container; entries from one management cluster never see
	// entries from another.
	// This is the same resourceID as the fleetapi.ManagementCluster.CosmosMetadata.ResourceID
	// Example: "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"
	ManagementCluster *azcorearm.ResourceID `json:"managementCluster"`

	// TargetItem identifies the GVR (and optionally the namespace) the kube-applier
	// will server-side-apply against. The controller uses Group + Resource verbatim
	// rather than guessing a plural form from KubeContent's kind. Name and Namespace
	// must agree with KubeContent.metadata.{name,namespace}; the controller does not
	// re-derive them from the manifest.
	TargetItem ResourceReference `json:"targetItem"`

	// KubeContent is a single Kubernetes object (not a List) to be applied
	// with server-side-apply and Force=true. The object must carry apiVersion,
	// kind, metadata.name, and metadata.namespace if namespaced.
	//
	// A nil pointer (or one with an empty Raw) is treated as a pre-check
	// failure: the kube-applier needs an object to apply.
	//
	// The kube-applier always issues SSA with FieldManager="aro-hcp-kube-applier".
	// The manager name is intentionally not configurable via this API: every
	// field the kube-applier owns on the cluster traces to that one string,
	// so an operator inspecting fieldsV1 metadata can attribute ownership at
	// a glance.
	KubeContent *runtime.RawExtension `json:"kubeContent,omitempty"`
}

type ApplyDesireStatus struct {
	// Conditions reports per-desire reconciliation status. Well-known types:
	//   - "Successful": the SSA succeeded.
	//   - "Degraded":   the controller is not making progress for an
	//                   out-of-band reason.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
