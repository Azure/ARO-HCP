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
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
)

const (
	StackDesireActionApply                  = "Apply"
	StackDesireActionDelete                 = "Delete"
	StackDesireAuthenticationTypeFPA        = "FPA"
	StackDesireAuthenticationTypeSMI        = "SMI"
	StackDesireConditionSuccessfullyApplied = "SuccessfullyApplied"
	StackDesireConditionSuccessfullyDeleted = "SuccessfullyDeleted"
	StackDesireConditionDegraded            = "Degraded"
)

// StackDesire manages an ARM deployment stack at any Azure scope. The desire
// itself is rooted under an HCP cluster. Purging it stops management; deleting
// the Azure stack requires an explicit Delete action.
// Producers must keep the target immutable and must not publish competing desires
// for the same Azure stack. These are producer contracts, not enforced by storage.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type StackDesire struct {
	// CosmosMetadata identifies the stored desire and its owning HCP cluster.
	// PartitionKey must be set to the lowercased subscription ID of the owning
	// HCP cluster before creation, independently of the Azure target's scope.
	// Written by: ClusterRoleAssignments (resource identity and partition), Cosmos storage (persistence metadata)
	CosmosMetadata `json:"cosmosMetadata"`
	// Spec describes the Azure stack operation the producer wants reconciled.
	// Written by: ClusterRoleAssignments
	Spec StackDesireSpec `json:"spec"`
	// Status records the outcome of the controller's latest observation or attempt.
	// Written by: StackDesires
	Status StackDesireStatus `json:"status,omitempty"`
}

type StackDesireSpec struct {
	// TargetResourceID is the full Azure deployment stack ID, including its scope.
	// Producers must not change it for the lifetime of this desire.
	// Written by: ClusterRoleAssignments
	TargetResourceID *azcorearm.ResourceID `json:"targetResourceID"`
	// Authentication selects the identity used to manage the target stack.
	// Written by: ClusterRoleAssignments
	Authentication StackDesireAuthentication `json:"authentication"`
	// Action selects Apply to create or update the stack, or Delete to remove it.
	// Written by: ClusterRoleAssignments
	Action string `json:"action"`
	// Stack describes the deployment stack for both Apply and Delete. Apply sends
	// the stack to Azure; Delete uses Properties.ActionOnUnmanage and
	// Properties.BypassStackOutOfSyncError as its deletion policy.
	// Written by: ClusterRoleAssignments
	Stack *armdeploymentstacks.DeploymentStack `json:"stack"`
	// ReconcileInterval is the cooldown before reconciling unchanged intent again
	// after success or an invalid-intent outcome. Non-positive values select the
	// controller's six-hour default; pending operations and transient failures use
	// separate polling and retry delays.
	// Written by: ClusterRoleAssignments
	ReconcileInterval metav1.Duration `json:"reconcileInterval,omitempty"`
}

type StackDesireAuthentication struct {
	// Type selects first-party application (FPA) or service managed identity (SMI) credentials.
	// Written by: ClusterRoleAssignments
	Type string `json:"type"`
	// TenantID is the Azure tenant in which the FPA obtains credentials.
	// Written by: ClusterRoleAssignments
	TenantID string `json:"tenantID,omitempty"`
	// SMIResourceID identifies the service managed identity used for SMI authentication.
	// Written by: StackDesire producer (unused by ClusterRoleAssignments)
	SMIResourceID *azcorearm.ResourceID `json:"smiResourceID,omitempty"`
	// ClusterIdentityURL is the cluster identity endpoint used to obtain SMI credentials.
	// Written by: StackDesire producer (unused by ClusterRoleAssignments)
	ClusterIdentityURL string `json:"clusterIdentityURL,omitempty"`
}

