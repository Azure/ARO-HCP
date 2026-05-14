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

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

// DeleteDesire targets a single Kubernetes object on the management cluster
// for deletion. Each DeleteDesire targets exactly one kube object — there
// is no list form.
//
// Deleting a DeleteDesire from Cosmos has no effect on the kube object that
// was (or was not) deleted. Removing the desire only stops further
// reconciliation; the underlying kube state is unchanged.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DeleteDesire struct {
	// CosmosMetadata.ResourceID is nested under an HCPOpenShiftCluster (and
	// optionally a NodePool) so that listing the partition by parent prefix
	// naturally returns the desires associated with that resource — and so
	// that cluster/nodepool deletion can sweep them.
	resourcesapi.CosmosMetadata `json:"cosmosMetadata"`

	Spec DeleteDesireSpec `json:"spec"`

	Status DeleteDesireStatus `json:"status"`
}

type DeleteDesireSpec struct {
	// ManagementCluster names the management cluster whose kube-applier should
	// reconcile this desire. It is the cosmos partition key for the
	// kube-applier container; entries from one management cluster never see
	// entries from another.
	// TODO this may end up changing to be a resourceID
	ManagementCluster string `json:"managementCluster"`

	// TargetItem identifies the single kube object to delete. The
	// kube-applier will issue a delete and then wait for the item to actually
	// disappear, so finalizer-driven removal is reflected in
	// .status.conditions["Successful"] rather than the bare delete-call result.
	TargetItem ResourceReference `json:"targetItem,omitempty"`
}

type DeleteDesireStatus struct {
	// Conditions reports per-desire reconciliation status. Well-known types:
	//   - "Successful": the target is gone. While finalizers are running this
	//                   stays False with reason "WaitingForDeletion".
	//   - "Degraded":   the controller is not making progress for an
	//                   out-of-band reason.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
