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
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPoolMakeTargetsQuoteSelectors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stub := filepath.Join(dir, "record-arguments")
	if err := os.WriteFile(stub, []byte("#!/bin/bash\nprintf '%s\\0' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	testDir, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"apply-pool-assets", "validate-pool-assets", "apply-identity-pool", "validate-identity-pool"} {
		for _, value := range []string{"", "normal-name", "spaces and 'single' \"double\" quotes ; # $HOME `literal`", "first\nsecond"} {
			t.Run(target+"/"+value, func(t *testing.T) {
				cmd := exec.Command("make", "--no-print-directory", "-s", "-C", testDir, "-o", stub,
					"ARO_HCP_TESTS="+stub, "ENVIRONMENT=dev", "ASSET="+value, "POOL="+value, "SUBSCRIPTION="+value, target)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("make selector forwarding failed: %v\n%s", err, output)
				}
				command := strings.Replace(target, "identity-pool", "pool-assets", 1)
				want := []string{"slot-manager", command, "--environment", "dev"}
				if strings.Contains(target, "identity-pool") {
					want = append(want, "--asset", "e2e_identities")
				} else if value != "" {
					want = append(want, "--asset", value, "--pool", value)
				}
				if value != "" {
					want = append(want, "--subscription", value)
				}
				got := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("selectors must remain literal single arguments:\ngot  %q\nwant %q", got, want)
				}
				// The shell-quoting cases also include intentionally invalid CSV flag values.
				if value != "" && value != "normal-name" {
					return
				}
				registry, err := newAssetRegistry()
				if err != nil {
					t.Fatal(err)
				}
				constructor := newApplyPoolAssetsCommand
				if strings.HasPrefix(command, "validate-") {
					constructor = newValidatePoolAssetsCommand
				}
				cli, err := constructor(registry)
				if err != nil {
					t.Fatal(err)
				}
				if err := cli.ParseFlags(got[2:]); err != nil {
					t.Fatalf("Make target emitted flags rejected by %s: %v", command, err)
				}
			})
		}
	}
}
