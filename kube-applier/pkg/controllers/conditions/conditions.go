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

// Package conditions provides typed setters for the well-known
// kube-applier *Desire conditions (SuccessfullyApplied, SuccessfullyDeleted,
// the legacy Successful, and Degraded).
//
// All setters go through meta.SetStatusCondition, which preserves
// LastTransitionTime when the condition's Status, Reason, and Message are
// unchanged.
package conditions

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
)

// PreCheckError is an error type controllers raise when they cannot even reach
// the kube-apiserver — typically a malformed spec, a GVR that does not resolve,
// or a namespace mismatch. SetSuccessful uses this to pick the
// "PreCheckFailed" reason instead of "KubeAPIError".
type PreCheckError struct {
	Err error
}

func (e *PreCheckError) Error() string { return e.Err.Error() }
func (e *PreCheckError) Unwrap() error { return e.Err }

// NewPreCheckError wraps err so that SetSuccessful classifies it as a
// pre-check failure rather than a kube-apiserver call failure.
func NewPreCheckError(err error) error { return &PreCheckError{Err: err} }

// setResult records the result of a single sync attempt under condType.
// err == nil means the desired effect was achieved.
//   - nil err          -> condType=True, reason=NoErrors
//   - *PreCheckError   -> condType=False, reason=PreCheckFailed
//   - any other err    -> condType=False, reason=KubeAPIError
func setResult(conds *[]metav1.Condition, condType string, err error) {
	if err == nil {
		meta.SetStatusCondition(conds, metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionTrue,
			Reason:  kubeapplierapi.ConditionReasonNoErrors,
			Message: "As expected.",
		})
		return
	}
	reason := kubeapplierapi.ConditionReasonKubeAPIError
	if _, ok := err.(*PreCheckError); ok {
		reason = kubeapplierapi.ConditionReasonPreCheckFailed
	}
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:    condType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
}

// SetSuccessful records the result of a single sync attempt under the legacy
// ConditionTypeSuccessful. It is used for ReadDesire, whose single observe
// operation has no operation-specific condition. ApplyDesire controllers use
// SetSuccessfullyApplied / SetSuccessfullyDeleted instead.
func SetSuccessful(conds *[]metav1.Condition, err error) {
	setResult(conds, kubeapplierapi.ConditionTypeSuccessful, err)
}

// SetSuccessfullyApplied records the result of an ApplyDesire Type=ServerSideApply
// sync attempt. It writes both the operation-specific ConditionTypeSuccessfullyApplied
// and the legacy ConditionTypeSuccessful (retained for backwards compatibility)
// with the same status/reason/message.
func SetSuccessfullyApplied(conds *[]metav1.Condition, err error) {
	setResult(conds, kubeapplierapi.ConditionTypeSuccessfullyApplied, err)
	setResult(conds, kubeapplierapi.ConditionTypeSuccessful, err)
}

// SetSuccessfullyDeleted records the result of an ApplyDesire Type=Delete sync
// attempt. It writes both the operation-specific ConditionTypeSuccessfullyDeleted
// and the legacy ConditionTypeSuccessful (retained for backwards compatibility)
// with the same status/reason/message.
func SetSuccessfullyDeleted(conds *[]metav1.Condition, err error) {
	setResult(conds, kubeapplierapi.ConditionTypeSuccessfullyDeleted, err)
	setResult(conds, kubeapplierapi.ConditionTypeSuccessful, err)
}

// SetWaitingForDeletion records the "deletion is in flight" state for an
// ApplyDesire with Type=Delete whose target still exists in the cluster. The
// deletion timestamp and UID are surfaced verbatim in the message so consumers
// can correlate without an extra cluster read. It writes both the
// operation-specific ConditionTypeSuccessfullyDeleted and the legacy
// ConditionTypeSuccessful (retained for backwards compatibility).
func SetWaitingForDeletion(conds *[]metav1.Condition, deletionTime metav1.Time, uid types.UID) {
	message := fmt.Sprintf("waiting for deletion: deletionTimestamp=%s uid=%s",
		deletionTime.UTC().Format(time.RFC3339), uid)
	for _, condType := range []string{
		kubeapplierapi.ConditionTypeSuccessfullyDeleted,
		kubeapplierapi.ConditionTypeSuccessful,
	} {
		meta.SetStatusCondition(conds, metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionFalse,
			Reason:  kubeapplierapi.ConditionReasonWaitingForDeletion,
			Message: message,
		})
	}
}

// SetDegraded records controller-level health. Convention matches the
// existing backend controllers: nil -> NoErrors/False, non-nil -> Failed/True.
func SetDegraded(conds *[]metav1.Condition, err error) {
	if err == nil {
		meta.SetStatusCondition(conds, metav1.Condition{
			Type:    kubeapplierapi.ConditionTypeDegraded,
			Status:  metav1.ConditionFalse,
			Reason:  kubeapplierapi.ConditionReasonNoErrors,
			Message: "As expected.",
		})
		return
	}
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:    kubeapplierapi.ConditionTypeDegraded,
		Status:  metav1.ConditionTrue,
		Reason:  kubeapplierapi.ConditionReasonFailed,
		Message: fmt.Sprintf("Had an error while syncing: %s", err.Error()),
	})
}