type StackDesireStatus struct {
	// ObservedSpecHash identifies the whole spec associated with this outcome, using
	// StackDesireSpecHash. It is not a record of historical success.
	// Written by: StackDesires
	ObservedSpecHash string `json:"observedSpecHash,omitempty"`
	// ObservedAt is the submission/outcome clock used for reconciliation cooldowns;
	// nil means no outcome has been recorded. New submissions and changed outcomes
	// advance it, but unchanged observations and metadata-only changes to completed
	// outcomes do not restart the clock.
	// Written by: StackDesires
	ObservedAt *metav1.Time `json:"observedAt,omitempty"`
	// Stack is the Azure stack returned for this outcome, without an SDK response
	// envelope. It is nil when no stack was returned, including confirmed absence.
	// Azure's operation correlation ID is available in Stack.Properties.CorrelationID.
	// Written by: StackDesires
	Stack *armdeploymentstacks.DeploymentStack `json:"stack,omitempty"`
	// Error contains Azure error details for this outcome, when available; nil means
	// no Azure error details were returned, not necessarily successful reconciliation.
	// Written by: StackDesires
	Error *armdeploymentstacks.ErrorDetail `json:"error,omitempty"`
	// Conditions summarize this outcome: SuccessfullyApplied means Apply completed,
	// SuccessfullyDeleted means Delete confirmed absence, and Degraded reports an
	// execution failure. Invalid intent is not degraded. Consumers must also match
	// ObservedSpecHash to their intent.
	// Written by: StackDesires
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// StackDesireSpecHash hashes the whole spec after canonicalizing JSON object
// keys and whitespace. Array order and JSON number representations are retained.
func StackDesireSpecHash(spec StackDesireSpec) (string, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal stack desire spec: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode stack desire spec: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize stack desire spec: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(canonical)), nil
}

// DeepCopyInto copies the spec, including arbitrary JSON in the Azure SDK stack.
// The receiver must be non-nil. Stack data must be JSON-serializable for persistence.
func (in *StackDesireSpec) DeepCopyInto(out *StackDesireSpec) {
	*out = *in
	out.TargetResourceID = DeepCopyResourceID(in.TargetResourceID)
	in.Authentication.DeepCopyInto(&out.Authentication)
	out.Stack = deepCopyStackDesireSDKValue(in.Stack)
}

// DeepCopyInto copies the status, including arbitrary JSON in Azure SDK outcomes.
// The receiver must be non-nil. SDK outcomes must be JSON-serializable, as required
// for persistence; invalid in-memory outcomes panic rather than alias mutable data.
func (in *StackDesireStatus) DeepCopyInto(out *StackDesireStatus) {
	*out = *in
	out.ObservedAt = in.ObservedAt.DeepCopy()
	out.Stack = deepCopyStackDesireSDKValue(in.Stack)
	out.Error = deepCopyStackDesireSDKValue(in.Error)
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

// Use the SDK's JSON codecs to copy nested pointers and arbitrary template, output,
// and error data with the same representation used by Cosmos persistence.
func deepCopyStackDesireSDKValue[T any](in *T) *T {
	if in == nil {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		panic(fmt.Errorf("marshal stack desire SDK value for deep copy: %w", err))
	}
	out := new(T)
	if err := json.Unmarshal(data, out); err != nil {
		panic(fmt.Errorf("unmarshal stack desire SDK value for deep copy: %w", err))
	}
	return out
}

var (
	_ runtime.Object            = &StackDesire{}
	_ metav1.ObjectMetaAccessor = &StackDesire{}
)

func (o *StackDesire) GetObjectKind() schema.ObjectKind { return schema.EmptyObjectKind }

func (o *StackDesire) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{ResourceVersion: strconv.FormatInt(o.InstanceVersion, 10)}
	if o.ResourceID != nil {
		om.Name = strings.ToLower(o.ResourceID.String())
	}
	return om
}

// StackDesireList is compatible with Kubernetes informer machinery.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type StackDesireList struct {
	// TypeMeta identifies the API type of this list.
	// Written by: StackDesire informer list/watch
	metav1.TypeMeta `json:",inline"`
	// ListMeta carries list resource version and pagination metadata.
	// Written by: StackDesire informer list/watch
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the stack desires returned by the list operation.
	// Written by: StackDesire informer list/watch
	Items []StackDesire `json:"items"`
}
