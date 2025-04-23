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
	"net/http"
	"reflect"
	"testing"

	validator "github.com/go-playground/validator/v10"
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

type TestRequiredForPutTag struct {
	StructField any `json:"field" validate:"required_for_put"`
}

func TestNewValidator(t *testing.T) {
	var nilSlice []int
	var nilMap map[int]int
	var nilPointer *int

	// Register an API version without implementing the interface.
	apiRegistry["valid-api-version"] = nil

	tests := []struct {
		name        string
		context     validateContext
		expectError bool
	}{
		{
			name: "Validation passes on known API version",
			context: validateContext{
				Method: http.MethodPost, // not relevant
				Resource: TestAPIVersionTag{
					APIVersion: "valid-api-version",
				},
			},
		},
		{
			name: "Validation fails on unknown API version",
			context: validateContext{
				Method: http.MethodPost, // not relevant
				Resource: TestAPIVersionTag{
					APIVersion: "bogus-api-version",
				},
			},
			expectError: true,
		},
		{
			name: "Zero value is ok when not required",
			context: validateContext{
				Method:   http.MethodPut,
				Resource: int(0),
			},
		},
		{
			name: "Zero value on required field is error when method is PUT",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPutTag{
					StructField: int(0),
				},
			},
			expectError: true,
		},
		{
			name: "Zero value on required field is ok when method is not PUT",
			context: validateContext{
				Method: http.MethodGet,
				Resource: TestRequiredForPutTag{
					StructField: int(0),
				},
			},
		},
		{
			name: "Validation fails on nil slice",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPutTag{
					StructField: nilSlice,
				},
			},
			expectError: true,
		},
		{
			name: "Validation passes on empty slice",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPutTag{
					StructField: []int{},
				},
			},
		},
		{
			name: "Validation fails on nil map",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPutTag{
					StructField: nilMap,
				},
			},
			expectError: true,
		},
		{
			name: "Validation passes on empty map",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPutTag{
					StructField: map[int]int{},
				},
			},
		},
		{
			name: "Validation fails on nil pointer",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPutTag{
					StructField: nilPointer,
				},
			},
			expectError: true,
		},
		{
			// FieldLevel.ExtractType dives into nullable types.
			name: "Validation fails on pointer to zero value",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPutTag{
					StructField: Ptr(nilSlice),
				},
			},
			expectError: true,
		},
		{
			// FieldLevel.ExtractType dives into nullable types.
			name: "Validation passes on pointer to non-zero value",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPutTag{
					StructField: Ptr([]int{}),
				},
			},
		},
	}

	validate := NewValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.context)
			if !tt.expectError {
				assert.NoError(t, err)

			} else if assert.Error(t, err) {
				for _, fieldError := range err.(validator.ValidationErrors) {
					switch fieldError.Tag() {
					case "api_version":
						// Valid tag, nothing more to check.
					case "required_for_put":
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
