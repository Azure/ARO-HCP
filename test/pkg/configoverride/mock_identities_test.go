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

package configoverride

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func TestSlotMockIdentityPrecedence(t *testing.T) {
	principal := func(name string) leaseenv.MockPrincipal {
		return leaseenv.MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
	}
	bundle := leaseenv.MockIdentityBundlePayload{SlotName: "slot", SubscriptionID: uuid.NewString(), MSI: principal("msi"), Backend: principal("backend"), ClustersService: principal("cs")}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	input := Inputs{DeployEnvironment: "ci01", MockIdentityBundle: string(payload)}
	result, err := Generate(input, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{bundle.MSI.ClientID, bundle.MSI.PrincipalID, bundle.MSI.CertificateName, bundle.Backend.ClientID, bundle.Backend.CertificateName, bundle.ClustersService.ClientID, bundle.ClustersService.CertificateName} {
		if !strings.Contains(string(result.YAML), expected) {
			t.Fatalf("missing %s", expected)
		}
	}
	if strings.Contains(string(result.YAML), "armHelperFPAPrincipalId") {
		t.Fatal("changed FPA identity")
	}
	for _, invalid := range []string{"", "null", "{}", "bad"} {
		input.MockIdentityBundle = invalid
		input.MockIdentityBundleProvided = true
		if _, err := Generate(input, Paths{}); err == nil {
			t.Fatalf("accepted invalid bundle %q", invalid)
		}
	}
	parsed, err := InputsFromEnvironment([]string{leaseenv.MockIdentityBundle + "="})
	if err != nil || !parsed.MockIdentityBundleProvided {
		t.Fatal("lost empty provided bundle")
	}
}

func TestLegacyLeasesDoNotSelectIdentities(t *testing.T) {
	input, err := InputsFromEnvironment([]string{"DEPLOY_ENV=pers", "LEASED_MSI_MOCK_SP=obsolete", "LEASED_ARM_HELPER_SP=one two three"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Generate(input, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"miMock", "armHelper", "clustersServiceArmHelper"} {
		if strings.Contains(string(result.YAML), field) {
			t.Fatalf("overrode personal-dev configuration using obsolete leases: %s", result.YAML)
		}
	}
}
