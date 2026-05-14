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

	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestValidateSystemData(t *testing.T) {
	ctx := context.Background()
	fldPath := field.NewPath("systemData")

	now := time.Now()
	later := now.Add(time.Hour)

	tests := []struct {
		name         string
		op           operation.Operation
		newObj       *armresourcesapi.SystemData
		oldObj       *armresourcesapi.SystemData
		expectErrors []utils.ExpectedError
	}{
		// Required field tests
		{
			name: "missing createdBy - rejected",
			op:   operation.Operation{Type: operation.Create},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdBy", Message: "Required"},
			},
		},
		{
			name: "missing createdAt - rejected",
			op:   operation.Operation{Type: operation.Create},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     nil,
			},
			oldObj: nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdAt", Message: "Required"},
			},
		},
		{
			name: "missing createdByType - rejected",
			op:   operation.Operation{Type: operation.Create},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: "",
				CreatedAt:     &now,
			},
			oldObj: nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdByType", Message: "Required"},
			},
		},
		{
			name: "missing all created fields - rejected for all",
			op:   operation.Operation{Type: operation.Create},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "",
				CreatedByType: "",
				CreatedAt:     nil,
			},
			oldObj: nil,
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdBy", Message: "Required"},
				{FieldPath: "systemData.createdAt", Message: "Required"},
				{FieldPath: "systemData.createdByType", Message: "Required"},
			},
		},
		{
			name: "all created fields present - allowed",
			op:   operation.Operation{Type: operation.Create},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj:       nil,
			expectErrors: []utils.ExpectedError{},
		},
		// Backfill tests: old value missing, new value present - should succeed
		{
			name: "old missing createdBy, new has createdBy - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "new-user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "old missing createdAt, new has createdAt - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     nil,
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "old missing createdByType, new has createdByType - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: "",
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "old missing all created fields, new has all - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "new-user",
				CreatedByType: armresourcesapi.CreatedByTypeApplication,
				CreatedAt:     &now,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "",
				CreatedByType: "",
				CreatedAt:     nil,
			},
			expectErrors: []utils.ExpectedError{},
		},
		// Immutability tests: old value present, new value different - should fail
		{
			name: "old has createdBy, new changes it - rejected",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "different-user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "original-user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdBy", Message: "immutable"},
			},
		},
		{
			name: "old has createdAt, new changes it - rejected",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &later,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdAt", Message: "immutable"},
			},
		},
		{
			name: "old has createdByType, new changes it - rejected",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeApplication,
				CreatedAt:     &now,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdByType", Message: "immutable"},
			},
		},
		{
			name: "old has all fields, new changes all - rejected for all",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "different-user",
				CreatedByType: armresourcesapi.CreatedByTypeApplication,
				CreatedAt:     &later,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "original-user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdBy", Message: "immutable"},
				{FieldPath: "systemData.createdAt", Message: "immutable"},
				{FieldPath: "systemData.createdByType", Message: "immutable"},
			},
		},
		// No-change tests: should succeed
		{
			name: "old has all fields, new keeps same values - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "nil oldObj with valid newObj - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &armresourcesapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: armresourcesapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj:       nil,
			expectErrors: []utils.ExpectedError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateSystemData(ctx, tt.op, fldPath, tt.newObj, tt.oldObj)
			utils.VerifyErrorsMatch(t, tt.expectErrors, errs)
		})
	}
}
