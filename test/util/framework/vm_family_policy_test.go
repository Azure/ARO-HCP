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

package framework

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
)

func familySKU(name, family, cpus string, opts ...skuOpt) *armcompute.ResourceSKU {
	sku := makeSKU(name, testLocation, withCapability(capabilityVCPUs, cpus), withCapability(capabilityCPUArchitecture, "x64"))
	sku.Family = to.Ptr(family)
	for _, opt := range opts {
		opt(sku)
	}
	return sku
}

func TestConfiguredFamilySelection(t *testing.T) {
	selector := VMSizeSelector{Name: "family-worker", Families: []string{"first", "second"}, Preferred: []string{"Standard_D8s_v5", "Standard_D8as_v5"}, MinVCPUs: 8, MaxVCPUs: 8, CPUArchitecture: "x64"}
	tests := []struct {
		name string
		skus []*armcompute.ResourceSKU
		want string
	}{
		{"family order beats preference order", []*armcompute.ResourceSKU{familySKU("Standard_D8s_v5", "second", "8"), familySKU("Standard_D8s_v4", "first", "8")}, "Standard_D8s_v4"},
		{"preference beats lexical discovery within family", []*armcompute.ResourceSKU{familySKU("Standard_D8as_v5", "first", "8"), familySKU("Standard_D8s_v5", "first", "8")}, "Standard_D8s_v5"},
		{"deterministic bounded discovery", []*armcompute.ResourceSKU{familySKU("Standard_D8s_v6", "first", "8"), familySKU("Standard_D16s_v5", "first", "16"), familySKU("Standard_D8s_v4", "first", "8")}, "Standard_D8s_v4"},
		{"restricted family advances to next", []*armcompute.ResourceSKU{familySKU("Standard_D8s_v5", "first", "8", withLocationRestriction(testLocation)), familySKU("Standard_D8as_v5", "second", "8")}, "Standard_D8as_v5"},
		{"all zones restricted", []*armcompute.ResourceSKU{familySKU("Standard_D8s_v5", "first", "8", withZones(testLocation, "1", "2"), withZoneRestriction(testLocation, "1", "2"))}, ""},
		{"one unrestricted zone remains", []*armcompute.ResourceSKU{familySKU("Standard_D8s_v5", "first", "8", withZones(testLocation, "1", "2"), withZoneRestriction(testLocation, "1"))}, "Standard_D8s_v5"},
		{"RP rejects local disk worker", []*armcompute.ResourceSKU{familySKU("Standard_D8ds_v5", "first", "8")}, ""},
		{"wrong metadata family even for preferred name", []*armcompute.ResourceSKU{familySKU("Standard_D8s_v5", "outside", "8")}, ""},
		{"missing family metadata", []*armcompute.ResourceSKU{makeSKU("Standard_D8s_v5", testLocation, withCapability(capabilityVCPUs, "8"))}, ""},
		{"missing CPU metadata", []*armcompute.ResourceSKU{familySKU("Standard_D8s_v5", "first", "invalid")}, ""},
		{"undersized worker", []*armcompute.ResourceSKU{familySKU("Standard_D4s_v5", "first", "4")}, ""},
		{"no fallthrough outside configured families", []*armcompute.ResourceSKU{familySKU(DefaultWorkerVMSize, "outside", "8")}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := selectVMSize(tt.skus, testLocation, selector)
			if got != tt.want || (tt.want != "" && err != nil) || (tt.want == "" && !errors.Is(err, ErrNoUsableVMSize)) {
				t.Fatalf("expected SKU %q (or family exhaustion), got %q err=%v", tt.want, got, err)
			}
		})
	}
	t.Run("empty explicit family list fails closed", func(t *testing.T) {
		empty := selector
		empty.Families = []string{}
		got, _, err := selectVMSize([]*armcompute.ResourceSKU{familySKU("Standard_D8s_v5", "first", "8")}, testLocation, empty)
		if got != "" || !errors.Is(err, ErrNoUsableVMSize) {
			t.Fatalf("empty policy must not fall back: SKU=%q err=%v", got, err)
		}
	})
	t.Run("oversized preferred SKU cannot bypass role cap", func(t *testing.T) {
		bounded := selector
		bounded.Preferred = []string{"Standard_D16s_v5"}
		skus := []*armcompute.ResourceSKU{familySKU("Standard_D16s_v5", "first", "16"), familySKU("Standard_D8s_v5", "first", "8")}
		got, _, err := selectVMSize(skus, testLocation, bounded)
		if err != nil || got != "Standard_D8s_v5" {
			t.Fatalf("expected bounded discovery instead of oversized preference, got %q err=%v", got, err)
		}
	})
	t.Run("require zones preserved", func(t *testing.T) {
		zonal := selector
		zonal.RequireZones = true
		_, _, err := selectVMSize([]*armcompute.ResourceSKU{familySKU("Standard_D8s_v5", "first", "8")}, testLocation, zonal)
		if !errors.Is(err, ErrNoUsableVMSize) {
			t.Fatalf("expected non-zonal SKU rejection, got %v", err)
		}
	})
}

