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

package lease

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/catalog"
	"github.com/Azure/ARO-HCP/test/pkg/vmfamily"
)

func TestRuntimeContractRejectsInvalidVMFamilyPolicy(t *testing.T) {
	t.Parallel()
	for _, policy := range []catalog.PoolVMFamilyPolicy{
		{VMFamilyPolicy: vmfamily.VMFamilyPolicy{WorkerFamilies: []string{}}},
		{Regions: map[string]vmfamily.VMFamilyPolicy{"westus3": {HelperFamilies: []string{}}}},
		{Regions: map[string]vmfamily.VMFamilyPolicy{" ": {}}},
	} {
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
	policy := catalog.PoolVMFamilyPolicy{
		VMFamilyPolicy: vmfamily.VMFamilyPolicy{
			WorkerFamilies: []string{"standardDSv5Family", "standardDSv6Family"},
			HelperFamilies: []string{"standardDDSv5Family"},
		},
		Regions: map[string]vmfamily.VMFamilyPolicy{
			"centralus": {WorkerFamilies: []string{"standardDASv5Family"}},
			"eastus2":   {HelperFamilies: []string{"standardDADSv5Family"}},
		},
	}
	shellPolicy := vmfamily.VMFamilyPolicy{WorkerFamilies: []string{"family'$(printf unsafe)`printf unsafe`"}}
	for _, tc := range []struct {
		name   string
		region string
		policy catalog.PoolVMFamilyPolicy
		want   vmfamily.VMFamilyPolicy
	}{
		{"default", "westus3", policy, policy.VMFamilyPolicy},
		{"worker replacement inherits helper", "centralus", policy, vmfamily.VMFamilyPolicy{WorkerFamilies: []string{"standardDASv5Family"}, HelperFamilies: []string{"standardDDSv5Family"}}},
		{"helper replacement inherits worker", "eastus2", policy, vmfamily.VMFamilyPolicy{WorkerFamilies: policy.WorkerFamilies, HelperFamilies: []string{"standardDADSv5Family"}}},
		{"no policy clears inherited policy", "centralus", catalog.PoolVMFamilyPolicy{}, vmfamily.VMFamilyPolicy{}},
		{"shell metacharacters remain literal", "centralus", catalog.PoolVMFamilyPolicy{VMFamilyPolicy: shellPolicy}, shellPolicy},
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
			got, err := vmfamily.ParseVMFamilyPolicy([]byte(data))
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
