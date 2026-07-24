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

package azregions

import (
	"reflect"
	"strconv"
	"testing"
)

func TestAvailabilityZones(t *testing.T) {
	tests := []struct {
		name      string
		location  string
		wantZones []string
		wantOK    bool
	}{
		{"zonal region", "uksouth", []string{"1", "2", "3"}, true},
		{"zonal region eastus", "eastus", []string{"1", "2", "3"}, true},
		{"display-name form normalized", "UK South", []string{"1", "2", "3"}, true},
		{"mixed case normalized", "EastUS", []string{"1", "2", "3"}, true},
		{"known non-zonal region", "australiacentral", []string{}, true},
		{"unknown region fails closed", "neverland", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := AvailabilityZones(tt.location)
			if ok != tt.wantOK {
				t.Fatalf("AvailabilityZones(%q) ok = %v, want %v", tt.location, ok, tt.wantOK)
			}
			if tt.wantOK && !reflect.DeepEqual(got, tt.wantZones) {
				t.Errorf("AvailabilityZones(%q) = %v, want %v", tt.location, got, tt.wantZones)
			}
		})
	}
}

// TestTableShapeMatchesBicep guards against accidental corruption of the ported
// table: every zone must be a small positive integer string. Keep the table in
// sync with dev-infrastructure/modules/common.bicep (_locationAvailabilityZones).
func TestTableShapeMatchesBicep(t *testing.T) {
	if len(locationAvailabilityZones) == 0 {
		t.Fatal("locationAvailabilityZones is empty")
	}
	for region, zones := range locationAvailabilityZones {
		for _, z := range zones {
			n, err := strconv.Atoi(z)
			if err != nil || n < 1 || n > 9 {
				t.Errorf("region %q has unexpected zone %q (want a single-digit zone number)", region, z)
			}
		}
	}
}
