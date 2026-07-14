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

package framework

import (
	"errors"
	"regexp"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

const testLocation = "uksouth"

type skuOpt func(*armcompute.ResourceSKU)

func withCapability(name, value string) skuOpt {
	return func(s *armcompute.ResourceSKU) {
		s.Capabilities = append(s.Capabilities, &armcompute.ResourceSKUCapabilities{
			Name:  to.Ptr(name),
			Value: to.Ptr(value),
		})
	}
}

func withZones(location string, zones ...string) skuOpt {
	return func(s *armcompute.ResourceSKU) {
		zonePtrs := make([]*string, 0, len(zones))
		for _, z := range zones {
			zonePtrs = append(zonePtrs, to.Ptr(z))
		}
		s.LocationInfo = append(s.LocationInfo, &armcompute.ResourceSKULocationInfo{
			Location: to.Ptr(location),
			Zones:    zonePtrs,
		})
	}
}

func withLocationRestriction(location string) skuOpt {
	return func(s *armcompute.ResourceSKU) {
		s.Restrictions = append(s.Restrictions, &armcompute.ResourceSKURestrictions{
			Type:       to.Ptr(armcompute.ResourceSKURestrictionsTypeLocation),
			ReasonCode: to.Ptr(armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription),
			RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
				Locations: []*string{to.Ptr(location)},
			},
			Values: []*string{to.Ptr(location)},
		})
	}
}

func withZoneRestriction(location string, zones ...string) skuOpt {
	return func(s *armcompute.ResourceSKU) {
		zonePtrs := make([]*string, 0, len(zones))
		for _, z := range zones {
			zonePtrs = append(zonePtrs, to.Ptr(z))
		}
		s.Restrictions = append(s.Restrictions, &armcompute.ResourceSKURestrictions{
			Type:       to.Ptr(armcompute.ResourceSKURestrictionsTypeZone),
			ReasonCode: to.Ptr(armcompute.ResourceSKURestrictionsReasonCodeNotAvailableForSubscription),
			RestrictionInfo: &armcompute.ResourceSKURestrictionInfo{
				Locations: []*string{to.Ptr(location)},
				Zones:     zonePtrs,
			},
		})
	}
}

func makeSKU(name, location string, opts ...skuOpt) *armcompute.ResourceSKU {
	sku := &armcompute.ResourceSKU{
		Name:         to.Ptr(name),
		ResourceType: to.Ptr("virtualMachines"),
		Locations:    []*string{to.Ptr(location)},
	}
	// Default LocationInfo with no zones so the SKU is advertised in the location.
	sku.LocationInfo = []*armcompute.ResourceSKULocationInfo{{Location: to.Ptr(location)}}
	for _, opt := range opts {
		opt(sku)
	}
	return sku
}

