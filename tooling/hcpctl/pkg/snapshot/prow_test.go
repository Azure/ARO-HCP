package snapshot

import "testing"

func TestKustoRegion(t *testing.T) {
	tests := []struct {
		name   string
		region string
		want   string
	}{
		{name: "non-euap region unchanged", region: "eastus2", want: "eastus2"},
		{name: "west europe unchanged", region: "westeurope", want: "westeurope"},
		{name: "eastus2euap canary maps to eastus2", region: "eastus2euap", want: "eastus2"},
		{name: "centraluseuap canary maps to centralus", region: "centraluseuap", want: "centralus"},
		{name: "empty region unchanged", region: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kustoRegion(tt.region); got != tt.want {
				t.Errorf("kustoRegion(%q) = %q, want %q", tt.region, got, tt.want)
			}
		})
	}
}

func TestParseConfigNormalizesCanaryRegion(t *testing.T) {
	data := []byte("kusto:\n  kustoName: hcp-prod-usc\n  location: eastus2euap\n  serviceLogsDatabase: ServiceLogs\n  hostedControlPlaneLogsDatabase: HostedControlPlaneLogs\n")

	cfg, err := parseConfig(data)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}
	if cfg.Region != "eastus2" {
		t.Errorf("Region = %q, want %q", cfg.Region, "eastus2")
	}
	if cfg.KustoName != "hcp-prod-usc" {
		t.Errorf("KustoName = %q, want %q", cfg.KustoName, "hcp-prod-usc")
	}
}
