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

package testdata

import (
	"fmt"
	"testing"

	"github.com/onsi/ginkgo/v2"
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

func TestWithGinkgoLogr(t *testing.T) {
	// Acceptable - using GinkgoLogr
	ginkgo.GinkgoLogr.Info("This is fine")
}

func TestWithGinkgoWriter(t *testing.T) {
	// Acceptable - using GinkgoWriter
	ginkgo.GinkgoWriter.Write([]byte("This is fine"))
}
