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

	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
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
	c := findCondition(conds, kubeapplierapi.ConditionTypeSuccessful)
	if c == nil {
		t.Fatal("Successful condition not set")
	}
	if c.Status != metav1.ConditionTrue {
		t.Errorf("Status = %v, want True", c.Status)
	}
	if c.Reason != kubeapplierapi.ConditionReasonNoErrors {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplierapi.ConditionReasonNoErrors)
	}
}

func TestSetSuccessful_PreCheckErrorReason(t *testing.T) {
	var conds []metav1.Condition
	SetSuccessful(&conds, NewPreCheckError(errors.New("malformed input")))
	c := findCondition(conds, kubeapplierapi.ConditionTypeSuccessful)
	if c == nil {
		t.Fatal("Successful condition not set")
	}
	if c.Status != metav1.ConditionFalse {
		t.Errorf("Status = %v, want False", c.Status)
	}
	if c.Reason != kubeapplierapi.ConditionReasonPreCheckFailed {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplierapi.ConditionReasonPreCheckFailed)
	}
	if c.Message != "malformed input" {
		t.Errorf("Message = %q, want %q", c.Message, "malformed input")
	}
}

func TestSetSuccessful_RegularErrorIsKubeAPIError(t *testing.T) {
	var conds []metav1.Condition
	SetSuccessful(&conds, errors.New("503 from apiserver"))
	c := findCondition(conds, kubeapplierapi.ConditionTypeSuccessful)
	if c == nil {
		t.Fatal("Successful condition not set")
	}
	if c.Reason != kubeapplierapi.ConditionReasonKubeAPIError {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplierapi.ConditionReasonKubeAPIError)
	}
}

// SetSuccessfullyApplied writes both the operation-specific SuccessfullyApplied
// condition and the legacy Successful condition with identical status/reason.
func TestSetSuccessfullyApplied_WritesBothConditions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{"success", nil, metav1.ConditionTrue, kubeapplierapi.ConditionReasonNoErrors},
		{"precheck", NewPreCheckError(errors.New("bad")), metav1.ConditionFalse, kubeapplierapi.ConditionReasonPreCheckFailed},
		{"kubeapi", errors.New("503"), metav1.ConditionFalse, kubeapplierapi.ConditionReasonKubeAPIError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var conds []metav1.Condition
			SetSuccessfullyApplied(&conds, tc.err)
			for _, condType := range []string{kubeapplierapi.ConditionTypeSuccessfullyApplied, kubeapplierapi.ConditionTypeSuccessful} {
				c := findCondition(conds, condType)
				if c == nil {
					t.Fatalf("%s condition not set", condType)
				}
				if c.Status != tc.wantStatus {
					t.Errorf("%s Status = %v, want %v", condType, c.Status, tc.wantStatus)
				}
				if c.Reason != tc.wantReason {
					t.Errorf("%s Reason = %q, want %q", condType, c.Reason, tc.wantReason)
				}
			}
			if findCondition(conds, kubeapplierapi.ConditionTypeSuccessfullyDeleted) != nil {
				t.Error("SuccessfullyDeleted should not be set by SetSuccessfullyApplied")
			}
		})
	}
}

// SetSuccessfullyDeleted writes both the operation-specific SuccessfullyDeleted
// condition and the legacy Successful condition.
func TestSetSuccessfullyDeleted_WritesBothConditions(t *testing.T) {
	var conds []metav1.Condition
	SetSuccessfullyDeleted(&conds, nil)
	for _, condType := range []string{kubeapplierapi.ConditionTypeSuccessfullyDeleted, kubeapplierapi.ConditionTypeSuccessful} {
		c := findCondition(conds, condType)
		if c == nil {
			t.Fatalf("%s condition not set", condType)
		}
		if c.Status != metav1.ConditionTrue {
			t.Errorf("%s Status = %v, want True", condType, c.Status)
		}
	}
	if findCondition(conds, kubeapplierapi.ConditionTypeSuccessfullyApplied) != nil {
		t.Error("SuccessfullyApplied should not be set by SetSuccessfullyDeleted")
	}
}

func TestSetWaitingForDeletion_WritesBothConditions(t *testing.T) {
	var conds []metav1.Condition
	dt := metav1.NewTime(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	uid := types.UID("abc-123")
	SetWaitingForDeletion(&conds, dt, uid)
	for _, condType := range []string{kubeapplierapi.ConditionTypeSuccessfullyDeleted, kubeapplierapi.ConditionTypeSuccessful} {
		c := findCondition(conds, condType)
		if c == nil {
			t.Fatalf("%s condition not set", condType)
		}
		if c.Status != metav1.ConditionFalse {
			t.Errorf("%s Status = %v, want False", condType, c.Status)
		}
		if c.Reason != kubeapplierapi.ConditionReasonWaitingForDeletion {
			t.Errorf("%s Reason = %q, want %q", condType, c.Reason, kubeapplierapi.ConditionReasonWaitingForDeletion)
		}
		if !contains(c.Message, "abc-123") {
			t.Errorf("%s Message = %q does not contain UID", condType, c.Message)
		}
		if !contains(c.Message, "2026-05-01T12:00:00Z") {
			t.Errorf("%s Message = %q does not contain RFC3339 deletionTimestamp", condType, c.Message)
		}
	}
}

func TestSetDegraded(t *testing.T) {
	var conds []metav1.Condition
	SetDegraded(&conds, errors.New("control loop wedged"))
	c := findCondition(conds, kubeapplierapi.ConditionTypeDegraded)
	if c == nil {
		t.Fatal("Degraded not set")
	}
	if c.Status != metav1.ConditionTrue {
		t.Errorf("Status = %v, want True", c.Status)
	}
	if c.Reason != kubeapplierapi.ConditionReasonFailed {
		t.Errorf("Reason = %q, want %q", c.Reason, kubeapplierapi.ConditionReasonFailed)
	}
	SetDegraded(&conds, nil)
	c = findCondition(conds, kubeapplierapi.ConditionTypeDegraded)
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
