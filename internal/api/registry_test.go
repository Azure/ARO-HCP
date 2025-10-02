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

package api

import (
	"reflect"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
)

func TestGetJSONTagName(t *testing.T) {
	tests := []struct {
		name           string
		structTag      reflect.StructTag
		expectedResult string
	}{
		{
			name:           "No JSON tag returns empty string",
			structTag:      reflect.StructTag(""),
			expectedResult: "",
		},
		{
			name:           "Simple JSON tag returns field name",
			structTag:      reflect.StructTag("json:\"abc\""),
			expectedResult: "abc",
		},
		{
			name:           "JSON tag with option returns field name",
			structTag:      reflect.StructTag("json:\"abc,omitempty\""),
			expectedResult: "abc",
		},
		{
			name:           "JSON tag with \"-\" value returns empty string",
			structTag:      reflect.StructTag("json:\"-\""),
			expectedResult: "",
		},
		{
			name:           "JSON tag with field named \"-\" returns \"-\"",
			structTag:      reflect.StructTag("json:\"-,\""),
			expectedResult: "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualResult := GetJSONTagName(tt.structTag)
			assert.Equal(t, tt.expectedResult, actualResult)
		})
	}
}

type TestAPIVersionTag struct {
	APIVersion string `validate:"api_version"`
}

type TestRequiredIntTag struct {
	StructField int `json:"field" validate:"required"`
}

type TestRequiredStringTag struct {
	StructField string `json:"field" validate:"required"`
}

type TestRequiredTag struct {
	StructField any `json:"field" validate:"required"`
}

func TestNewValidator(t *testing.T) {
	var nilSlice []int
	var nilMap map[int]int
	var nilPointer *int

	tests := []struct {
		name        string
		resource    any
		expectError bool
	}{
		{
			name:     "Zero value is ok when not required",
			resource: struct{ StructField int }{0},
		},
		{
			name: "Zero value on required field is error",
			resource: TestRequiredIntTag{
				StructField: 0,
			},
			expectError: true,
		},
		{
			name:     "Empty string is ok when not required",
			resource: struct{ StructField string }{""},
		},
		{
			name: "Empty string on required field is error",
			resource: TestRequiredStringTag{
				StructField: "",
			},
			expectError: true,
		},
		{
			name: "Validation fails on nil slice",
			resource: TestRequiredTag{
				StructField: nilSlice,
			},
			expectError: true,
		},
		{
			name: "Validation passes on empty slice",
			resource: TestRequiredTag{
				StructField: []int{},
			},
		},
		{
			name: "Validation fails on nil map",
			resource: TestRequiredTag{
				StructField: nilMap,
			},
			expectError: true,
		},
		{
			name: "Validation passes on empty map",
			resource: TestRequiredTag{
				StructField: map[int]int{},
			},
		},
		{
			name: "Validation fails on nil pointer",
			resource: TestRequiredTag{
				StructField: nilPointer,
			},
			expectError: true,
		},
		{
			// FieldLevel.ExtractType dives into nullable types.
			name: "Validation fails on pointer to zero value",
			resource: TestRequiredTag{
				StructField: Ptr(nilSlice),
			},
			expectError: true,
		},
		{
			// FieldLevel.ExtractType dives into nullable types.
			name: "Validation passes on pointer to non-zero value",
			resource: TestRequiredTag{
				StructField: Ptr([]int{}),
			},
		},
	}

	validate := NewValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.resource)

			if err == nil {
				if tt.expectError {
					t.Errorf("Expected a FieldError but got none")
				}
			} else if !tt.expectError {
				t.Errorf("Unexpected error: %v", err)
			} else {
				for _, fieldError := range err.(validator.ValidationErrors) {
					switch fieldError.Tag() {
					case "api_version":
						// Valid tag, nothing more to check.
					case "required":
						// Verify the validate instance is using GetJSONTagName.
						assert.Equal(t, "field", fieldError.Field())
						assert.Equal(t, "StructField", fieldError.StructField())
					default:
						t.Errorf("Unexpected validation tag: %s", fieldError.Tag())
					}
				}
			}
		})
	}
}
