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

package acquire

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
)

func TestResolveRegionSelectionWeightedOverride(t *testing.T) {
	t.Parallel()

	slotCatalog := loadWeightedTestCatalog(t)
	selection, err := resolveRegionSelection(
		slotCatalog,
		"dev",
		catalog.RegionModeWeighted,
		"canadacentral",
		"not-valid-and-must-be-ignored",
		"",
	)
	if err != nil {
		t.Fatalf("expected valid override to bypass weighted inputs: %v", err)
	}
	if selection.RuntimeRegion != "canadacentral" {
		t.Fatalf("expected override region %q, got %q", "canadacentral", selection.RuntimeRegion)
	}
	if len(selection.NormalizedWeights) != 0 {
		t.Fatalf("expected override path to ignore weights, got %v", selection.NormalizedWeights)
	}
	if selection.SelectionKeySource != "" {
		t.Fatalf("expected override path not to use a selection key, got %q", selection.SelectionKeySource)
	}

	_, err = resolveRegionSelection(slotCatalog, "dev", catalog.RegionModeWeighted, "eastus2", "", "")
	if err == nil {
		t.Fatal("expected override outside the catalog regions to fail")
	}
	if !strings.Contains(err.Error(), "is not allowed") {
		t.Fatalf("expected invalid override error, got %v", err)
	}
}

func TestResolveRegionSelectionWeightedDeterministic(t *testing.T) {
	t.Parallel()

	slotCatalog := loadWeightedTestCatalog(t)
	tests := []struct {
		buildID string
		want    string
	}{
		{buildID: "3", want: "westus3"},
		{buildID: "1", want: "centralus"},
		{buildID: "0", want: "canadacentral"},
	}
	for _, tc := range tests {
		t.Run(tc.buildID, func(t *testing.T) {
			t.Parallel()
			selection, err := resolveRegionSelection(
				slotCatalog,
				"dev",
				catalog.RegionModeWeighted,
				"",
				"canadacentral=1,westus3=1,centralus=1",
				tc.buildID,
			)
			if err != nil {
				t.Fatalf("expected weighted selection to succeed: %v", err)
			}
			if selection.RuntimeRegion != tc.want {
				t.Fatalf("expected build ID %q to select %q, got %q", tc.buildID, tc.want, selection.RuntimeRegion)
			}
			if got, want := selection.NormalizedWeights, []string{"westus3=1", "centralus=1", "canadacentral=1"}; !slices.Equal(got, want) {
				t.Fatalf("unexpected normalized weights: got %v want %v", got, want)
			}
			if selection.SelectionKeySource != "BUILD_ID" {
				t.Fatalf("expected BUILD_ID selection source, got %q", selection.SelectionKeySource)
			}
		})
	}
}

func TestParseLocationWeights(t *testing.T) {
	t.Parallel()

	regions := []string{"westus3", "centralus", "canadacentral"}
	tests := []struct {
		name      string
		raw       string
		want      []locationWeight
		wantTotal uint64
		wantError string
	}{
		{
			name:      "catalog order and zero weight",
			raw:       "canadacentral=0, westus3=2\ncentralus=1",
			want:      []locationWeight{{Location: "westus3", Weight: 2}, {Location: "centralus", Weight: 1}, {Location: "canadacentral", Weight: 0}},
			wantTotal: 3,
		},
		{name: "empty", wantError: "must not be empty"},
		{name: "leading separator", raw: ",westus3=1,centralus=1,canadacentral=1", wantError: "location=weight format"},
		{name: "trailing separator", raw: "westus3=1,centralus=1,canadacentral=1,", wantError: "location=weight format"},
		{name: "repeated separator", raw: "westus3=1,,centralus=1,canadacentral=1", wantError: "location=weight format"},
		{name: "repeated newline separator", raw: "westus3=1\n\ncentralus=1\ncanadacentral=1", wantError: "location=weight format"},
		{name: "missing", raw: "westus3=1,centralus=1", wantError: "missing weight"},
		{name: "unknown", raw: "westus3=1,centralus=1,eastus2=1", wantError: "unknown location"},
		{name: "duplicate", raw: "westus3=1,centralus=1,canadacentral=1,westus3=2", wantError: "more than once"},
		{name: "negative", raw: "westus3=-1,centralus=1,canadacentral=1", wantError: "non-negative integer"},
		{name: "non-integer", raw: "westus3=1.5,centralus=1,canadacentral=1", wantError: "non-negative integer"},
		{name: "all zero", raw: "westus3=0,centralus=0,canadacentral=0", wantError: "greater than zero"},
		{name: "overflow", raw: "westus3=18446744073709551615,centralus=1,canadacentral=0", wantError: "overflows"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, total, err := parseLocationWeights(tc.raw, regions)
			if tc.wantError != "" {
				if err == nil {
					t.Fatalf("expected weight parsing to fail with %q", tc.wantError)
				}
				if !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected error to contain %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected weight parsing to succeed: %v", err)
			}
			if !equalLocationWeights(got, tc.want) {
				t.Fatalf("unexpected parsed weights: got %v want %v", got, tc.want)
			}
			if total != tc.wantTotal {
				t.Fatalf("expected total weight %d, got %d", tc.wantTotal, total)
			}
		})
	}
}

func equalLocationWeights(left, right []locationWeight) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func loadWeightedTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	return loadCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
      - name: aro-hcp-dev-shard0-slot
        subscriptions: {e2e: dev-sub}
        region_mode: weighted
        regions: [westus3, centralus, canadacentral]
        resource_type: aro-hcp-dev-shard0-slot
        slot_count: 2
        slot_assets:
          e2e_identities:
            allocation: dedicated
            provisioning_region: westus3
            resource_group_prefix: aro-hcp-msi-container-dev
            resource_group_count: 2
`)
}

func loadCatalogFromYAML(t *testing.T, catalogYAML string) *catalog.Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "e2e-slots.yaml")
	if err := os.WriteFile(path, []byte(catalogYAML), 0o644); err != nil {
		t.Fatalf("expected catalog write to succeed: %v", err)
	}
	slotCatalog, err := catalog.LoadCatalog(path)
	if err != nil {
		t.Fatalf("expected catalog to load: %v", err)
	}
	return slotCatalog
}
