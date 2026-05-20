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

package conditions

import (
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

func findCondition(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

func TestSetSuccessful_NilErrIsTrueWithNoErrors(t *testing.T) {
	var conds []metav1.Condition
	SetSuccessful(&conds, nil)
	c := findCondition(conds, kubeapplier.ConditionTypeSuccessful)
	if c == nil {
		t.Fatal("Successful condition not set")
	}
	if c.Status != metav1.ConditionTrue {
		t.Errorf("Status = %v, want True", c.Status)
	}
	if c.Reason != kubeapplier.ConditionReasonNoErrors {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplier.ConditionReasonNoErrors)
	}
}

func TestSetSuccessful_PreCheckErrorReason(t *testing.T) {
	var conds []metav1.Condition
	SetSuccessful(&conds, NewPreCheckError(errors.New("malformed input")))
	c := findCondition(conds, kubeapplier.ConditionTypeSuccessful)
	if c == nil {
		t.Fatal("Successful condition not set")
	}
	if c.Status != metav1.ConditionFalse {
		t.Errorf("Status = %v, want False", c.Status)
	}
	if c.Reason != kubeapplier.ConditionReasonPreCheckFailed {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplier.ConditionReasonPreCheckFailed)
	}
	if c.Message != "malformed input" {
		t.Errorf("Message = %q, want %q", c.Message, "malformed input")
	}
}

func TestSetSuccessful_RegularErrorIsKubeAPIError(t *testing.T) {
	var conds []metav1.Condition
	SetSuccessful(&conds, errors.New("503 from apiserver"))
	c := findCondition(conds, kubeapplier.ConditionTypeSuccessful)
	if c == nil {
		t.Fatal("Successful condition not set")
	}
	if c.Reason != kubeapplier.ConditionReasonKubeAPIError {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplier.ConditionReasonKubeAPIError)
	}
}

func TestSetSuccessfulWaitingForDeletion(t *testing.T) {
	var conds []metav1.Condition
	dt := metav1.NewTime(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	uid := types.UID("abc-123")
	SetSuccessfulWaitingForDeletion(&conds, dt, uid)
	c := findCondition(conds, kubeapplier.ConditionTypeSuccessful)
	if c == nil {
		t.Fatal("Successful condition not set")
	}
	if c.Status != metav1.ConditionFalse {
		t.Errorf("Status = %v, want False", c.Status)
	}
	if c.Reason != kubeapplier.ConditionReasonWaitingForDeletion {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplier.ConditionReasonWaitingForDeletion)
	}
	if !contains(c.Message, "abc-123") {
		t.Errorf("Message = %q does not contain UID", c.Message)
	}
	if !contains(c.Message, "2026-05-01T12:00:00Z") {
		t.Errorf("Message = %q does not contain RFC3339 deletionTimestamp", c.Message)
	}
}

func TestSetDegraded(t *testing.T) {
	var conds []metav1.Condition
	SetDegraded(&conds, errors.New("control loop wedged"))
	c := findCondition(conds, kubeapplier.ConditionTypeDegraded)
	if c == nil {
		t.Fatal("Degraded not set")
	}
	if c.Status != metav1.ConditionTrue {
		t.Errorf("Status = %v, want True", c.Status)
	}
	if c.Reason != kubeapplier.ConditionReasonFailed {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplier.ConditionReasonFailed)
	}
	SetDegraded(&conds, nil)
	c = findCondition(conds, kubeapplier.ConditionTypeDegraded)
	if c.Status != metav1.ConditionFalse {
		t.Errorf("Status = %v after recovery, want False", c.Status)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
