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

package validation

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

func TestImmutableByCompare(t *testing.T) {
	t.Parallel()
	fldPath := field.NewPath("test")
	ctx := context.Background()

	tests := []struct {
		name      string
		op        operation.Operation
		value     *string
		oldValue  *string
		wantError bool
	}{
		{
			name:      "create is always allowed",
			op:        operation.Operation{Type: operation.Create},
			value:     ptr.To("a"),
			wantError: false,
		},
		{
			name:      "update with same value",
			op:        operation.Operation{Type: operation.Update},
			value:     ptr.To("a"),
			oldValue:  ptr.To("a"),
			wantError: false,
		},
		{
			name:      "update with different value",
			op:        operation.Operation{Type: operation.Update},
			value:     ptr.To("a"),
			oldValue:  ptr.To("b"),
			wantError: true,
		},
		{
			name:      "update both nil",
			op:        operation.Operation{Type: operation.Update},
			value:     nil,
			oldValue:  nil,
			wantError: false,
		},
		{
			name:      "update value nil old not nil",
			op:        operation.Operation{Type: operation.Update},
			value:     nil,
			oldValue:  ptr.To("a"),
			wantError: true,
		},
		{
			name:      "update value not nil old nil",
			op:        operation.Operation{Type: operation.Update},
			value:     ptr.To("a"),
			oldValue:  nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := ImmutableByCompare(ctx, tt.op, fldPath, tt.value, tt.oldValue)
			if tt.wantError && len(errs) == 0 {
				t.Error("expected error, got none")
			}
			if !tt.wantError && len(errs) > 0 {
				t.Errorf("expected no error, got %v", errs)
			}
		})
	}
}

func TestImmutableByReflect(t *testing.T) {
	t.Parallel()
	fldPath := field.NewPath("test")
	ctx := context.Background()

	type nested struct {
		Value string
	}

	tests := []struct {
		name      string
		op        operation.Operation
		value     nested
		oldValue  nested
		wantError bool
	}{
		{
			name:      "create is always allowed",
			op:        operation.Operation{Type: operation.Create},
			value:     nested{Value: "a"},
			wantError: false,
		},
		{
			name:      "update with same value",
			op:        operation.Operation{Type: operation.Update},
			value:     nested{Value: "a"},
			oldValue:  nested{Value: "a"},
			wantError: false,
		},
		{
			name:      "update with different value",
			op:        operation.Operation{Type: operation.Update},
			value:     nested{Value: "a"},
			oldValue:  nested{Value: "b"},
			wantError: true,
		},
		{
			name:      "update both zero value",
			op:        operation.Operation{Type: operation.Update},
			value:     nested{},
			oldValue:  nested{},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := ImmutableByReflect(ctx, tt.op, fldPath, tt.value, tt.oldValue)
			if tt.wantError && len(errs) == 0 {
				t.Error("expected error, got none")
			}
			if !tt.wantError && len(errs) > 0 {
				t.Errorf("expected no error, got %v", errs)
			}
		})
	}
}
