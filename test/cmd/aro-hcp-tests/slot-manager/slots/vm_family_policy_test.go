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

package slots

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"

	e2econfig "github.com/Azure/ARO-HCP/test/e2e-config"
)

func TestCatalogVMFamilyPolicyPoolIsolation(t *testing.T) {
	t.Parallel()
	catalog := loadCatalogFromYAML(t, `version: 2
environments:
  dev:
    deployment_environment: {name: ci01}
    pools:
    - name: limited
      subscriptions: {e2e: limited-sub}
      region: westus3
      slot_count: 2
      slot_assets:
        e2e_identities:
          allocation: dedicated
          resource_group_prefix: limited-identities
          resource_group_count: 1
      vm_family_policy:
        worker_families: [standardDSv5Family]
        helper_families: [standardDDSv5Family]
    - name: unrestricted
      subscriptions: {e2e: unrestricted-sub}
      region: westus3
      slot_count: 1
      slot_assets:
        e2e_identities:
          allocation: dedicated
          resource_group_prefix: unrestricted-identities
          resource_group_count: 1
`)
	for _, subscription := range []string{"limited-sub", "unrestricted-sub"} {
		pool, err := catalog.ResolvePool("dev", sets.New(subscription), nil, "westus3")
		if err != nil {
			t.Fatal(err)
		}
		want := e2econfig.VMFamilyPolicy{}
		if subscription == "limited-sub" {
			want.WorkerFamilies = []string{"standardDSv5Family"}
			want.HelperFamilies = []string{"standardDDSv5Family"}
		}
		for _, slot := range ExpandSlotsForPool("dev", pool) {
			if got := slot.VMFamilyPolicy.Resolve("westus3"); !reflect.DeepEqual(got, want) {
				t.Fatalf("subscription %q slot %q: got %+v, want %+v", subscription, slot.ResourceName, got, want)
			}
		}
	}
}

func TestCatalogRejectsMalformedVMFamilyPolicy(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{
		`[]`,
		`{unknown: [standardDSv5Family]}`,
		`{worker_families: null}`,
		`{helper_families: null}`,
		`{worker_families: []}`,
		`{helper_families: []}`,
		`{worker_families: standardDSv5Family}`,
		`{worker_families: [null]}`,
		`{worker_families: [123]}`,
		`{worker_families: [""]}`,
		`{worker_families: [standardDSv5Family, standardDSv5Family]}`,
		`{worker_families: [standardDSv5Family], worker_families: [standardDDSv5Family]}`,
		`{regions: null}`,
		`{regions: []}`,
		`{regions: {westus3: null}}`,
		`{regions: {westus3: {helper_families: null}}}`,
		`{regions: {westus3: {worker_families: []}}}`,
		`{regions: {westus3: {unknown: [standardDSv5Family]}}}`,
		`{regions: {westus3: {regions: {}}}}`,
		`{regions: {"": {}}}`,
		`{regions: {"West US 3": {}}}`,
		`{regions: {westus3: {}, westus3: {}}}`,
	} {
		t.Run(policy, func(t *testing.T) {
			input := strings.Replace(dedicatedCatalog, "      slot_count: 2", "      slot_count: 2\n      vm_family_policy: "+policy, 1)
			if _, err := loadCatalogFromYAMLWithError(t, input); err == nil {
				t.Fatalf("malformed policy was accepted: %s", policy)
			}
		})
	}
}

func TestCatalogValidatesProgrammaticVMFamilyPolicy(t *testing.T) {
	t.Parallel()
	for _, policy := range []PoolVMFamilyPolicy{
		{VMFamilyPolicy: e2econfig.VMFamilyPolicy{WorkerFamilies: []string{}}},
		{Regions: map[string]e2econfig.VMFamilyPolicy{"westus3": {HelperFamilies: []string{}}}},
		{Regions: map[string]e2econfig.VMFamilyPolicy{" ": {}}},
	} {
		catalog := loadCatalogFromYAML(t, dedicatedCatalog)
		catalog.Environments["dev"].Pools[0].VMFamilyPolicy = policy
		if err := catalog.Validate(); err == nil {
			t.Fatalf("invalid programmatic policy was accepted: %+v", policy)
		}
		state := resolvedTestState()
		state.Slot.VMFamilyPolicy = policy
		contract := NewRuntimeContractBuilder()
		if err := AddCoreRuntimeExports(contract, state, "dev-e2e", "profile"); err == nil {
			t.Fatal("invalid policy was published")
		}
		if len(contract.MarshalShell()) != 0 {
			t.Fatal("invalid policy partially published core exports")
		}
	}
}