func TestDirectFamilyPolicyRoleSizes(t *testing.T) {
	t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", `{"worker_families":["standardDSv5Family"],"helper_families":["standardDDSv5Family"]}`)
	t.Setenv("SELECTED_LOCATION", "")
	t.Setenv("ARO_HCP_DEPLOY_ENV", "")
	t.Setenv("LOCATION", "")
	for _, tt := range []struct {
		constructor                  func() VMSizeSelector
		name, family, cpus           string
		oversizedName, oversizedCPUs string
	}{
		{DefaultWorkerVMSizeSelector, "Standard_D8s_v5", "standardDSv5Family", "8", "Standard_D16s_v5", "16"},
		{SmallWorkerVMSizeSelector, "Standard_D4s_v5", "standardDSv5Family", "4", "Standard_D8s_v5", "8"},
		{JumpboxVMSizeSelector, "Standard_D2ds_v5", "standardDDSv5Family", "2", "Standard_D4ds_v5", "4"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			selector := tt.constructor()
			good := familySKU(tt.name, tt.family, tt.cpus)
			oversized := familySKU(tt.oversizedName, tt.family, tt.oversizedCPUs)
			got, _, err := selectVMSize([]*armcompute.ResourceSKU{oversized, good}, testLocation, selector)
			if err != nil || got != tt.name {
				t.Fatalf("expected role-sized SKU %q, got %q err=%v", tt.name, got, err)
			}
			_, _, err = selectVMSize([]*armcompute.ResourceSKU{oversized}, testLocation, selector)
			if !errors.Is(err, ErrNoUsableVMSize) {
				t.Fatalf("expected oversized role rejection, got %v", err)
			}
		})
	}
}

func TestApprovedFamilyFallbacks(t *testing.T) {
	t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", `{"worker_families":["StandardDsv6Family","standardDSv5Family","standardDASv5Family"],"helper_families":["StandardDdsv6Family","standardDDSv5Family","standardDADSv5Family"]}`)
	t.Setenv("SELECTED_LOCATION", "")
	for _, role := range []struct {
		name        string
		constructor func() VMSizeSelector
		families    [3]string
		sizes       [3]string
		oversized   [3]string
		cpus        string
		largerCPUs  string
		legacy      string
	}{
		{
			name: "default-worker", constructor: DefaultWorkerVMSizeSelector,
			families:  [3]string{"StandardDsv6Family", "standardDSv5Family", "standardDASv5Family"},
			sizes:     [3]string{"Standard_D8s_v6", "Standard_D8s_v5", "Standard_D8as_v5"},
			oversized: [3]string{"Standard_D16s_v6", "Standard_D16s_v5", "Standard_D16as_v5"},
			cpus:      "8", largerCPUs: "16", legacy: DefaultWorkerVMSize,
		},
		{
			name: "small-worker", constructor: SmallWorkerVMSizeSelector,
			families:  [3]string{"StandardDsv6Family", "standardDSv5Family", "standardDASv5Family"},
			sizes:     [3]string{"Standard_D4s_v6", "Standard_D4s_v5", "Standard_D4as_v5"},
			oversized: [3]string{"Standard_D8s_v6", "Standard_D8s_v5", "Standard_D8as_v5"},
			cpus:      "4", largerCPUs: "8", legacy: SmallWorkerVMSize,
		},
		{
			name: "jumpbox", constructor: JumpboxVMSizeSelector,
			families:  [3]string{"StandardDdsv6Family", "standardDDSv5Family", "standardDADSv5Family"},
			sizes:     [3]string{"Standard_D2ds_v6", "Standard_D2ds_v5", "Standard_D2ads_v5"},
			oversized: [3]string{"Standard_D4ds_v6", "Standard_D4ds_v5", "Standard_D4ads_v5"},
			cpus:      "2", largerCPUs: "4", legacy: JumpboxVMSize,
		},
	} {
		t.Run(role.name, func(t *testing.T) {
			selector := role.constructor()
			for blocked := 0; blocked <= len(role.families); blocked++ {
				name := "exhausted"
				if blocked < len(role.sizes) {
					name = role.sizes[blocked]
				}
				t.Run(name, func(t *testing.T) {
					// A usable historical default must not escape the whitelist.
					skus := []*armcompute.ResourceSKU{familySKU(role.legacy, "standardDSv3Family", role.cpus)}
					// Reverse API order to ensure the configured family order wins.
					for i := len(role.families) - 1; i >= 0; i-- {
						sku := familySKU(role.sizes[i], role.families[i], role.cpus)
						if i < blocked {
							withLocationRestriction(testLocation)(sku)
						}
						skus = append(skus, sku, familySKU(role.oversized[i], role.families[i], role.largerCPUs))
					}
					got, _, err := selectVMSize(skus, testLocation, selector)
					if blocked == len(role.families) {
						if got != "" || !errors.Is(err, ErrNoUsableVMSize) {
							t.Fatalf("exhausted approved families must reject oversized and legacy SKUs: got %q err=%v", got, err)
						}
					} else if got != role.sizes[blocked] || err != nil {
						t.Fatalf("expected %s-vCPU fallback %q after %d restricted families, got %q err=%v", role.cpus, role.sizes[blocked], blocked, got, err)
					}
				})
			}
		})
	}
}

