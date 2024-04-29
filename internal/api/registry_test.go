package api

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"net/http"
	"reflect"
	"testing"

	validator "github.com/go-playground/validator/v10"
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
			if actualResult != tt.expectedResult {
				t.Errorf("Expected field name '%s' for %s, got '%s'", tt.expectedResult, tt.structTag, actualResult)
			}
		})
	}
}

type TestRequiredForPut struct {
	StructField any `json:"field" validate:"required_for_put"`
}

func TestNewValidator(t *testing.T) {
	var nilSlice []int
	var nilMap map[int]int
	var nilPointer *int

	tests := []struct {
		name        string
		context     validateContext
		expectError bool
	}{
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
				Resource: TestRequiredForPut{
					StructField: int(0),
				},
			},
			expectError: true,
		},
		{
			name: "Zero value on required field is ok when method is not PUT",
			context: validateContext{
				Method: http.MethodGet,
				Resource: TestRequiredForPut{
					StructField: int(0),
				},
			},
		},
		{
			name: "Validation fails on nil slice",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPut{
					StructField: nilSlice,
				},
			},
			expectError: true,
		},
		{
			name: "Validation passes on empty slice",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPut{
					StructField: []int{},
				},
			},
		},
		{
			name: "Validation fails on nil map",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPut{
					StructField: nilMap,
				},
			},
			expectError: true,
		},
		{
			name: "Validation passes on empty map",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPut{
					StructField: map[int]int{},
				},
			},
		},
		{
			name: "Validation fails on nil pointer",
			context: validateContext{
				Method: http.MethodPut,
				Resource: TestRequiredForPut{
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
				Resource: TestRequiredForPut{
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
				Resource: TestRequiredForPut{
					StructField: Ptr([]int{}),
				},
			},
		},
	}

	validate := NewValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate.Struct(tt.context)
			if err == nil {
				if tt.expectError {
					t.Errorf("Expected a FieldError but got none")
				}
			} else if !tt.expectError {
				t.Errorf("Unexpected error: %v", err)
			} else {
				for _, fieldError := range err.(validator.ValidationErrors) {
					if fieldError.Tag() != "required_for_put" {
						t.Errorf("Unexpected tag '%s' in FieldError, expected 'required_for_put'", fieldError.Tag())
					}
					if fieldError.Field() != "field" {
						t.Errorf("Unexpected JSON field name '%s' in FieldError, expected 'field'", fieldError.Field())
					}
					if fieldError.StructField() != "StructField" {
						t.Errorf("Unexpected struct field name '%s' in FieldError, expected 'StructField'", fieldError.StructField())
					}
				}
			}
		})
	}
}
