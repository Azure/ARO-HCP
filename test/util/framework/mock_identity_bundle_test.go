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
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
)

func TestSlotMockPrincipalAlignment(t *testing.T) {
	principal := func(name string) leaseenv.MockPrincipal {
		return leaseenv.MockPrincipal{ClientID: uuid.NewString(), PrincipalID: uuid.NewString(), CertificateName: name}
	}
	bundle := leaseenv.MockIdentityBundlePayload{SlotName: "slot", SubscriptionID: uuid.NewString(), MSI: principal("msi"), Backend: principal("backend"), ClustersService: principal("cs")}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(leaseenv.MockIdentityBundle, string(payload))
	t.Setenv(leaseenv.MSIMockSP, "unused-legacy")
	t.Setenv("MI_MOCK_PRINCIPAL_ID", bundle.MSI.PrincipalID)
	if got := MIMockPrincipalID(); got != bundle.MSI.PrincipalID {
		t.Fatalf("wrong MSI principal: %s", got)
	}
	for _, invalid := range []struct{ payload, explicit string }{{"{}", ""}, {string(payload), uuid.NewString()}} {
		t.Setenv(leaseenv.MockIdentityBundle, invalid.payload)
		t.Setenv("MI_MOCK_PRINCIPAL_ID", invalid.explicit)
		func() {
			defer func() {
				if recover() == nil {
					t.Error("invalid or conflicting bundle silently fell back")
				}
			}()
			MIMockPrincipalID()
		}()
	}
}