func TestFamilyPolicyRetainsRPAllowlist(t *testing.T) {
	// Helper families may be configured for workers, but that cannot make
	// their local-disk SKUs valid RP node pool sizes.
	t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", `{"worker_families":["standardDDSv5Family","StandardDdsv6Family","standardDADSv5Family"],"helper_families":["standardDDSv5Family","StandardDdsv6Family","standardDADSv5Family"]}`)
	t.Setenv("SELECTED_LOCATION", "")
	for _, role := range []struct {
		constructor func() VMSizeSelector
		sizes       [3]string
		cpus        string
	}{
		{DefaultWorkerVMSizeSelector, [3]string{"Standard_D8ds_v5", "Standard_D8ds_v6", "Standard_D8ads_v5"}, "8"},
		{SmallWorkerVMSizeSelector, [3]string{"Standard_D4ds_v5", "Standard_D4ds_v6", "Standard_D4ads_v5"}, "4"},
	} {
		selector := role.constructor()
		t.Run(selector.Name, func(t *testing.T) {
			skus := []*armcompute.ResourceSKU{
				familySKU(role.sizes[0], "standardDDSv5Family", role.cpus),
				familySKU(role.sizes[1], "StandardDdsv6Family", role.cpus),
				familySKU(role.sizes[2], "standardDADSv5Family", role.cpus),
			}
			got, trace, err := selectVMSize(skus, testLocation, selector)
			if got != "" || !errors.Is(err, ErrNoUsableVMSize) || trace.filteredByRPAllowlist != len(skus) {
				t.Fatalf("expected all local-disk worker SKUs rejected by RP allowlist, got %q filtered=%d err=%v", got, trace.filteredByRPAllowlist, err)
			}
		})
	}
}

func unsetVMFamilyPolicy(t *testing.T) {
	t.Helper()
	t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", "")
	if err := os.Unsetenv("ARO_HCP_E2E_VM_FAMILY_POLICY"); err != nil {
		t.Fatalf("failed to unset VM family override: %v", err)
	}
}

func TestUnconfiguredFamilyPolicyPreservesLegacySelection(t *testing.T) {
	for _, override := range []string{"unset", "{}"} {
		t.Run(override, func(t *testing.T) {
			unsetVMFamilyPolicy(t)
			t.Setenv("SELECTED_LOCATION", "")
			if override != "unset" {
				t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", override)
			}
			for _, tt := range []struct {
				constructor func() VMSizeSelector
				preferred   string
				fallback    string
				cpus        string
			}{
				{DefaultWorkerVMSizeSelector, DefaultWorkerVMSize, "Standard_D8s_v4", "8"},
				{SmallWorkerVMSizeSelector, SmallWorkerVMSize, "Standard_D4s_v4", "4"},
				{JumpboxVMSizeSelector, JumpboxVMSize, "Standard_D2s_v5", "2"},
			} {
				selector := tt.constructor()
				if selector.Families != nil || selector.MaxVCPUs != 0 || selector.CPUArchitecture != "" {
					t.Fatalf("unconfigured role must preserve historical selector constraints: %+v", selector)
				}
				for _, name := range []string{tt.preferred, tt.fallback} {
					got, _, err := selectVMSize([]*armcompute.ResourceSKU{makeSKU(name, testLocation, withCapability(capabilityVCPUs, tt.cpus))}, testLocation, selector)
					if err != nil || got != name {
						t.Fatalf("expected unchanged legacy selection %q, got %q err=%v", name, got, err)
					}
				}
			}
		})
	}
}

