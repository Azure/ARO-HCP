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

package azureresourcesapihelpers

import (
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func TestToDeploymentStackDesireResourceIDStringIsCanonical(t *testing.T) {
	got := ToDeploymentStackDesireResourceIDString("00000000-0000-0000-0000-000000000001", "RG", "Cluster", "Desire")
	parsed, err := azcorearm.ParseResourceID(got)
	if err != nil {
		t.Fatalf("helper produced an ID that does not parse: %v\ngot: %q", err, got)
	}
	canonical := strings.ToLower(parsed.String())
	if got != canonical {
		t.Fatalf("helper output is not canonical:\nhelper:    %q\ncanonical: %q", got, canonical)
	}
}

func TestToDeploymentStackDesireResourceID(t *testing.T) {
	id, err := ToDeploymentStackDesireResourceID("SUB", "RG", "Cluster", "Desire")
	if err != nil {
		t.Fatal(err)
	}
	want := ToDeploymentStackDesireResourceIDString("sub", "rg", "cluster", "desire")
	if strings.ToLower(id.String()) != want {
		t.Fatalf("typed helper returned %q, want %q", id.String(), want)
	}
}
