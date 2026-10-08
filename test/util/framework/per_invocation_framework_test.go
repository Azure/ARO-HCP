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
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLocationRejectsInvalidatedSlotPolicy(t *testing.T) {
	for _, tt := range []struct {
		name       string
		selected   string
		location   string
		policy     string
		unset      bool
		wantReject bool
	}{
		{name: "slot region", selected: "westus3", location: "westus3", policy: `{}`},
		{name: "case insensitive", selected: "westus3", location: "WestUS3", policy: `{}`},
		{name: "overridden slot region", selected: "westus3", location: "uksouth", policy: `{"worker_families":["standardDSv5Family"]}`, wantReject: true},
		{name: "unconfigured slot region overridden", selected: "uksouth", location: "westus3", policy: `{}`, wantReject: true},
		{name: "missing runtime location", selected: "westus3", policy: `{}`, wantReject: true},
		{name: "direct local policy", location: "centralus", policy: `{"worker_families":["standardDSv5Family"]}`},
		{name: "legacy without policy", selected: "westus3", location: "centralus", unset: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SELECTED_LOCATION", tt.selected)
			t.Setenv("LOCATION", tt.location)
			t.Setenv("ARO_HCP_E2E_VM_FAMILY_POLICY", tt.policy)
			if tt.unset {
				if err := os.Unsetenv("ARO_HCP_E2E_VM_FAMILY_POLICY"); err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				rejection := recover()
				if (rejection != nil) != tt.wantReject {
					t.Fatalf("location rejection = %v, want rejection %t", rejection, tt.wantReject)
				}
				if rejection != nil && !strings.Contains(fmt.Sprint(rejection), "SELECTED_LOCATION") {
					t.Fatalf("region mismatch did not identify the slot contract: %v", rejection)
				}
			}()
			if got := location(); got != tt.location {
				t.Fatalf("runtime location = %q, want %q", got, tt.location)
			}
		})
	}
}
