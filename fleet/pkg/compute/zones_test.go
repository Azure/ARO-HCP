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

package compute

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveZones(t *testing.T) {
	tests := []struct {
		name          string
		explicitZones string
		zoneCount     int
		want          []string
		wantErr       string
	}{
		{
			name:          "empty list derives full region zone set",
			explicitZones: "",
			zoneCount:     3,
			want:          []string{"1", "2", "3"},
		},
		{
			name:          "whitespace-only list derives full region zone set",
			explicitZones: "  ",
			zoneCount:     3,
			want:          []string{"1", "2", "3"},
		},
		{
			name:          "single-zone region derives single zone",
			explicitZones: "",
			zoneCount:     1,
			want:          []string{"1"},
		},
		{
			name:          "explicit full list is returned as-is",
			explicitZones: "1,2,3",
			zoneCount:     3,
			want:          []string{"1", "2", "3"},
		},
		{
			name:          "explicit subset skips a known-bad zone",
			explicitZones: "1,3",
			zoneCount:     3,
			want:          []string{"1", "3"},
		},
		{
			name:          "explicit list preserves operator order",
			explicitZones: "3,1",
			zoneCount:     3,
			want:          []string{"3", "1"},
		},
		{
			name:          "explicit list is normalized and trimmed",
			explicitZones: " 1 , 2 ",
			zoneCount:     3,
			want:          []string{"1", "2"},
		},
		{
			name:          "region with four zones derives all four",
			explicitZones: "",
			zoneCount:     4,
			want:          []string{"1", "2", "3", "4"},
		},
		{
			name:          "explicit subset within a four-zone region",
			explicitZones: "1,4",
			zoneCount:     4,
			want:          []string{"1", "4"},
		},
		{
			name:          "zero zone count is rejected",
			explicitZones: "",
			zoneCount:     0,
			wantErr:       "region has no availability zones",
		},
		{
			name:          "negative zone count is rejected",
			explicitZones: "",
			zoneCount:     -1,
			wantErr:       "region has no availability zones",
		},
		{
			name:          "zero zone count is rejected even with explicit zones",
			explicitZones: "1,2,3",
			zoneCount:     0,
			wantErr:       "region has no availability zones",
		},
		{
			name:          "zone above region range is rejected",
			explicitZones: "4",
			zoneCount:     3,
			wantErr:       "zone 4 is outside the region's availability zones [1,3]",
		},
		{
			name:          "valid zones mixed with an out-of-range zone is rejected",
			explicitZones: "1,3,4",
			zoneCount:     3,
			wantErr:       "zone 4 is outside the region's availability zones [1,3]",
		},
		{
			name:          "zone zero is rejected",
			explicitZones: "0",
			zoneCount:     3,
			wantErr:       "zone 0 is outside the region's availability zones [1,3]",
		},
		{
			name:          "duplicate zone is rejected",
			explicitZones: "1,1",
			zoneCount:     3,
			wantErr:       "contains duplicate zone 1",
		},
		{
			name:          "non-integer zone is rejected",
			explicitZones: "a",
			zoneCount:     3,
			wantErr:       `zone "a" is not a valid integer`,
		},
		{
			name:          "empty entry between commas is rejected",
			explicitZones: "1,,3",
			zoneCount:     3,
			wantErr:       "contains an empty zone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveZones(tt.explicitZones, tt.zoneCount)
			if len(tt.wantErr) > 0 {
				require.Error(t, err, "expected an error")
				assert.Contains(t, err.Error(), tt.wantErr, "error message mismatch")
				assert.Nil(t, got, "no zones expected on error")
				return
			}
			require.NoError(t, err, "unexpected error")
			assert.Equal(t, tt.want, got, "resolved zones mismatch")
		})
	}
}
