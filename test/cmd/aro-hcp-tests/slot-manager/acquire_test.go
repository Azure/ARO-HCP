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

package slotmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

func TestResolveLeasedSlot(t *testing.T) {
	t.Parallel()

	opts := &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			PoolEnvironment: "dev",
			Pool: slots.Pool{
				SubscriptionName:        "dev-subscription",
				Region:                  "westus3",
				ResourceType:            "aro-hcp-dev-westus3-slot",
				SlotCount:               2,
				IdentityContainerPrefix: "aro-hcp-msi-container-dev",
				IdentityContainerCount:  1,
			},
		},
	}

	slot, err := opts.ResolveLeasedSlot("aro-hcp-dev-westus3-slot-01")
	if err != nil {
		t.Fatalf("expected leased slot resolution to succeed: %v", err)
	}
	if slot.SlotIndex != 1 {
		t.Fatalf("expected slot index 1, got %d", slot.SlotIndex)
	}
}

func TestResolveLeasedSlotRejectsResourceOutsideSelectedPool(t *testing.T) {
	t.Parallel()

	opts := &AcquireOptions{
		completedAcquireOptions: &completedAcquireOptions{
			PoolEnvironment: "dev",
			Pool: slots.Pool{
				SubscriptionName:        "dev-subscription",
				Region:                  "westus3",
				ResourceType:            "aro-hcp-dev-westus3-slot",
				SlotCount:               2,
				IdentityContainerPrefix: "aro-hcp-msi-container-dev",
				IdentityContainerCount:  1,
			},
		},
	}

	_, err := opts.ResolveLeasedSlot("aro-hcp-prod-uksouth-slot-00")
	if err == nil {
		t.Fatal("expected leased slot resolution to fail for a resource outside the selected pool")
	}
	if !strings.Contains(err.Error(), "not part of selected pool") {
		t.Fatalf("expected selected pool mismatch error, got %v", err)
	}
}

func TestAcquireCompleteRegionModeMatrix(t *testing.T) {
	fixedRegion := "westus3"
	alternateRegion := "eastus2"

	type testCase struct {
		regionMode         string
		location           string
		override           string
		wantRuntimeRegion  string
		wantErrorSubstring string
	}

	var cases []testCase
	for _, regionMode := range []string{slots.RegionModeFixed, slots.RegionModeRuntimeSelected} {
		for _, location := range []string{"", fixedRegion, alternateRegion} {
			for _, override := range []string{"", fixedRegion, alternateRegion} {
				effectiveRegion := override
				if effectiveRegion == "" {
					effectiveRegion = location
				}

				tc := testCase{
					regionMode: regionMode,
					location:   location,
					override:   override,
				}

				switch regionMode {
				case slots.RegionModeFixed:
					if effectiveRegion == "" || effectiveRegion == fixedRegion {
						tc.wantRuntimeRegion = fixedRegion
					} else {
						tc.wantErrorSubstring = "no pool found"
					}
				case slots.RegionModeRuntimeSelected:
					if effectiveRegion == "" {
						tc.wantRuntimeRegion = fixedRegion
					} else {
						tc.wantRuntimeRegion = effectiveRegion
					}
				}

				cases = append(cases, tc)
			}
		}
	}

	for _, tc := range cases {
		testName := fmt.Sprintf(
			"regionMode=%s/location=%q/override=%q",
			tc.regionMode,
			tc.location,
			tc.override,
		)
		t.Run(testName, func(t *testing.T) {
			clusterProfileDir := writeAcquireTestClusterProfile(t, "ARO HCP E2E Hosted Clusters (EA Subscription)")
			catalogPath := writeAcquireTestCatalog(t, tc.regionMode, fixedRegion)

			t.Setenv("CLUSTER_PROFILE_DIR", clusterProfileDir)
			t.Setenv("ARO_HCP_DEPLOY_ENV", "ci01")
			t.Setenv("SHARED_DIR", t.TempDir())
			t.Setenv("LEASE_PROXY_SERVER_URL", "http://lease-proxy.example.com")
			t.Setenv("LOCATION", tc.location)
			t.Setenv("MULTISTAGE_PARAM_OVERRIDE_LOCATION", tc.override)

			opts := DefaultAcquireOptions()
			opts.CatalogPath = catalogPath

			validated, err := opts.Validate()
			if err != nil {
				t.Fatalf("expected options validation to succeed: %v", err)
			}

			completed, err := validated.Complete(context.Background())
			if tc.wantErrorSubstring != "" {
				if err == nil {
					t.Fatalf("expected completion to fail with %q", tc.wantErrorSubstring)
				}
				if !strings.Contains(err.Error(), tc.wantErrorSubstring) {
					t.Fatalf("expected completion error to contain %q, got %v", tc.wantErrorSubstring, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected completion to succeed: %v", err)
			}

			if completed.RuntimeRegion != tc.wantRuntimeRegion {
				t.Fatalf("expected runtime region %q, got %q", tc.wantRuntimeRegion, completed.RuntimeRegion)
			}
			if completed.Pool.Region != fixedRegion {
				t.Fatalf("expected selected pool region %q, got %q", fixedRegion, completed.Pool.Region)
			}
		})
	}
}

func writeAcquireTestCatalog(t *testing.T, regionMode, region string) string {
	t.Helper()

	catalogDir := t.TempDir()
	catalogPath := filepath.Join(catalogDir, "e2e-slots.yaml")
	catalog := fmt.Sprintf(`version: 1
environments:
  dev:
    deploy_envs: [prow, ci01]
    pools:
      - subscription_name: "ARO HCP E2E Hosted Clusters (EA Subscription)"
        region: %s
        region_mode: %s
        resource_type: aro-hcp-dev-shard0-slot
        slot_count: 2
        identity_container_prefix: aro-hcp-msi-container-dev
        identity_container_count: 2
`, region, regionMode)
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o644); err != nil {
		t.Fatalf("expected catalog write to succeed: %v", err)
	}
	return catalogPath
}

func writeAcquireTestClusterProfile(t *testing.T, subscriptionName string) string {
	t.Helper()

	clusterProfileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clusterProfileDir, "customer-shard0-subscription-name"), []byte(subscriptionName), 0o644); err != nil {
		t.Fatalf("expected cluster profile write to succeed: %v", err)
	}
	return clusterProfileDir
}