func TestRuntimeVMFamilyPolicyShellExport(t *testing.T) {
	t.Parallel()
	policy := PoolVMFamilyPolicy{
		VMFamilyPolicy: e2econfig.VMFamilyPolicy{
			WorkerFamilies: []string{"standardDSv5Family", "standardDSv6Family"},
			HelperFamilies: []string{"standardDDSv5Family"},
		},
		Regions: map[string]e2econfig.VMFamilyPolicy{
			"centralus": {WorkerFamilies: []string{"standardDASv5Family"}},
			"eastus2":   {HelperFamilies: []string{"standardDADSv5Family"}},
		},
	}
	shellPolicy := e2econfig.VMFamilyPolicy{WorkerFamilies: []string{"family'$(printf unsafe)`printf unsafe`"}}
	for _, tc := range []struct {
		name   string
		region string
		policy PoolVMFamilyPolicy
		want   e2econfig.VMFamilyPolicy
	}{
		{"default", "westus3", policy, policy.VMFamilyPolicy},
		{"worker replacement inherits helper", "centralus", policy, e2econfig.VMFamilyPolicy{WorkerFamilies: []string{"standardDASv5Family"}, HelperFamilies: []string{"standardDDSv5Family"}}},
		{"helper replacement inherits worker", "eastus2", policy, e2econfig.VMFamilyPolicy{WorkerFamilies: policy.WorkerFamilies, HelperFamilies: []string{"standardDADSv5Family"}}},
		{"no policy clears inherited policy", "centralus", PoolVMFamilyPolicy{}, e2econfig.VMFamilyPolicy{}},
		{"shell metacharacters remain literal", "centralus", PoolVMFamilyPolicy{VMFamilyPolicy: shellPolicy}, shellPolicy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := resolvedTestState()
			state.Slot.Region = "westus3"
			state.RuntimeRegion = tc.region
			state.Slot.VMFamilyPolicy = tc.policy
			sharedDir := t.TempDir()
			if err := WriteAcquiredSlotState(sharedDir, state); err != nil {
				t.Fatal(err)
			}
			state, err := LoadAcquiredSlotState(sharedDir)
			if err != nil {
				t.Fatal(err)
			}
			contract := NewRuntimeContractBuilder()
			if err := AddCoreRuntimeExports(contract, state, "dev-e2e", "profile'$(printf unsafe)"); err != nil {
				t.Fatal(err)
			}
			if err := WriteRuntimeContract(sharedDir, contract); err != nil {
				t.Fatal(err)
			}
			envFile, err := EnvFile(sharedDir)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", `. "$1"; printf '%s\n%s' "$ARO_HCP_E2E_VM_FAMILY_POLICY" "$SELECTED_CLUSTER_PROFILE_DIR"`, "sh", envFile)
			cmd.Env = append(os.Environ(), `ARO_HCP_E2E_VM_FAMILY_POLICY={"worker_families":["staleFamily"]}`)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("executing runtime exports: %v: %s", err, output)
			}
			data, profile, ok := strings.Cut(string(output), "\n")
			if !ok || profile != "profile'$(printf unsafe)" {
				t.Fatalf("shell interpolated exported values: %q", output)
			}
			got, err := e2econfig.ParseVMFamilyPolicy([]byte(data))
			if err != nil {
				t.Fatalf("export is not a strict resolved policy: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("runtime region %q: got %+v, want %+v", tc.region, got, tc.want)
			}
			if tc.name == "no policy clears inherited policy" && data != "{}" {
				t.Fatalf("unconfigured policy must explicitly export {}, got %q", data)
			}
		})
	}
}
