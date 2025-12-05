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

package utils

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// CustomError is a custom error type for testing errors.As functionality
type CustomError struct {
	message string
	code    int
}

func (e *CustomError) Error() string {
	return fmt.Sprintf("custom error: %s (code: %d)", e.message, e.code)
}

func TestReturningNilForNil(t *testing.T) {
	wrappedErr := TrackError(nil)
	if wrappedErr != nil {
		t.Error("expected nil for nil input error")
	}
}

func TestLineTrackingError_Error(t *testing.T) {
	originalErr := errors.New("database connection failed")
	wrappedErr := TrackError(originalErr) // This is line 55

	errorMsg := wrappedErr.Error()
	expectedMsg := "(wrapped at line_tracking_error_test.go:43) database connection failed"

	if errorMsg != expectedMsg {
		t.Errorf("expected exact error message:\n%q\nbut got:\n%q", expectedMsg, errorMsg)
	}
}

func TestLineTrackingError_ErrorsAs(t *testing.T) {
	t.Run("errors.As works with custom error types", func(t *testing.T) {
		customErr := &CustomError{message: "validation failed", code: 400}
		wrappedErr := TrackError(customErr)

		var targetErr *CustomError
		if !errors.As(wrappedErr, &targetErr) {
			t.Error("expected errors.As to find CustomError in wrapped error")
		}

		if targetErr.message != "validation failed" {
			t.Errorf("expected message 'validation failed', got %s", targetErr.message)
		}

		if targetErr.code != 400 {
			t.Errorf("expected code 400, got %d", targetErr.code)
		}
	})

	t.Run("unwrap returns original error", func(t *testing.T) {
		originalErr := errors.New("standard error")
		wrappedErr := TrackError(originalErr)

		unwrapped := wrappedErr.Unwrap()
		if unwrapped != originalErr {
			t.Error("expected Unwrap to return original error")
		}

		if unwrapped.Error() != "standard error" {
			t.Errorf("expected 'standard error', got %s", unwrapped.Error())
		}
	})
}

func TestLineTrackingError_ErrorsIs(t *testing.T) {
	originalErr := errors.New("specific error")
	wrappedErr := TrackError(originalErr)

	if !errors.Is(wrappedErr, originalErr) {
		t.Error("expected errors.Is to identify original error in wrapped error")
	}
}

func TestLineTrackingError_MultipleWrapping(t *testing.T) {
	t.Run("multiple wrapping preserves original error", func(t *testing.T) {
		originalErr := &CustomError{message: "core error", code: 500}
		firstWrap := TrackError(originalErr)
		secondWrap := TrackError(firstWrap)

		var targetErr *CustomError
		if !errors.As(secondWrap, &targetErr) {
			t.Error("expected errors.As to find CustomError through multiple wraps")
		}

		if targetErr.message != "core error" {
			t.Errorf("expected message 'core error', got %s", targetErr.message)
		}

		errorMsg := secondWrap.Error()
		wrapCount := strings.Count(errorMsg, "wrapped at")
		if wrapCount < 1 {
			t.Errorf("expected at least 1 'wrapped at' in error message, got %d", wrapCount)
		}
	})

	t.Run("double wrapping shows both wrap locations", func(t *testing.T) {
		originalErr := errors.New("base error")
		firstWrap := TrackError(originalErr) // This is line 120
		secondWrap := TrackError(firstWrap)  // This is line 121

		errorMsg := secondWrap.Error()
		expectedMsg := "(wrapped at line_tracking_error_test.go:121) (wrapped at line_tracking_error_test.go:120) base error"

		if errorMsg != expectedMsg {
			t.Errorf("Expected exact double-wrapped error message:\n%q\nbut got:\n%q", expectedMsg, errorMsg)
		}

		// Verify errors.Is still works through double wrapping
		if !errors.Is(secondWrap, originalErr) {
			t.Error("expected errors.Is to work through double wrapping")
		}
	})
}
