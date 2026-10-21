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

package infrastructureidentities

import (
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func TestDefaultTopologyDerivesCompleteBundle(t *testing.T) {
	t.Parallel()
	topology := DefaultTopology()
	if len(topology.ServiceIdentityNames) != 14 || len(topology.ManagementIdentityNames) != 8 {
		t.Fatalf("unexpected identity set sizes: service=%d management=%d", len(topology.ServiceIdentityNames), len(topology.ManagementIdentityNames))
	}
	if got := len(topology.IdentityInventory()); got != 30 {
		t.Fatalf("identity inventory must be 14 + 8*2, got %d", got)
	}
	groups, err := topology.AzureResourceGroups("bundle-03")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 || groups[0].Name != "bundle-03-svc" || groups[1].Name != "bundle-03-mgmt-1" || groups[2].Name != "bundle-03-mgmt-2" {
		t.Fatalf("unexpected Azure resource groups: %+v", groups)
	}
	bundle, err := topology.Bundle("bundle-03")
	if err != nil {
		t.Fatal(err)
	}
	want := leaseenv.InfrastructureIdentityBundlePayload{
		ServiceResourceGroup:       "bundle-03-svc",
		ServiceAksIdentityName:     "service-control-plane",
		ManagementResourceGroups:   map[string]string{"1": "bundle-03-mgmt-1", "2": "bundle-03-mgmt-2"},
		ManagementAksIdentityNames: map[string]string{"1": "management-control-plane", "2": "management-control-plane"},
	}
	if bundle.ServiceResourceGroup != want.ServiceResourceGroup || bundle.ServiceAksIdentityName != want.ServiceAksIdentityName {
		t.Fatalf("service bundle = %+v, want %+v", bundle, want)
	}
	if bundle.ManagementResourceGroups["1"] != want.ManagementResourceGroups["1"] || bundle.ManagementAksIdentityNames["2"] != want.ManagementAksIdentityNames["2"] {
		t.Fatalf("management bundle = %+v, want %+v", bundle, want)
	}
}

func TestTopologyRejectsOversizedResourceGroupNames(t *testing.T) {
	t.Parallel()
	leaseName := strings.Repeat("x", 84)
	_, err := DefaultTopology().AzureResourceGroups(leaseName)
	if err == nil || !strings.Contains(err.Error(), "90-character") {
		t.Fatalf("expected Azure resource group length error, got %v", err)
	}
}
