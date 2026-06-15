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

package noprint

import (
	"fmt"
	"testing"
)

// This file contains negative test cases - code that should NOT trigger the linter

func TestWithFmtSprintf(t *testing.T) {
	// Acceptable - writing to strings, not standard output
	message := fmt.Sprintf("This is fine: %s", "writing to string")
	_ = message
}

func TestWithFmtErrorf(t *testing.T) {
	// Acceptable - returns an error, doesn't print
	err := fmt.Errorf("error message: %v", "some error")
	_ = err
}

func TestBuildingStrings(t *testing.T) {
	// Acceptable - building strings
	str := fmt.Sprintf("Building a string with value: %d", 42)
	t.Logf("Result: %s", str)
}

func TestWithTestingLog(t *testing.T) {
	// Acceptable - use t.Log / t.Logf instead of fmt.Print*
	t.Log("This is fine")
	t.Logf("Also fine: %s", "value")
}

func TestWithNolintInlineComment(t *testing.T) {
	fmt.Println("suppressed by inline nolint")  // nolint:noprint
	fmt.Printf("suppressed too: %s\n", "value") // nolint:noprint
	println("builtin suppressed")               // nolint:noprint
}

func TestWithNoprintIgnoreInlineComment(t *testing.T) {
	fmt.Println("suppressed by inline noprint:ignore") // noprint:ignore
	fmt.Printf("suppressed too: %s\n", "value")        // noprint:ignore
	println("builtin suppressed")                      // noprint:ignore
}

func TestWithStandaloneNolintComment(t *testing.T) {
	// nolint:noprint
	fmt.Println("suppressed by preceding nolint comment")
	// noprint:ignore
	fmt.Printf("suppressed too: %s\n", "value")
}