func TestFamilyPolicyOmittedRolePreservesLegacySelection(t *testing.T) {
	t.Setenv("SELECTED_LOCATION", "")
	for _, tt := range []struct {
		policy      string
		constructor func() VMSizeSelector
		preferred   string
		cpus        string
	}{
		{`{"helper_families":["helper"]}`, DefaultWorkerVMSizeSelector, DefaultWorkerVMSize, "8"},
		{`{"helper_families":["helper"]}`, SmallWorkerVMSizeSelector, SmallWorkerVMSize, "4"},
		{`{"worker_families":["worker"]}`, JumpboxVMSizeSelector, JumpboxVMSize, "2"},
	} {
		t.Run(tt.preferred, func(t *testing.T) {
			t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", tt.policy)
			selector := tt.constructor()
			got, _, err := selectVMSize([]*armcompute.ResourceSKU{makeSKU(tt.preferred, testLocation, withCapability(capabilityVCPUs, tt.cpus))}, testLocation, selector)
			if err != nil || got != tt.preferred || selector.Families != nil {
				t.Fatalf("expected omitted role to retain historical selection %q, got %q err=%v", tt.preferred, got, err)
			}
		})
	}
}

func TestMalformedFamilyPolicyCannotFallBack(t *testing.T) {
	t.Setenv("SELECTED_LOCATION", "")
	for _, override := range []string{"", " ", "null", "{", `{"worker_families":[]}`, `{"helper_families":null}`, `{"unknown":["family"]}`, `{} {}`} {
		t.Run(override, func(t *testing.T) {
			t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", override)
			for _, constructor := range []func() VMSizeSelector{DefaultWorkerVMSizeSelector, SmallWorkerVMSizeSelector, JumpboxVMSizeSelector} {
				selector := constructor()
				skus := []*armcompute.ResourceSKU{makeSKU(selector.Preferred[0], testLocation, withCapability(capabilityVCPUs, "8"))}
				got, _, err := selectVMSize(skus, testLocation, selector)
				if got != "" || err == nil || errors.Is(err, ErrNoUsableVMSize) || !strings.Contains(err.Error(), "ARO_HCP_E2E_VM_FAMILY_POLICY") {
					t.Fatalf("expected actionable configuration error without fallback or skippable exhaustion, got SKU=%q err=%v", got, err)
				}
				// A nil context receiver proves validation precedes Azure access.
				var tc *perItOrDescribeTestContext
				got, err = tc.SelectVMSize(context.Background(), selector)
				if got != "" || err == nil || !strings.Contains(err.Error(), "ARO_HCP_E2E_VM_FAMILY_POLICY") {
					t.Fatalf("expected configuration error before Azure access, got SKU=%q err=%v", got, err)
				}
			}
		})
	}
}

func TestPinnedSelectorBypassesFamilyPolicy(t *testing.T) {
	t.Setenv("SELECTED_LOCATION", "")
	t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", `{"worker_families":["standardDSv5Family"]}`)
	selector := VMSizeSelector{Name: "explicit-pin", Preferred: []string{DefaultWorkerVMSize}}
	got, _, err := selectVMSize([]*armcompute.ResourceSKU{makeSKU(DefaultWorkerVMSize, testLocation)}, testLocation, selector)
	if err != nil || got != DefaultWorkerVMSize {
		t.Fatalf("expected explicit historical pin unchanged, got %q err=%v", got, err)
	}
}

func TestConfiguredFamilyCapabilityChecks(t *testing.T) {
	for _, tt := range []struct {
		name         string
		selector     VMSizeSelector
		capabilities []*armcompute.ResourceSKUCapabilities
	}{
		{name: "missing vCPU capability"},
		{name: "wrong architecture", selector: VMSizeSelector{CPUArchitecture: "Arm64"}, capabilities: []*armcompute.ResourceSKUCapabilities{{Name: to.Ptr(capabilityVCPUs), Value: to.Ptr("8")}, {Name: to.Ptr(capabilityCPUArchitecture), Value: to.Ptr("x64")}}},
		{name: "GPU requirement", selector: VMSizeSelector{RequireGPU: true}, capabilities: []*armcompute.ResourceSKUCapabilities{{Name: to.Ptr(capabilityVCPUs), Value: to.Ptr("8")}}},
		{name: "ephemeral disk requirement", selector: VMSizeSelector{RequireEphemeralOSDisk: true}, capabilities: []*armcompute.ResourceSKUCapabilities{{Name: to.Ptr(capabilityVCPUs), Value: to.Ptr("8")}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			selector := tt.selector
			selector.Name = tt.name
			selector.Families = []string{"family"}
			selector.MinVCPUs, selector.MaxVCPUs = 8, 8
			sku := familySKU("Standard_D8s_v5", "family", "8")
			sku.Capabilities = tt.capabilities
			_, _, err := selectVMSize([]*armcompute.ResourceSKU{sku}, testLocation, selector)
			if !errors.Is(err, ErrNoUsableVMSize) {
				t.Fatalf("expected configured family to preserve %s check, got %v", tt.name, err)
			}
		})
	}
}
