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
		{
			name: "SKU with all advertised zones restricted is excluded even without RequireZones",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation,
					withCapability(capabilityVCPUs, "8"),
					withZones(testLocation, "1", "2", "3"),
					withZoneRestriction(testLocation, "1", "2", "3"),
				),
			},
			selector: VMSizeSelector{
				Name:      "default-worker",
				Preferred: []string{"Standard_D8s_v3"},
				MinVCPUs:  8,
			},
			wantErr: ErrNoUsableVMSize,
		},
		{
			name: "SKU with a surviving zone is usable when only some zones are restricted",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation,
					withCapability(capabilityVCPUs, "8"),
					withZones(testLocation, "1", "2", "3"),
					withZoneRestriction(testLocation, "1", "2"),
				),
			},
			selector: VMSizeSelector{
				Name:      "default-worker",
				Preferred: []string{"Standard_D8s_v3"},
				MinVCPUs:  8,
			},
			want: "Standard_D8s_v3",
		},
		{
			name: "restriction listing more zones than advertised still excludes the SKU",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_NV12s_v3", testLocation,
					withCapability(capabilityVCPUs, "12"),
					withZones(testLocation, "2", "3"),
					withZoneRestriction(testLocation, "1", "2", "3"),
				),
			},
			selector: VMSizeSelector{
				Name:      "gpu",
				Preferred: []string{"Standard_NV12s_v3"},
				MinVCPUs:  8,
			},
			wantErr: ErrNoUsableVMSize,
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

	// These are RP-allowlisted but exceed the 8-vCPU cap enforced by the worker
	// selectors; their fallback patterns must reject them regardless.
	tooBig := []string{
		"Standard_D16s_v3", "Standard_D32s_v3", "Standard_D64s_v3",
		"Standard_D16as_v4", "Standard_E16s_v3", "Standard_E16as_v4",
	}
	for _, sel := range productionWorkerSelectors() {
		for _, name := range tooBig {
			if sel.NamePattern.MatchString(name) {
				t.Errorf("selector %q NamePattern must not match >8-vCPU SKU %q (worker selectors cap at 8 vCPUs)", sel.Name, name)
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
		"ephemeral-osdisk-worker": {"Standard_D8s_v3", "Standard_D8as_v4", "Standard_E8s_v3", "Standard_E8as_v4"},
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

// TestEphemeralSelectorCapsAtEightVCPUs verifies the ephemeral OS disk worker
// selector never selects a SKU larger than 8 vCPUs, even when a larger
// ephemeral-capable, allowlisted SKU is the only one available.
func TestEphemeralSelectorCapsAtEightVCPUs(t *testing.T) {
	skus := []*armcompute.ResourceSKU{
		makeSKU("Standard_D16s_v3", testLocation,
			withCapability(capabilityVCPUs, "16"),
			withCapability(capabilityEphemeralOSDiskSupported, "True")),
	}
	_, _, err := selectVMSize(skus, testLocation, EphemeralOSDiskWorkerVMSizeSelector())
	if !errors.Is(err, ErrNoUsableVMSize) {
		t.Fatalf("expected ErrNoUsableVMSize (>8-vCPU SKU must be rejected), got err=%v", err)
	}
}

// TestEphemeralSelectorSelectsDifferentFamily verifies that when the Intel
// D-series preferred SKUs are unusable, the selector falls through to an
// ephemeral-capable, allowlisted SKU from a different family rather than a
// larger size of the same family.
func TestEphemeralSelectorSelectsDifferentFamily(t *testing.T) {
	skus := []*armcompute.ResourceSKU{
		// Same-family larger sizes are available but must not be chosen.
		makeSKU("Standard_D16s_v3", testLocation,
			withCapability(capabilityVCPUs, "16"),
			withCapability(capabilityEphemeralOSDiskSupported, "True")),
		// A different-family 8-vCPU ephemeral SKU that should be selected.
		makeSKU("Standard_E8as_v4", testLocation,
			withCapability(capabilityVCPUs, "8"),
			withCapability(capabilityEphemeralOSDiskSupported, "True")),
	}
	got, _, err := selectVMSize(skus, testLocation, EphemeralOSDiskWorkerVMSizeSelector())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Standard_E8as_v4" {
		t.Fatalf("expected different-family SKU Standard_E8as_v4, got %q", got)
	}
}

func TestVMSizeWithoutAvailabilityZones(t *testing.T) {
	selector := DefaultWorkerVMSizeSelector()

	skuWithZones := makeSKU("Standard_D8s_v3", testLocation,
		withCapability(capabilityVCPUs, "8"),
		withZones(testLocation, "1", "2", "3"),
	)
	if name, ok := vmSizeWithoutAvailabilityZones(skuWithZones, testLocation, selector); ok {
		t.Fatalf("expected SKU with zones to be rejected, got %q", name)
	}

	withoutZones := makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8"))
	if name, ok := vmSizeWithoutAvailabilityZones(withoutZones, testLocation, selector); !ok || name != "Standard_D8s_v3" {
		t.Fatalf("expected Standard_D8s_v3 without zones to match, got name=%q ok=%v", name, ok)
	}

	// A zone-capable SKU with all zones subscription-restricted must not be
	// treated as lacking zone support.
	withZonesAllRestricted := makeSKU("Standard_D8s_v5", testLocation,
		withCapability(capabilityVCPUs, "8"),
		withZones(testLocation, "1", "2", "3"),
		withZoneRestriction(testLocation, "1", "2", "3"),
	)
	if name, ok := vmSizeWithoutAvailabilityZones(withZonesAllRestricted, testLocation, selector); ok {
		t.Fatalf("expected zone-capable SKU with all zones restricted to be rejected, got %q", name)
	}

	nonAllowlisted := makeSKU("Standard_D8ds_v5", testLocation, withCapability(capabilityVCPUs, "8"))
	if name, ok := vmSizeWithoutAvailabilityZones(nonAllowlisted, testLocation, selector); ok {
		t.Fatalf("expected non-allowlisted SKU to be rejected, got %q", name)
	}
}

// TestWorkerSelectorPreferredEntriesAreAllowlisted closes the Preferred-list
// gap in the allowlist-boundary guard. selectVMSize tries Preferred SKUs before
// discovery and intentionally does NOT constrain them by NamePattern, so a
// non-allowlisted SKU added to Preferred would bypass the pattern boundary
// entirely. Asserting that every Preferred entry also matches its own
// NamePattern keeps the two lists in sync and prevents a non-allowlisted SKU
// from being reintroduced via Preferred.
func TestWorkerSelectorPreferredEntriesAreAllowlisted(t *testing.T) {
	for _, sel := range productionWorkerSelectors() {
		if sel.NamePattern == nil {
			t.Fatalf("selector %q has no NamePattern; it is required as the allowlist boundary", sel.Name)
		}
		if len(sel.Preferred) == 0 {
			t.Errorf("selector %q has no Preferred entries; expected an allowlisted default", sel.Name)
		}
		for _, name := range sel.Preferred {
			if !sel.NamePattern.MatchString(name) {
				t.Errorf("selector %q Preferred entry %q does not match its NamePattern %q; Preferred must stay within the RP allowlist", sel.Name, name, sel.NamePattern.String())
			}
		}
	}
}

// TestSkuRestrictedInLocation covers the zone-aware restriction detection added
// for zone-level SKU bans. Azure often expresses a full subscription/region ban
// as a Zone-type restriction listing every zone rather than a Location-type
// restriction, so a SKU whose every advertised zone is restricted must be
// reported as restricted even though no Location-type restriction is present.
func TestSkuRestrictedInLocation(t *testing.T) {
	tests := []struct {
		name string
		sku  *armcompute.ResourceSKU
		want bool
	}{
		{
			name: "no restrictions is not restricted",
			sku:  makeSKU("Standard_D8s_v3", testLocation, withZones(testLocation, "1", "2", "3")),
			want: false,
		},
		{
			name: "location-type restriction is restricted",
			sku:  makeSKU("Standard_D8s_v3", testLocation, withLocationRestriction(testLocation)),
			want: true,
		},
		{
			name: "all advertised zones restricted is restricted",
			sku: makeSKU("Standard_D8s_v3", testLocation,
				withZones(testLocation, "1", "2", "3"),
				withZoneRestriction(testLocation, "1", "2", "3"),
			),
			want: true,
		},
		{
			name: "some zones restricted is not restricted",
			sku: makeSKU("Standard_D8s_v3", testLocation,
				withZones(testLocation, "1", "2", "3"),
				withZoneRestriction(testLocation, "1", "2"),
			),
			want: false,
		},
		{
			name: "restriction covering more zones than advertised is restricted",
			sku: makeSKU("Standard_NV12s_v3", testLocation,
				withZones(testLocation, "2", "3"),
				withZoneRestriction(testLocation, "1", "2", "3"),
			),
			want: true,
		},
		{
			name: "non-zonal SKU with no restrictions is not restricted",
			sku:  makeSKU("Standard_D8s_v3", testLocation),
			want: false,
		},
		{
			name: "zone restriction for a different location is ignored",
			sku: makeSKU("Standard_D8s_v3", testLocation,
				withZones(testLocation, "1", "2", "3"),
				withZoneRestriction("westeurope", "1", "2", "3"),
			),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skuRestrictedInLocation(tt.sku, testLocation); got != tt.want {
				t.Fatalf("skuRestrictedInLocation = %v, want %v", got, tt.want)
			}
		})
	}
}
