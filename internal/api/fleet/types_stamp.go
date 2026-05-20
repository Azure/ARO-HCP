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

package fleet

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

// StampConditionType represents the type of a stamp condition.
type StampConditionType string

// StampConditionReason represents the reason for a stamp condition.
type StampConditionReason string

const (
	// StampConditionApproved indicates whether the stamp has been approved
	// for promotion to a ManagementCluster.
	StampConditionApproved StampConditionType = "Approved"

	// StampConditionReasonAutoApproved indicates the stamp was automatically
	// approved (non-production environments).
	StampConditionReasonAutoApproved StampConditionReason = "AutoApproved"

	// StampConditionReasonManuallyApproved indicates the stamp was approved
	// by an SRE via the admin API.
	StampConditionReasonManuallyApproved StampConditionReason = "ManuallyApproved"

	// StampConditionReasonApprovalRevoked indicates approval was revoked
	// via the admin API.
	StampConditionReasonApprovalRevoked StampConditionReason = "ApprovalRevoked"
)

// Stamp is the parent scope for management cluster resources, analogous to
// a CAPI Machine. It represents provisioning intent and lifecycle state.
// The ManagementCluster sub-resource is the Node — the operational record
// that the RP consumes.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Stamp struct {
	api.CosmosMetadata `json:"cosmosMetadata"`

	// ResourceID exists to match cosmosMetadata.resourceID until we're able to transition all types to use cosmosMetadata,
	// at which point we will stop using properties.resourceId in our queries.
	// Example: "/providers/microsoft.redhatopenshift/stamps/1"
	//
	// +required, immutable once set.
	ResourceID *azcorearm.ResourceID `json:"resourceId,omitempty"`

	Spec   StampSpec   `json:"spec"`
	Status StampStatus `json:"status"`
}

// StampSpec contains the desired state of a stamp.
// Reserved for future provisioning intent (constraints, features, sizing).
type StampSpec struct{}

// StampStatus contains the observed state of a stamp.
type StampStatus struct {
	// Conditions tracks the stamp's lifecycle progression.
	// Known condition types: Approved.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
