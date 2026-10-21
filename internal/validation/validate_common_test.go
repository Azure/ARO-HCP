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
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestBuildValidationOptions(testContext *testing.T) {
	tests := []struct {
		name       string
		features   []coreapi.Feature
		apiVersion metadataapi.APIVersion
		want       map[string]bool
	}{
		{name: "empty", want: map[string]bool{}},
		{
			name: "duplicate feature registered then unregistered",
			features: []coreapi.Feature{
				{Name: ptr.To("DuplicateFeature"), State: ptr.To("Registered")},
				{Name: ptr.To("DUPLICATEFEATURE"), State: ptr.To("Unregistered")},
				{Name: ptr.To("UnregisteredFeature"), State: ptr.To("Unregistered")},
			},
			want: map[string]bool{"duplicatefeature": true, "unregisteredfeature": false},
		},
		{
			name: "duplicate feature unregistered then registered",
			features: []coreapi.Feature{
				{Name: ptr.To("DuplicateFeature"), State: ptr.To("Unregistered")},
				{Name: ptr.To("DUPLICATEFEATURE"), State: ptr.To("Registered")},
				{Name: ptr.To("UnregisteredFeature"), State: ptr.To("Unregistered")},
			},
			want: map[string]bool{"duplicatefeature": true, "unregisteredfeature": false},
		},
		{
			name: "registered and unregistered features and API version",
			features: []coreapi.Feature{
				{Name: ptr.To("EnabledFeature"), State: ptr.To("Registered")},
				{Name: ptr.To("ENABLEDFEATURE"), State: ptr.To("Registered")},
				{Name: ptr.To("PendingFeature"), State: ptr.To("Pending")},
				{Name: ptr.To("UnregisteredFeature"), State: ptr.To("Unregistered")},
				{Name: ptr.To("MissingStateFeature")},
				{Name: ptr.To("EmptyStateFeature"), State: ptr.To("")},
				{State: ptr.To("Registered")},
				{Name: ptr.To(""), State: ptr.To("Registered")},
			},
			apiVersion: metadataapi.APIVersionV20260630Preview,
			want: map[string]bool{
				"enabledfeature":      true,
				"pendingfeature":      false,
				"unregisteredfeature": false,
				"missingstatefeature": false,
				"emptystatefeature":   false,
				metadataapi.APIVersionOption(metadataapi.APIVersionV20260630Preview): true,
			},
		},
	}
	for _, test := range tests {
		testContext.Run(test.name, func(testContext *testing.T) {
			if got := BuildValidationOptions(test.features, test.apiVersion); !reflect.DeepEqual(got, test.want) {
				testContext.Errorf("BuildValidationOptions() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestValidateVersionProfileDisabledOption(testContext *testing.T) {
	profile := &coreapi.VersionProfile{ID: "4.20", ChannelGroup: metadataapi.ChannelGroupNightly}
	path := field.NewPath("version")
	missing := validateVersionProfile(testContext.Context(), operation.Operation{Type: operation.Create}, path, profile, nil)
	disabled := validateVersionProfile(testContext.Context(), operation.Operation{
		Type:    operation.Create,
		Options: map[string]bool{metadataapi.FeatureExperimentalReleaseFeatures: false},
	}, path, profile, nil)
	if len(missing) == 0 || !reflect.DeepEqual(missing, disabled) {
		testContext.Errorf("missing and disabled experimental features must reject identically: missing=%v disabled=%v", missing, disabled)
	}
	enabled := validateVersionProfile(testContext.Context(), operation.Operation{
		Type:    operation.Create,
		Options: map[string]bool{metadataapi.FeatureExperimentalReleaseFeatures: true},
	}, path, profile, nil)
	if len(enabled) != 0 {
		testContext.Errorf("enabled experimental features must allow the nightly channel group: %v", enabled)
	}
}

func TestValidateSystemData(t *testing.T) {
	ctx := context.Background()
	fldPath := field.NewPath("systemData")

	now := time.Now()
	later := now.Add(time.Hour)

	tests := []struct {
		name         string
		op           operation.Operation
		newObj       *coreapi.SystemData
		oldObj       *coreapi.SystemData
		expectErrors []utils.ExpectedError
	}{
		// Required field tests
		{
			name: "missing createdBy - rejected",
			op:   operation.Operation{Type: operation.Create},
			newObj: &coreapi.SystemData{
				CreatedBy:     "",
				CreatedByType: coreapi.CreatedByTypeUser,
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
			newObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
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
			newObj: &coreapi.SystemData{
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
			newObj: &coreapi.SystemData{
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
			newObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj:       nil,
			expectErrors: []utils.ExpectedError{},
		},
		// Backfill tests: old value missing, new value present - should succeed
		{
			name: "old missing createdBy, new has createdBy - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &coreapi.SystemData{
				CreatedBy:     "new-user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &coreapi.SystemData{
				CreatedBy:     "",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "old missing createdAt, new has createdAt - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     nil,
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "old missing createdByType, new has createdByType - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: "",
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "old missing all created fields, new has all - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &coreapi.SystemData{
				CreatedBy:     "new-user",
				CreatedByType: coreapi.CreatedByTypeApplication,
				CreatedAt:     &now,
			},
			oldObj: &coreapi.SystemData{
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
			newObj: &coreapi.SystemData{
				CreatedBy:     "different-user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &coreapi.SystemData{
				CreatedBy:     "original-user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdBy", Message: "immutable"},
			},
		},
		{
			name: "old has createdAt, new changes it - rejected",
			op:   operation.Operation{Type: operation.Update},
			newObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &later,
			},
			oldObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdAt", Message: "immutable"},
			},
		},
		{
			name: "old has createdByType, new changes it - rejected",
			op:   operation.Operation{Type: operation.Update},
			newObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeApplication,
				CreatedAt:     &now,
			},
			oldObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{
				{FieldPath: "systemData.createdByType", Message: "immutable"},
			},
		},
		{
			name: "old has all fields, new changes all - rejected for all",
			op:   operation.Operation{Type: operation.Update},
			newObj: &coreapi.SystemData{
				CreatedBy:     "different-user",
				CreatedByType: coreapi.CreatedByTypeApplication,
				CreatedAt:     &later,
			},
			oldObj: &coreapi.SystemData{
				CreatedBy:     "original-user",
				CreatedByType: coreapi.CreatedByTypeUser,
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
			newObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			oldObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
				CreatedAt:     &now,
			},
			expectErrors: []utils.ExpectedError{},
		},
		{
			name: "nil oldObj with valid newObj - allowed",
			op:   operation.Operation{Type: operation.Update},
			newObj: &coreapi.SystemData{
				CreatedBy:     "user",
				CreatedByType: coreapi.CreatedByTypeUser,
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
