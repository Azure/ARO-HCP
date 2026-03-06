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
	"time"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestValidateSystemData(t *testing.T) {
	ctx := context.Background()
	fldPath := field.NewPath("systemData")

	now := time.Now()
	later := now.Add(time.Hour)

	tests := []struct {
		name         string
		op           operation.Operation
		newObj       *arm.SystemData
		oldObj       *arm.SystemData
		expectErrors []expectedError
	}{
		// Required field tests
		{
			name: "missing createdBy - rejected",
			op:   operation.Operation{Type: operation.Create},
			newObj: &arm.SystemData{
				CreatedBy:     "",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: nil,
			expectErrors: []expectedError{
				{fieldPath: "systemData.createdBy", message: "Required"},
			},
		},
		{
			name: "missing createdAt - rejected",
			op:   operation.Operation{Type: operation.Create},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     nil,
			},
			oldObj: nil,
			expectErrors: []expectedError{
				{fieldPath: "systemData.createdAt", message: "Required"},
			},
		},
		{
			name: "missing createdByType - rejected",
			op:   operation.Operation{Type: operation.Create},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: "",
				CreatedAt:     &now,
			},
			oldObj: nil,
			expectErrors: []expectedError{
				{fieldPath: "systemData.createdByType", message: "Required"},
			},
		},
		{
			name: "missing all created fields - rejected for all",
			op:   operation.Operation{Type: operation.Create},
			newObj: &arm.SystemData{
				CreatedBy:     "",
				CreatedByType: "",
				CreatedAt:     nil,
			},
			oldObj: nil,
			expectErrors: []expectedError{
				{fieldPath: "systemData.createdBy", message: "Required"},
				{fieldPath: "systemData.createdAt", message: "Required"},
				{fieldPath: "systemData.createdByType", message: "Required"},
			},
		},
		{
			name: "all created fields present - allowed",
			op:   operation.Operation{Type: operation.Create},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj:       nil,
			expectErrors: []expectedError{},
		},
		// Backfill tests: old value missing, new value present - should succeed
		{
			name: "old missing createdBy, new has createdBy - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "new-user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []expectedError{},
		},
		{
			name: "old missing createdAt, new has createdAt - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     nil,
			},
			expectErrors: []expectedError{},
		},
		{
			name: "old missing createdByType, new has createdByType - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: "",
				CreatedAt:     &now,
			},
			expectErrors: []expectedError{},
		},
		{
			name: "old missing all created fields, new has all - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "new-user",
				CreatedByType: arm.CreatedByTypeApplication,
				CreatedAt:     &now,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "",
				CreatedByType: "",
				CreatedAt:     nil,
			},
			expectErrors: []expectedError{},
		},
		// Immutability tests: old value present, new value different - should fail
		{
			name: "old has createdBy, new changes it - rejected",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "different-user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "original-user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []expectedError{
				{fieldPath: "systemData.createdBy", message: "immutable"},
			},
		},
		{
			name: "old has createdAt, new changes it - rejected",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &later,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []expectedError{
				{fieldPath: "systemData.createdAt", message: "immutable"},
			},
		},
		{
			name: "old has createdByType, new changes it - rejected",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeApplication,
				CreatedAt:     &now,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []expectedError{
				{fieldPath: "systemData.createdByType", message: "immutable"},
			},
		},
		{
			name: "old has all fields, new changes all - rejected for all",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "different-user",
				CreatedByType: arm.CreatedByTypeApplication,
				CreatedAt:     &later,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "original-user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []expectedError{
				{fieldPath: "systemData.createdBy", message: "immutable"},
				{fieldPath: "systemData.createdAt", message: "immutable"},
				{fieldPath: "systemData.createdByType", message: "immutable"},
			},
		},
		// No-change tests: should succeed
		{
			name: "old has all fields, new keeps same values - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []expectedError{},
		},
		{
			name: "nil oldObj with valid newObj - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &arm.SystemData{
				CreatedBy:     "user",
				CreatedByType: arm.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj:       nil,
			expectErrors: []expectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateSystemData(ctx, tt.op, fldPath, tt.newObj, tt.oldObj)
			verifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}
