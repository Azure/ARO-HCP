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

package upgradecontrollers

import (
	"testing"
)

func TestIsValidNextYStreamUpgradePath(t *testing.T) {
	tests := []struct {
		name          string
		actualMinor   string
		desiredMinor  string
		expectedValid bool
	}{
		{
			name:          "Valid Y-stream upgrade",
			actualMinor:   "4.19",
			desiredMinor:  "4.20",
			expectedValid: true,
		},
		{
			name:          "Same minor version",
			actualMinor:   "4.19",
			desiredMinor:  "4.19",
			expectedValid: false,
		},
		{
			name:          "Downgrade attempt",
			actualMinor:   "4.20",
			desiredMinor:  "4.19",
			expectedValid: false,
		},
		{
			name:          "Skip minor version",
			actualMinor:   "4.19",
			desiredMinor:  "4.21",
			expectedValid: false,
		},
		{
			name:          "Major version change",
			actualMinor:   "4.19",
			desiredMinor:  "5.0",
			expectedValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidNextYStreamUpgradePath(tt.actualMinor, tt.desiredMinor)
			if result != tt.expectedValid {
				t.Errorf("isValidNextYStreamUpgradePath(%q, %q) = %v, expected %v",
					tt.actualMinor, tt.desiredMinor, result, tt.expectedValid)
			}
		})
	}
}
