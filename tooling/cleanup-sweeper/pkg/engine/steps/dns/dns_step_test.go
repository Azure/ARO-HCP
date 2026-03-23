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
