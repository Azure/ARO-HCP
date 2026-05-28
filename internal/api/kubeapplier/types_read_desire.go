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

// ReadDesire indicates a kube item in .spec.targetItem to issue a list/watch+informer for.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ReadDesire struct {
	// CosmosMetadata.ResourceID is nested under an HCPOpenShiftCluster (and
	// optionally a NodePool) so that listing the partition by parent prefix
	// naturally returns the desires associated with that resource — and so
	// that cluster/nodepool deletion can sweep them.
	// PartitionKey holds the lowercased Spec.ManagementCluster resource ID
	// (e.g. "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default")
	// so that all desires for one management cluster live together in one Cosmos
	// partition (and one container, in the per-MC container model).
	api.CosmosMetadata `json:"cosmosMetadata"`

	Spec ReadDesireSpec `json:"spec"`

	Status ReadDesireStatus `json:"status"`
}

type ReadDesireSpec struct {
	// ManagementCluster names the management cluster whose kube-applier should
	// reconcile this desire. It is the cosmos partition key for the
	// kube-applier container; entries from one management cluster never see
	// entries from another.
	// This is the same resourceID as the fleetapi.ManagementCluster.CosmosMetadata.ResourceID
	// Example: "/providers/microsoft.redhatopenshift/stamps/1/managementclusters/default"
	ManagementCluster *azcorearm.ResourceID `json:"managementCluster"`

	// TargetItem identifies the single kube object to read. The kube-applier
	// runs a per-instance list/watch+informer scoped to this exact name and
	// mirrors the live object into .status.kubeContent. Refresh frequency is
	// not contractual, and .status carries no explicit observed-at timestamp.
	TargetItem ResourceReference `json:"targetItem,omitempty"`
}

type ReadDesireStatus struct {
	// Conditions reports per-desire reconciliation status. Well-known types:
	//   - "Successful": the informer has synced and KubeContent reflects
	//                   the last observation.
	//   - "Degraded":   the controller is not making progress for an
	//                   out-of-band reason.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// KubeContent holds the most recently observed state of TargetItem.
	// nil means the target either has not been observed yet or is absent
	// from the cluster — distinguish those two via the "Successful"
	// condition (Unknown vs. True).
	KubeContent *runtime.RawExtension `json:"kubeContent,omitempty"`
}
