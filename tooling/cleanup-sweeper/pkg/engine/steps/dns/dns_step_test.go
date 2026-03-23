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

package dns

import "testing"

func TestParseNSRecordSetTargetID(t *testing.T) {
	t.Parallel()

	id := "/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/global/providers/Microsoft.Network/dnszones/hcp.osadev.cloud/NS/usw3rvaz"

	subscriptionID, resourceGroup, zoneName, recordSetName, err := parseNSRecordSetTargetID(id)
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	if subscriptionID != "1d3378d3-5a3f-4712-85a1-2485495dfc4b" {
		t.Fatalf("unexpected subscriptionID: %q", subscriptionID)
	}
	if resourceGroup != "global" {
		t.Fatalf("unexpected resourceGroup: %q", resourceGroup)
	}
	if zoneName != "hcp.osadev.cloud" {
		t.Fatalf("unexpected zoneName: %q", zoneName)
	}
	if recordSetName != "usw3rvaz" {
		t.Fatalf("unexpected recordSetName: %q", recordSetName)
	}
}

func TestParseNSRecordSetTargetID_Invalid(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := parseNSRecordSetTargetID("/subscriptions/abc/resourceGroups/rg/providers/Microsoft.Network/dnszones")
	if err == nil {
		t.Fatalf("expected parse error for invalid ID")
	}
}
