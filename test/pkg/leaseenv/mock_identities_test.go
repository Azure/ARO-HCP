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

package leaseenv

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestMockBundleValidation(t *testing.T) {
	principal := func(name string) MockPrincipal {
		return MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
	}
	bundle := MockIdentityBundlePayload{SlotName: "slot-01", SubscriptionID: uuid.NewString(), MSI: principal("msi"), Backend: principal("backend"), ClustersService: principal("cs")}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := ParseMockIdentityBundle(string(payload))
	if err != nil || actual != bundle {
		t.Fatalf("round trip: %v, %v", actual, err)
	}
	for _, input := range []string{"", "null", "{}", string(payload) + "{}", `{"unknown":true}`, `{"msi":null}`} {
		if _, err := ParseMockIdentityBundle(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	for _, mutate := range []func(*MockIdentityBundlePayload){
		func(bundle *MockIdentityBundlePayload) { bundle.Backend = bundle.MSI },
		func(bundle *MockIdentityBundlePayload) {
			bundle.ClustersService.PrincipalID = bundle.Backend.PrincipalID
		},
		func(bundle *MockIdentityBundlePayload) { bundle.MSI.CertificateName = "bad/name" },
		func(bundle *MockIdentityBundlePayload) { bundle.SubscriptionID = "not-a-uuid" },
	} {
		invalid := bundle
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatal("accepted invalid bundle")
		}
	}
}
