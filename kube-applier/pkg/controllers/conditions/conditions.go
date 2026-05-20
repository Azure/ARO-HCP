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
// kube-applier *Desire conditions (Successful, Degraded).
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

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
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

// SetSuccessful records the result of a single sync attempt on the desire's
// Conditions slice. err == nil means the desired effect was achieved.
//   - nil err          -> Successful=True, reason=NoErrors
//   - *PreCheckError   -> Successful=False, reason=PreCheckFailed
//   - any other err    -> Successful=False, reason=KubeAPIError
func SetSuccessful(conds *[]metav1.Condition, err error) {
	if err == nil {
		meta.SetStatusCondition(conds, metav1.Condition{
			Type:    kubeapplier.ConditionTypeSuccessful,
			Status:  metav1.ConditionTrue,
			Reason:  kubeapplier.ConditionReasonNoErrors,
			Message: "As expected.",
		})
		return
	}
	reason := kubeapplier.ConditionReasonKubeAPIError
	if _, ok := err.(*PreCheckError); ok {
		reason = kubeapplier.ConditionReasonPreCheckFailed
	}
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:    kubeapplier.ConditionTypeSuccessful,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
}

// SetSuccessfulWaitingForDeletion records the "deletion is in flight"
// state for a DeleteDesire whose target still exists in the cluster.
// The deletion timestamp and UID are surfaced verbatim in the message
// so consumers can correlate without an extra cluster read.
func SetSuccessfulWaitingForDeletion(conds *[]metav1.Condition, deletionTime metav1.Time, uid types.UID) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:   kubeapplier.ConditionTypeSuccessful,
		Status: metav1.ConditionFalse,
		Reason: kubeapplier.ConditionReasonWaitingForDeletion,
		Message: fmt.Sprintf("waiting for deletion: deletionTimestamp=%s uid=%s",
			deletionTime.UTC().Format(time.RFC3339), uid),
	})
}

// SetDegraded records controller-level health. Convention matches the
// existing backend controllers: nil -> NoErrors/False, non-nil -> Failed/True.
func SetDegraded(conds *[]metav1.Condition, err error) {
	if err == nil {
		meta.SetStatusCondition(conds, metav1.Condition{
			Type:    kubeapplier.ConditionTypeDegraded,
			Status:  metav1.ConditionFalse,
			Reason:  kubeapplier.ConditionReasonNoErrors,
			Message: "As expected.",
		})
		return
	}
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:    kubeapplier.ConditionTypeDegraded,
		Status:  metav1.ConditionTrue,
		Reason:  kubeapplier.ConditionReasonFailed,
		Message: fmt.Sprintf("Had an error while syncing: %s", err.Error()),
	})
}
