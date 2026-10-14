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
		name                    string
		explicitZones           string
		regionAvailabilityZones int
		want                    []string
		wantErr                 string
	}{
		{
			name:                    "empty list derives the first required zones",
			regionAvailabilityZones: 3,
			want:                    []string{"1", "2", "3"},
		},
		{
			name:                    "whitespace-only list derives the first required zones",
			explicitZones:           "  ",
			regionAvailabilityZones: 3,
			want:                    []string{"1", "2", "3"},
		},
		{
			name:                    "empty list is trimmed to the required count below the region's real count",
			regionAvailabilityZones: 5,
			want:                    []string{"1", "2", "3"},
		},
		{
			name:                    "region with fewer zones than required is rejected",
			regionAvailabilityZones: 2,
			wantErr:                 "region has 2 availability zones, fewer than the 3 required",
		},
		{
			name:                    "explicit list matching the required count is honored, including a zone beyond the default set",
			explicitZones:           "4,2,1",
			regionAvailabilityZones: 4,
			want:                    []string{"4", "2", "1"},
		},
		{
			name:                    "explicit list preserves operator order",
			explicitZones:           "3,1,2",
			regionAvailabilityZones: 3,
			want:                    []string{"3", "1", "2"},
		},
		{
			name:                    "explicit list is normalized and trimmed",
			explicitZones:           " 1 , 2 , 3 ",
			regionAvailabilityZones: 3,
			want:                    []string{"1", "2", "3"},
		},
		{
			name:                    "explicit list naming fewer zones than required is rejected",
			explicitZones:           "1,2",
			regionAvailabilityZones: 4,
			wantErr:                 `zone list "1,2" names 2 zones, must name exactly 3`,
		},
		{
			name:                    "explicit list naming more zones than required is rejected even though every value is valid",
			explicitZones:           "1,2,3,4",
			regionAvailabilityZones: 4,
			wantErr:                 `zone list "1,2,3,4" names 4 zones, must name exactly 3`,
		},
		{
			name:                    "explicit zone beyond the region's real count is rejected",
			explicitZones:           "5,2,1",
			regionAvailabilityZones: 4,
			wantErr:                 "zone 5 is outside the region's availability zones [1,4]",
		},
		{
			name:                    "zero region count is rejected",
			regionAvailabilityZones: 0,
			wantErr:                 "region has 0 availability zones, fewer than the 3 required",
		},
		{
			name:                    "negative region count is rejected",
			regionAvailabilityZones: -1,
			wantErr:                 "region has -1 availability zones, fewer than the 3 required",
		},
		{
			name:                    "region count too low is rejected even with explicit zones",
			explicitZones:           "1,2,3",
			regionAvailabilityZones: 0,
			wantErr:                 "region has 0 availability zones, fewer than the 3 required",
		},
		{
			name:                    "duplicate zone is rejected",
			explicitZones:           "1,1,2",
			regionAvailabilityZones: 3,
			wantErr:                 "contains duplicate zone 1",
		},
		{
			name:                    "non-integer zone is rejected",
			explicitZones:           "a,1,2",
			regionAvailabilityZones: 3,
			wantErr:                 `zone "a" is not a valid integer`,
		},
		{
			name:                    "empty entry between commas is rejected",
			explicitZones:           "1,,3",
			regionAvailabilityZones: 3,
			wantErr:                 "contains an empty zone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveZones(tt.explicitZones, tt.regionAvailabilityZones)
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