func TestSelectVMSize(t *testing.T) {
	dPattern := regexp.MustCompile(`^Standard_D\d+s_v[345]$`)

	tests := []struct {
		name     string
		skus     []*armcompute.ResourceSKU
		selector VMSizeSelector
		want     string
		wantErr  error
	}{
		{
			name: "preferred SKU is chosen when usable",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8")),
				makeSKU("Standard_D8s_v5", testLocation, withCapability(capabilityVCPUs, "8")),
			},
			selector: VMSizeSelector{
				Name:        "default-worker",
				Preferred:   []string{"Standard_D8s_v3", "Standard_D8s_v5"},
				NamePattern: dPattern,
				MinVCPUs:    8,
			},
			want: "Standard_D8s_v3",
		},
		{
			name: "first usable preferred wins when earlier is restricted",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8"), withLocationRestriction(testLocation)),
				makeSKU("Standard_D8s_v5", testLocation, withCapability(capabilityVCPUs, "8")),
			},
			selector: VMSizeSelector{
				Name:      "default-worker",
				Preferred: []string{"Standard_D8s_v3", "Standard_D8s_v5"},
				MinVCPUs:  8,
			},
			want: "Standard_D8s_v5",
		},
		{
			name: "falls back to deterministic sorted pick when no preferred usable",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8"), withLocationRestriction(testLocation)),
				makeSKU("Standard_D16s_v5", testLocation, withCapability(capabilityVCPUs, "16")),
				makeSKU("Standard_D8s_v4", testLocation, withCapability(capabilityVCPUs, "8")),
			},
			selector: VMSizeSelector{
				Name:        "default-worker",
				Preferred:   []string{"Standard_D8s_v3"},
				NamePattern: dPattern,
				MinVCPUs:    8,
			},
			want: "Standard_D16s_v5", // sorts before Standard_D8s_v4
		},
		{
			name: "SKU not advertised in location is excluded",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", "westeurope", withCapability(capabilityVCPUs, "8")),
			},
			selector: VMSizeSelector{
				Name:      "default-worker",
				Preferred: []string{"Standard_D8s_v3"},
				MinVCPUs:  8,
			},
			wantErr: ErrNoUsableVMSize,
		},
		{
			name: "MinVCPUs filters out too-small SKUs",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D2s_v3", testLocation, withCapability(capabilityVCPUs, "2")),
			},
			selector: VMSizeSelector{
				Name:        "default-worker",
				NamePattern: dPattern,
				MinVCPUs:    8,
			},
			wantErr: ErrNoUsableVMSize,
		},
		{
			name: "CPUArchitecture constraint is enforced",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D4ps_v6", testLocation, withCapability(capabilityCPUArchitecture, "Arm64")),
				makeSKU("Standard_D4s_v5", testLocation, withCapability(capabilityCPUArchitecture, "x64")),
			},
			selector: VMSizeSelector{
				Name:            "arm64",
				NamePattern:     regexp.MustCompile(`^Standard_`),
				CPUArchitecture: "Arm64",
			},
			want: "Standard_D4ps_v6",
		},
		{
			name: "RequireGPU selects only GPU SKUs",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8")),
				makeSKU("Standard_NC4as_T4_v3", testLocation, withCapability(capabilityGPUs, "1")),
			},
			selector: VMSizeSelector{
				Name:        "gpu",
				Preferred:   []string{"Standard_NC4as_T4_v3"},
				NamePattern: regexp.MustCompile(`^Standard_N`),
				RequireGPU:  true,
			},
			want: "Standard_NC4as_T4_v3",
		},
		{
			name: "RequireZones excludes zoneless SKUs",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8")),
			},
			selector: VMSizeSelector{
				Name:         "zoned",
				Preferred:    []string{"Standard_D8s_v3"},
				RequireZones: true,
			},
			wantErr: ErrNoUsableVMSize,
		},
		{
			name: "RequireZones accepts SKU with a non-restricted zone",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8"), withZones(testLocation, "1", "2", "3")),
			},
			selector: VMSizeSelector{
				Name:         "zoned",
				Preferred:    []string{"Standard_D8s_v3"},
				RequireZones: true,
			},
			want: "Standard_D8s_v3",
		},
		{
			name: "RequireZones excludes SKU whose only zones are restricted",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation,
					withCapability(capabilityVCPUs, "8"),
					withZones(testLocation, "1"),
					withZoneRestriction(testLocation, "1"),
				),
			},
			selector: VMSizeSelector{
				Name:         "zoned",
				Preferred:    []string{"Standard_D8s_v3"},
				RequireZones: true,
			},
			wantErr: ErrNoUsableVMSize,
		},
		{
			name: "NamePattern does not constrain preferred entries",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_NC4as_T4_v3", testLocation, withCapability(capabilityGPUs, "1")),
			},
			selector: VMSizeSelector{
				Name:        "gpu",
				Preferred:   []string{"Standard_NC4as_T4_v3"},
				NamePattern: dPattern, // would not match the preferred name
				RequireGPU:  true,
			},
			want: "Standard_NC4as_T4_v3",
		},
		{
			name:     "no SKUs yields ErrNoUsableVMSize",
			skus:     nil,
			selector: VMSizeSelector{Name: "default-worker", Preferred: []string{"Standard_D8s_v3"}},
			wantErr:  ErrNoUsableVMSize,
		},
		{
			name: "RequireEphemeralOSDisk selects SKU with EphemeralOSDiskSupported=True",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v5", testLocation, withCapability(capabilityVCPUs, "8")),
				makeSKU("Standard_D8ds_v5", testLocation, withCapability(capabilityVCPUs, "8"), withCapability(capabilityEphemeralOSDiskSupported, "True")),
			},
			selector: VMSizeSelector{
				Name:                   "ephemeral",
				Preferred:              []string{"Standard_D8s_v5", "Standard_D8ds_v5"},
				MinVCPUs:               8,
				RequireEphemeralOSDisk: true,
			},
			want: "Standard_D8ds_v5",
		},
		{
			name: "RequireEphemeralOSDisk excludes all SKUs when none support ephemeral",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v5", testLocation, withCapability(capabilityVCPUs, "8")),
			},
			selector: VMSizeSelector{
				Name:                   "ephemeral",
				Preferred:              []string{"Standard_D8s_v5"},
				NamePattern:            regexp.MustCompile(`^Standard_D`),
				RequireEphemeralOSDisk: true,
			},
			wantErr: ErrNoUsableVMSize,
		},
		{
			name: "RequireEphemeralOSDisk preferred ordering is preserved",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8"), withCapability(capabilityEphemeralOSDiskSupported, "True")),
				makeSKU("Standard_D8ds_v5", testLocation, withCapability(capabilityVCPUs, "8"), withCapability(capabilityEphemeralOSDiskSupported, "True")),
			},
			selector: VMSizeSelector{
				Name:                   "ephemeral",
				Preferred:              []string{"Standard_D8s_v3", "Standard_D8ds_v5"},
				RequireEphemeralOSDisk: true,
			},
			want: "Standard_D8s_v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := selectVMSize(tt.skus, testLocation, tt.selector)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

