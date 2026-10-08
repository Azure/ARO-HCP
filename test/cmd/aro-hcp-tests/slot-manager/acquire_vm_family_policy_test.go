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
	"fmt"
	"os/exec"
	"reflect"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/vmfamily"
)

func TestSelectedRuntimeRegionDeterminesVMFamilyPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		mode         slots.RegionMode
		regionConfig string
		override     string
		buildID      string
		wantRegion   string
		wantWorker   string
	}{
		{"runtime override", slots.RegionModeRuntimeSelected, "region: westus3", "centralus", "", "centralus", "standardDASv5Family"},
		{"runtime override case insensitive", slots.RegionModeRuntimeSelected, "region: westus3", "CentralUS", "", "CentralUS", "standardDASv5Family"},
		{"weighted central", slots.RegionModeWeighted, "regions: [westus3, centralus, canadacentral]", "", "1", "centralus", "standardDASv5Family"},
		{"weighted default policy", slots.RegionModeWeighted, "regions: [westus3, centralus, canadacentral]", "", "0", "canadacentral", "standardDSv5Family"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, err := slots.LoadCatalog(writeAcquireTestCatalogFromYAML(t, fmt.Sprintf(`version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
    - name: shard0
      subscriptions: {e2e: dev-e2e}
      region_mode: %s
      %s
      slot_count: 1
      slot_assets:
        e2e_identities:
          allocation: dedicated
          provisioning_region: westus3
          resource_group_prefix: smoke-identities
          resource_group_count: 1
      vm_family_policy:
        worker_families: [standardDSv5Family]
        helper_families: [standardDDSv5Family]
        regions:
          centralus:
            worker_families: [standardDASv5Family]
`, tc.mode, tc.regionConfig)))
			if err != nil {
				t.Fatal(err)
			}
			selection, err := resolveRegionSelection(catalog, "dev", tc.mode, tc.override, "westus3=1,centralus=1,canadacentral=1", tc.buildID)
			if err != nil {
				t.Fatal(err)
			}
			pool := catalog.Environments["dev"].Pools[0]
			opts := &AcquireOptions{completedAcquireOptions: &completedAcquireOptions{RegionSelection: selection}}
			slot := slots.ExpandSlotsForPool("dev", pool)[0]
			slot.Subscriptions.E2E.ID = "e2e-id"
			state := &slots.AcquiredSlotState{
				Version:            2,
				DeployEnvironment:  pool.DeployEnv,
				RuntimeRegion:      opts.runtimeRegionForPool(pool),
				Slot:               slot,
				LeasedResourceName: slot.ResourceName,
				Leases:             slots.LeaseSet{Primary: slots.Lease{ResourceType: slot.ResourceType, ResourceName: slot.ResourceName}},
			}
			if state.RuntimeRegion != tc.wantRegion {
				t.Fatalf("selected runtime region: got %q, want %q", state.RuntimeRegion, tc.wantRegion)
			}
			contract := slots.NewRuntimeContractBuilder()
			if err := slots.AddCoreRuntimeExports(contract, state, "dev-e2e", "profile"); err != nil {
				t.Fatal(err)
			}
			sharedDir := t.TempDir()
			if err := slots.WriteRuntimeContract(sharedDir, contract); err != nil {
				t.Fatal(err)
			}
			envFile, err := slots.EnvFile(sharedDir)
			if err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("sh", "-c", `. "$1"; printf '%s' "$ARO_HCP_E2E_VM_FAMILY_POLICY"`, "sh", envFile).CombinedOutput()
			if err != nil {
				t.Fatalf("sourcing runtime exports: %v: %s", err, output)
			}
			got, err := vmfamily.ParseVMFamilyPolicy(output)
			if err != nil {
				t.Fatal(err)
			}
			want := vmfamily.VMFamilyPolicy{WorkerFamilies: []string{tc.wantWorker}, HelperFamilies: []string{"standardDDSv5Family"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("selected region %q: got policy %+v, want %+v", state.RuntimeRegion, got, want)
			}
		})
	}
}
