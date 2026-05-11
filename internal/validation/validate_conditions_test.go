// Copyright 2025 Microsoft Corporation
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

package validation

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api"
)

func validCondition() api.Condition {
	return api.Condition{
		Type:               api.ConditionTypeAvailable,
		Status:             api.ConditionStatusTypeTrue,
		LastTransitionTime: time.Now(),
		Reason:             "AsExpected",
		Message:            "All components are running and healthy.",
	}
}

func TestValidateCondition(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("conditions")

	tests := []struct {
		name    string
		modify  func(*api.Condition)
		wantErr string
	}{
		{
			name:   "valid condition",
			modify: func(c *api.Condition) {},
		},
		{
			name:    "invalid type",
			modify:  func(c *api.Condition) { c.Type = "NotAValidType" },
			wantErr: "Unsupported value",
		},
		{
			name:    "type exceeds max length",
			modify:  func(c *api.Condition) { c.Type = api.ConditionType(strings.Repeat("a", conditionTypeMaxLength+1)) },
			wantErr: "Unsupported value",
		},
		{
			name:    "invalid status",
			modify:  func(c *api.Condition) { c.Status = "Maybe" },
			wantErr: "Unsupported value",
		},
		{
			name:    "zero lastTransitionTime",
			modify:  func(c *api.Condition) { c.LastTransitionTime = time.Time{} },
			wantErr: "Required value",
		},
		{
			name:    "empty reason",
			modify:  func(c *api.Condition) { c.Reason = "" },
			wantErr: "reason",
		},
		{
			name:    "reason exceeds max length",
			modify:  func(c *api.Condition) { c.Reason = strings.Repeat("A", conditionReasonMaxLength+1) },
			wantErr: "reason",
		},
		{
			name:    "reason with invalid characters",
			modify:  func(c *api.Condition) { c.Reason = "has spaces" },
			wantErr: "reason",
		},
		{
			name:    "reason starting with number",
			modify:  func(c *api.Condition) { c.Reason = "1BadStart" },
			wantErr: "reason",
		},
		{
			name:   "reason with allowed special chars",
			modify: func(c *api.Condition) { c.Reason = "Some_Reason,With:Chars_" },
		},
		{
			name:    "message exceeds max length",
			modify:  func(c *api.Condition) { c.Message = strings.Repeat("a", conditionMessageMaxLength+1) },
			wantErr: "message",
		},
		{
			name:   "empty message is valid",
			modify: func(c *api.Condition) { c.Message = "" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCondition()
			tt.modify(&c)
			errs := validateCondition(ctx, op, fldPath.Index(0), &c, nil)
			if tt.wantErr == "" {
				if len(errs) > 0 {
					t.Errorf("expected no errors, got: %v", errs)
				}
			} else {
				if len(errs) == 0 {
					t.Errorf("expected error containing %q, got none", tt.wantErr)
				} else {
					found := false
					for _, e := range errs {
						if strings.Contains(e.Error(), tt.wantErr) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got: %v", tt.wantErr, errs)
					}
				}
			}
		})
	}
}

func TestValidateConditions(t *testing.T) {
	ctx := context.Background()
	op := operation.Operation{Type: operation.Create}
	fldPath := field.NewPath("conditions")

	tests := []struct {
		name       string
		conditions []api.Condition
		wantErr    string
	}{
		{
			name:       "nil conditions",
			conditions: nil,
		},
		{
			name:       "empty conditions",
			conditions: []api.Condition{},
		},
		{
			name:       "single valid condition",
			conditions: []api.Condition{validCondition()},
		},
		{
			name: "multiple valid conditions with different types",
			conditions: []api.Condition{
				func() api.Condition { c := validCondition(); c.Type = api.ConditionTypeAvailable; return c }(),
				func() api.Condition { c := validCondition(); c.Type = api.ConditionTypeDegraded; return c }(),
				func() api.Condition { c := validCondition(); c.Type = api.ConditionTypeProgressing; return c }(),
			},
		},
		{
			name: "duplicate condition types",
			conditions: []api.Condition{
				func() api.Condition { c := validCondition(); c.Type = api.ConditionTypeAvailable; return c }(),
				func() api.Condition { c := validCondition(); c.Type = api.ConditionTypeAvailable; return c }(),
			},
			wantErr: "Duplicate value",
		},
		{
			name: "exceeds max items",
			conditions: func() []api.Condition {
				out := make([]api.Condition, conditionMaxItems+1)
				for i := range out {
					out[i] = validCondition()
				}
				return out
			}(),
			wantErr: "Too many",
		},
		{
			name: "invalid condition in slice",
			conditions: []api.Condition{
				func() api.Condition { c := validCondition(); c.Reason = ""; return c }(),
			},
			wantErr: "conditions[0].reason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateConditions(ctx, op, fldPath, tt.conditions)
			if tt.wantErr == "" {
				if len(errs) > 0 {
					t.Errorf("expected no errors, got: %v", errs)
				}
			} else {
				if len(errs) == 0 {
					t.Errorf("expected error containing %q, got none", tt.wantErr)
				} else {
					found := false
					for _, e := range errs {
						if strings.Contains(e.Error(), tt.wantErr) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got: %v", tt.wantErr, errs)
					}
				}
			}
		})
	}
}