// productionWorkerSelectors are the selectors whose fallback NamePattern is a
// correctness boundary: their deterministic fallback must never select a SKU
// outside the ARO-HCP RP instance-type allowlist (cluster-service
// cloud-resource-constraints-config), or node pool creation fails with
// InvalidRequestContent.
func productionWorkerSelectors() []VMSizeSelector {
	return []VMSizeSelector{
		DefaultWorkerVMSizeSelector(),
		SmallWorkerVMSizeSelector(),
		EphemeralOSDiskWorkerVMSizeSelector(),
	}
}

// TestWorkerSelectorPatternsRejectNonAllowlistedSKUs guards against future
// widening of the fallback regexes. Every SKU below is advertised by Azure but
// NOT in the RP allowlist; the fallback NamePattern must reject all of them.
func TestWorkerSelectorPatternsRejectNonAllowlistedSKUs(t *testing.T) {
	disallowed := []string{
		// Local-disk variants (ds/lds/ads) are not allowlisted.
		"Standard_D8ds_v5", "Standard_D4ds_v5",
		"Standard_D8lds_v6", "Standard_D4lds_v6", "Standard_D2lds_v6",
		"Standard_D8ads_v5", "Standard_D4ads_v5",
		// AMD "as" is allowlisted only for v4/v5, not v3 or v6.
		"Standard_D8as_v3", "Standard_D8as_v6",
		"Standard_D4as_v3", "Standard_D4as_v6",
		// Arm64 "p" variants are not allowlisted for these selectors.
		"Standard_D8ps_v6", "Standard_D8plds_v6",
	}
	for _, sel := range productionWorkerSelectors() {
		if sel.NamePattern == nil {
			t.Fatalf("selector %q has no NamePattern; it is required as the allowlist boundary", sel.Name)
		}
		for _, name := range disallowed {
			if sel.NamePattern.MatchString(name) {
				t.Errorf("selector %q NamePattern must not match non-allowlisted SKU %q", sel.Name, name)
			}
		}
	}
}

// TestWorkerSelectorPatternsAcceptAllowlistedSKUs ensures the patterns still
// admit the RP-allowlisted SKUs each selector is expected to fall back to.
func TestWorkerSelectorPatternsAcceptAllowlistedSKUs(t *testing.T) {
	cases := map[string][]string{
		"default-worker":          {"Standard_D8s_v3", "Standard_D8s_v4", "Standard_D8s_v5", "Standard_D8s_v6", "Standard_D8as_v4", "Standard_D8as_v5"},
		"small-worker":            {"Standard_D4s_v3", "Standard_D4s_v4", "Standard_D4s_v5", "Standard_D4s_v6", "Standard_D4as_v4", "Standard_D4as_v5"},
		"ephemeral-osdisk-worker": {"Standard_D8s_v3", "Standard_D16s_v3", "Standard_D32s_v3"},
	}
	byName := map[string]VMSizeSelector{}
	for _, sel := range productionWorkerSelectors() {
		byName[sel.Name] = sel
	}
	for name, skus := range cases {
		sel, ok := byName[name]
		if !ok {
			t.Fatalf("no production selector named %q", name)
		}
		for _, sku := range skus {
			if !sel.NamePattern.MatchString(sku) {
				t.Errorf("selector %q NamePattern must match allowlisted SKU %q", name, sku)
			}
		}
	}
}

// TestSelectVMSizeNeverPicksNonAllowlistedFallback reproduces the prod uksouth
// failure: the preferred SKUs are unusable and only a non-allowlisted SKU
// (Standard_D8lds_v6) is otherwise available. Selection must return
// ErrNoUsableVMSize rather than the non-allowlisted SKU, which the RP rejects
// with InvalidRequestContent.
func TestSelectVMSizeNeverPicksNonAllowlistedFallback(t *testing.T) {
	skus := []*armcompute.ResourceSKU{
		makeSKU("Standard_D8lds_v6", testLocation, withCapability(capabilityVCPUs, "8")),
	}
	_, _, err := selectVMSize(skus, testLocation, DefaultWorkerVMSizeSelector())
	if !errors.Is(err, ErrNoUsableVMSize) {
		t.Fatalf("expected ErrNoUsableVMSize when only a non-allowlisted SKU is available, got err=%v", err)
	}
}

// TestSelectVMSizeFallbackPrefersAllowlisted verifies that when both a
// non-allowlisted and an allowlisted (but non-preferred) SKU are available, the
// deterministic fallback selects the allowlisted one.
func TestSelectVMSizeFallbackPrefersAllowlisted(t *testing.T) {
	skus := []*armcompute.ResourceSKU{
		makeSKU("Standard_D8lds_v6", testLocation, withCapability(capabilityVCPUs, "8")), // non-allowlisted, usable
		makeSKU("Standard_D8s_v4", testLocation, withCapability(capabilityVCPUs, "8")),   // allowlisted, not in Preferred
	}
	got, _, err := selectVMSize(skus, testLocation, DefaultWorkerVMSizeSelector())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Standard_D8s_v4" {
		t.Fatalf("expected allowlisted fallback Standard_D8s_v4, got %q", got)
	}
}
