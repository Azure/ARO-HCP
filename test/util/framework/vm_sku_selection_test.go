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
				Name:              "default-worker",
				NamePattern:       dPattern,
				MinVCPUs:          8,
				IgnoreRPAllowlist: true,
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
				makeSKU("Standard_D8as_v4", testLocation, withCapability(capabilityVCPUs, "8"), withCapability(capabilityEphemeralOSDiskSupported, "True")),
			},
			selector: VMSizeSelector{
				Name:                   "ephemeral",
				Preferred:              []string{"Standard_D8s_v5", "Standard_D8as_v4"},
				MinVCPUs:               8,
				RequireEphemeralOSDisk: true,
			},
			want: "Standard_D8as_v4",
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
				makeSKU("Standard_D8as_v4", testLocation, withCapability(capabilityVCPUs, "8"), withCapability(capabilityEphemeralOSDiskSupported, "True")),
			},
			selector: VMSizeSelector{
				Name:                   "ephemeral",
				Preferred:              []string{"Standard_D8s_v3", "Standard_D8as_v4"},
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
				Name:              "gpu",
				Preferred:         []string{"Standard_NV12s_v3"},
				MinVCPUs:          8,
				IgnoreRPAllowlist: true,
			},
			wantErr: ErrNoUsableVMSize,
		},
		{
			name: "GPU selector has no fallback: non-preferred GPU SKUs are not selected",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_NC4as_T4_v3", testLocation, withCapability(capabilityGPUs, "1"), withLocationRestriction(testLocation)),
				makeSKU("Standard_NC16ads_A10_v4", testLocation, withCapability(capabilityGPUs, "1")),
				makeSKU("Standard_NC24ads_A100_v4", testLocation, withCapability(capabilityGPUs, "1")),
			},
			selector: GPUNodePoolVMSizeSelector(),
			wantErr:  ErrNoUsableVMSize,
		},
		{
			name: "nil NamePattern disables fallback (preferred-only)",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", testLocation, withCapability(capabilityVCPUs, "8"), withLocationRestriction(testLocation)),
				makeSKU("Standard_D8s_v4", testLocation, withCapability(capabilityVCPUs, "8")),
			},
			selector: VMSizeSelector{
				Name:      "no-fallback",
				Preferred: []string{"Standard_D8s_v3"},
				MinVCPUs:  8,
				// NamePattern intentionally nil: no fallback even though Standard_D8s_v4 is usable.
			},
			wantErr: ErrNoUsableVMSize,
		},
		{
			name: "IgnoreRPAllowlist lets non-allowlisted preferred through",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D2ds_v5", testLocation, withCapability(capabilityVCPUs, "2")),
			},
			selector: VMSizeSelector{
				Name:              "jumpbox",
				Preferred:         []string{"Standard_D2ds_v5"},
				IgnoreRPAllowlist: true,
			},
			want: "Standard_D2ds_v5",
		},
		{
			name: "all SKUs pass Azure checks but none in RP allowlist yields ErrNoUsableVMSize",
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_NC16ads_A10_v4", testLocation, withCapability(capabilityGPUs, "1")),
				makeSKU("Standard_NV6ads_A10_v5", testLocation, withCapability(capabilityGPUs, "1")),
			},
			selector: VMSizeSelector{
				Name:        "gpu",
				NamePattern: regexp.MustCompile(`^Standard_N`),
				RequireGPU:  true,
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

// TestSelectVMSizeNeverPicksNonAllowlistedFallback reproduces the prod uksouth
// failure: the preferred SKUs are unusable and only a non-allowlisted SKU
// (Standard_D8lds_v6) is otherwise available. Selection must return
// ErrNoUsableVMSize rather than the non-allowlisted SKU, which the RP rejects
// with InvalidRequestContent.
func TestSelectVMSizeNeverPicksNonAllowlistedFallback(t *testing.T) {
	unsetVMFamilyPolicy(t)

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
	unsetVMFamilyPolicy(t)

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

// TestEphemeralSelectorFallsBackToArm64 covers PROD eastus2, where every x86 candidate is restricted.
func TestEphemeralSelectorFallsBackToArm64(t *testing.T) {
	arm64 := makeSKU("Standard_D8plds_v6", testLocation,
		withCapability(capabilityVCPUs, "8"),
		withCapability(capabilityCPUArchitecture, "Arm64"),
		withCapability(capabilityEphemeralOSDiskSupported, "True"))
	skus := []*armcompute.ResourceSKU{arm64}
	for _, name := range []string{"Standard_D8s_v3", "Standard_D8as_v4", "Standard_E8s_v3", "Standard_E8as_v4"} {
		skus = append(skus, makeSKU(name, testLocation,
			withCapability(capabilityVCPUs, "8"),
			withCapability(capabilityEphemeralOSDiskSupported, "True"),
			withLocationRestriction(testLocation)))
	}
	got, _, err := selectVMSize(skus, testLocation, EphemeralOSDiskWorkerVMSizeSelector())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Standard_D8plds_v6" {
		t.Fatalf("expected Arm64 fallback Standard_D8plds_v6, got %q", got)
	}

	x86 := makeSKU("Standard_D8s_v3", testLocation,
		withCapability(capabilityVCPUs, "8"),
		withCapability(capabilityEphemeralOSDiskSupported, "True"))
	got, _, err = selectVMSize([]*armcompute.ResourceSKU{arm64, x86}, testLocation, EphemeralOSDiskWorkerVMSizeSelector())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Standard_D8s_v3" {
		t.Fatalf("expected usable x86 Standard_D8s_v3 ahead of the Arm64 fallback, got %q", got)
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

func TestSpecializedVMSizeSelectorsIgnoreFamilyPolicy(t *testing.T) {
	const location = "westus3"
	t.Setenv("SELECTED_LOCATION", "")
	t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", `{"worker_families":["standardDSv5Family"],"helper_families":["standardDDSv5Family"]}`)
	tests := []struct {
		name        string
		constructor func() VMSizeSelector
		skus        []*armcompute.ResourceSKU
		want        string
	}{
		{
			name:        "ephemeral retains historical preference",
			constructor: EphemeralOSDiskWorkerVMSizeSelector,
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v5", location, withCapability(capabilityVCPUs, "8")),
				makeSKU("Standard_D8s_v3", location, withCapability(capabilityVCPUs, "8"), withCapability(capabilityEphemeralOSDiskSupported, "True")),
			},
			want: "Standard_D8s_v3",
		},
		{
			name:        "ephemeral still requires ephemeral capability",
			constructor: EphemeralOSDiskWorkerVMSizeSelector,
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v3", location, withCapability(capabilityVCPUs, "8")),
			},
		},
		{
			name:        "GPU retains vetted GPU preference",
			constructor: GPUNodePoolVMSizeSelector,
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D8s_v5", location, withCapability(capabilityVCPUs, "8")),
				makeSKU("Standard_NC4as_T4_v3", location, withCapability(capabilityGPUs, "1")),
			},
			want: "Standard_NC4as_T4_v3",
		},
		{
			name:        "GPU still requires GPU capability",
			constructor: GPUNodePoolVMSizeSelector,
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_NC4as_T4_v3", location, withCapability(capabilityVCPUs, "4")),
			},
		},
		{
			name:        "Arm64 retains architecture selection",
			constructor: ARM64NodePoolVMSizeSelector,
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D4s_v5", location, withCapability(capabilityCPUArchitecture, "x64")),
				makeSKU("Standard_D4plds_v6", location, withCapability(capabilityCPUArchitecture, "Arm64")),
			},
			want: "Standard_D4plds_v6",
		},
		{
			name:        "Arm64 still requires Arm64 capability",
			constructor: ARM64NodePoolVMSizeSelector,
			skus: []*armcompute.ResourceSKU{
				makeSKU("Standard_D4plds_v6", location, withCapability(capabilityCPUArchitecture, "x64")),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := selectVMSize(tt.skus, location, tt.constructor())
			if tt.want == "" {
				if !errors.Is(err, ErrNoUsableVMSize) || got != "" {
					t.Fatalf("expected unchanged specialized capability rejection, got SKU=%q err=%v", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("expected specialized SKU %q, got SKU=%q err=%v", tt.want, got, err)
			}
		})
	}
}
