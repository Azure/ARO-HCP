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

package utils

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

type ExpectedError struct {
	Message   string // Expected error message (partial match)
	FieldPath string // Expected field path for the error
}

func VerifyErrorsMatch(t *testing.T, expectedErrors []ExpectedError, errs field.ErrorList) {
	t.Helper()
	if len(expectedErrors) != len(errs) {
		t.Errorf("expected %d errors, got %d: %v", len(expectedErrors), len(errs), errs)
	}

	// Check that each expected error message and field path is found
	for _, expectedErr := range expectedErrors {
		if len(strings.TrimSpace(expectedErr.FieldPath)) == 0 {
			t.Errorf("expected error with path %s to be non-empty", expectedErr.FieldPath)
		}
		if len(strings.TrimSpace(expectedErr.Message)) == 0 {
			t.Errorf("expected error with msg %s to be non-empty", expectedErr.Message)
		}
		found := false
		for _, err := range errs {
			messageMatch := strings.Contains(err.Detail, expectedErr.Message) || strings.Contains(err.Error(), expectedErr.Message)
			fieldMatch := strings.Contains(err.Field, expectedErr.FieldPath)
			if messageMatch && fieldMatch {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected error containing message '%s' at field '%s' but not found in: %v", expectedErr.Message, expectedErr.FieldPath, errs)
		}
	}

	for _, err := range errs {
		found := false
		for _, expectedErr := range expectedErrors {
			messageMatch := strings.Contains(err.Detail, expectedErr.Message) || strings.Contains(err.Error(), expectedErr.Message)
			fieldMatch := strings.Contains(err.Field, expectedErr.FieldPath)
			if messageMatch && fieldMatch {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("actual error '%v' but not found in expected", err)
		}
	}
}
