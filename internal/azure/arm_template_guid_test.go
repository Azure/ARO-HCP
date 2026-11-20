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

package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestARMTemplateGUID pins armTemplateGUID to the values Bicep's guid(subscription().id, roleName)
// produces. The expected values were generated with an independent implementation of the
// algorithm that was checked against role definitions deployed by
// dev-infrastructure/templates/e2e-subscription-rbac-assignment-subscription.bicep.
// Only placeholder subscription IDs are used here.
func TestARMTemplateGUID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		parts    []string
		expected string
	}{
		{
			name:     "disk CSI role, all-ones subscription",
			parts:    []string{"/subscriptions/11111111-1111-1111-1111-111111111111", "ARO HCP Data Plane Disk CSI Operator"},
			expected: "5907dcbb-f5f4-547f-84e2-a854a5d7e3a9",
		},
		{
			name:     "file CSI role, all-ones subscription",
			parts:    []string{"/subscriptions/11111111-1111-1111-1111-111111111111", "ARO HCP Data Plane File CSI Operator"},
			expected: "54f2f0e6-36cd-5318-9501-adc566aed026",
		},
		{
			name:     "image registry role, all-ones subscription",
			parts:    []string{"/subscriptions/11111111-1111-1111-1111-111111111111", "ARO HCP Data Plane Image Registry Operator"},
			expected: "a18cae55-6b07-5669-bc62-3d4560308db9",
		},
		{
			name:     "disk CSI role, all-zeros subscription",
			parts:    []string{"/subscriptions/00000000-0000-0000-0000-000000000000", "ARO HCP Data Plane Disk CSI Operator"},
			expected: "6eea2e50-c9e0-5875-ae64-300efa1827d0",
		},
		{
			name:     "file CSI role, all-zeros subscription",
			parts:    []string{"/subscriptions/00000000-0000-0000-0000-000000000000", "ARO HCP Data Plane File CSI Operator"},
			expected: "2e36b41d-5d0d-5a6b-98b4-ef94ed151adb",
		},
		{
			name:     "image registry role, all-zeros subscription",
			parts:    []string{"/subscriptions/00000000-0000-0000-0000-000000000000", "ARO HCP Data Plane Image Registry Operator"},
			expected: "c9994cce-fe17-54d1-9f78-7837e15d6da6",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, armTemplateGUID(tt.parts...))
		})
	}
}

// TestARMTemplateGUIDIsCaseSensitive documents that, like Bicep's guid(), the calculation is
// case-sensitive. Callers must normalize the casing of their inputs.
func TestARMTemplateGUIDIsCaseSensitive(t *testing.T) {
	t.Parallel()

	lower := armTemplateGUID("/subscriptions/11111111-aaaa-bbbb-cccc-dddddddddddd", "ARO HCP Data Plane Disk CSI Operator")
	upper := armTemplateGUID("/subscriptions/11111111-AAAA-BBBB-CCCC-DDDDDDDDDDDD", "ARO HCP Data Plane Disk CSI Operator")
	assert.Equal(t, "aad1e23c-4bfb-51c3-b570-ff7a07edd26a", lower)
	assert.NotEqual(t, lower, upper)
}
