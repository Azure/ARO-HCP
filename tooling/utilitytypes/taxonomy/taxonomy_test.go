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

package taxonomy

import "testing"

// TestValidL1Categories guards against accidental edits to the L1 vocabulary,
// which is a wire contract shared with downstream consumers.
func TestValidL1Categories(t *testing.T) {
	want := map[string]bool{
		"Azure Problems":      true,
		"Deployment Failures": true,
		"Product Failures":    true,
		"Test Reliability":    true,
	}
	if len(ValidL1Categories) != len(want) {
		t.Fatalf("ValidL1Categories has %d entries, want %d", len(ValidL1Categories), len(want))
	}
	for cat := range want {
		if !ValidL1Categories[cat] {
			t.Errorf("ValidL1Categories missing %q", cat)
		}
	}
}

// TestValidL2Subcategories guards against accidental edits to the L2 vocabulary.
func TestValidL2Subcategories(t *testing.T) {
	want := map[string]bool{
		"Frontend":        true,
		"Cluster Service": true,
		"Backend":         true,
		"Maestro":         true,
		"HyperShift":      true,
		"RH Upstream":     true,
	}
	if len(ValidL2Subcategories) != len(want) {
		t.Fatalf("ValidL2Subcategories has %d entries, want %d", len(ValidL2Subcategories), len(want))
	}
	for sub := range want {
		if !ValidL2Subcategories[sub] {
			t.Errorf("ValidL2Subcategories missing %q", sub)
		}
	}
}
