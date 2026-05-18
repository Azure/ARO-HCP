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

package database

import (
	"testing"

	"github.com/stretchr/testify/assert"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func TestTopLevelResourceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rid      *azcorearm.ResourceID
		expected string
	}{
		{
			name:     "nil resource ID returns empty string",
			rid:      nil,
			expected: "",
		},
		{
			name:     "stamp resource ID returns stamp name",
			rid:      mustParseResourceID(t, "/providers/microsoft.redhatopenshift/stamps/1"),
			expected: "1",
		},
		{
			name:     "management cluster returns top-level stamp name",
			rid:      mustParseResourceID(t, "/providers/microsoft.redhatopenshift/stamps/abc/managementClusters/default"),
			expected: "abc",
		},
		{
			name:     "controller returns top-level stamp name",
			rid:      mustParseResourceID(t, "/providers/microsoft.redhatopenshift/stamps/1/managementClusters/default/controllers/MyController"),
			expected: "1",
		},
		{
			name:     "mixed case stamp name is lowercased",
			rid:      mustParseResourceID(t, "/providers/Microsoft.RedHatOpenShift/stamps/ABC"),
			expected: "abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			actual := topLevelResourceName(tt.rid)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func mustParseResourceID(t *testing.T, rawID string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(rawID)
	if err != nil {
		t.Fatalf("failed to parse resource ID %q: %v", rawID, err)
	}
	return rid
}
