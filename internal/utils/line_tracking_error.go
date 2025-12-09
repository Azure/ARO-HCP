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
	"fmt"
	"path/filepath"
	"runtime"
)

// LineTrackingError wraps an existing error and tracks the file and line number
// where the error was created, providing detailed origin information when printed.
type LineTrackingError struct {
	originalError error
	file          string
	line          int
}

// TrackError creates a new LineTrackingError that wraps the provided error
// and captures the caller's file and line number information.
func TrackError(err error) *LineTrackingError {
	if err == nil {
		return nil
	}

	_, file, line, _ := runtime.Caller(1)
	return &LineTrackingError{
		originalError: err,
		file:          file,
		line:          line,
	}
}

// Error implements the error interface and returns a formatted string showing
// both the original error and the location where it was wrapped.
func (e *LineTrackingError) Error() string {
	fileString := "nil"
	lineString := "nil"
	originalErrorString := "nil"
	if e != nil {
		fileString = e.file
		lineString = fmt.Sprintf("%d", e.line)
		if e.originalError != nil {
			originalErrorString = e.originalError.Error()
		}
	}
	return fmt.Sprintf("(wrapped at %s:%s) %s", filepath.Base(fileString), lineString, originalErrorString)
}

// Unwrap returns the original wrapped error, enabling errors.As and errors.Is
// to work correctly with the underlying error type.
func (e *LineTrackingError) Unwrap() error {
	return e.originalError
}
