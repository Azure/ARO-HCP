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

package pipeline

import (
	"strings"
	"testing"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/modify"
)

func testGrafanaDatasourceConfig() configtypes.Configuration {
	return configtypes.Configuration{
		"azureGeoShortId": "uks",
		"kusto": map[string]any{
			"serviceLogsDatabase": "ServiceLogs",
		},
		"monitoring": map[string]any{
			"adxDatasourceGeographies": " UKS, eus2 ",
		},
	}
}

func testGrafanaDatasourceOutputs(kustoURI string) Outputs {
	return Outputs{
		"Microsoft.Azure.ARO.HCP.Geography": map[string]map[string]Output{
			"kusto-infra": {
				"deploy": ArmOutput{
					"kustoUri": map[string]any{
						"type":  "String",
						"value": kustoURI,
					},
				},
			},
		},
	}
}

func TestResolveGrafanaADXOptionsEnabledAndAllowed(t *testing.T) {
	adx := &types.GrafanaADXDatasource{
		Enabled:            types.Value{Value: "true"},
		DeleteWhenDisabled: true,
		ClusterURL: types.Value{Input: &types.Input{
			StepDependency: types.StepDependency{ResourceGroup: "kusto-infra", Step: "deploy"},
			Name:           "kustoUri",
		}},
		DefaultDatabase: types.Value{ConfigRef: "kusto.serviceLogsDatabase"},
		DatasourceName:  types.Value{Value: "kusto-int-uks"},
		Geographies:     types.Value{ConfigRef: "monitoring.adxDatasourceGeographies"},
		DataConsistency: "strongconsistency",
	}

	resolved, err := resolveGrafanaADXOptions("Microsoft.Azure.ARO.HCP.Geography", adx, testGrafanaDatasourceConfig(), testGrafanaDatasourceOutputs("https://example.kusto.windows.net"))
	if err != nil {
		t.Fatalf("resolveGrafanaADXOptions returned error: %v", err)
	}
	if !resolved.Enabled {
		t.Fatal("expected ADX desired state enabled")
	}
	if !resolved.DeleteWhenDisabled {
		t.Fatal("expected deleteWhenDisabled to be preserved")
	}
	if resolved.ClusterURL != "https://example.kusto.windows.net" {
		t.Fatalf("expected cluster URL to resolve, got %q", resolved.ClusterURL)
	}
	if resolved.DefaultDatabase != "ServiceLogs" {
		t.Fatalf("expected default database to resolve, got %q", resolved.DefaultDatabase)
	}
	if resolved.DatasourceName != "kusto-int-uks" {
		t.Fatalf("expected datasource name to resolve, got %q", resolved.DatasourceName)
	}
	if resolved.Geographies != "UKS, eus2" {
		t.Fatalf("expected geographies to resolve, got %q", resolved.Geographies)
	}
	if resolved.CurrentGeography != "uks" {
		t.Fatalf("expected current geography to resolve, got %q", resolved.CurrentGeography)
	}
}

func TestResolveGrafanaADXOptionsDisabledWhenGeographyNotAllowed(t *testing.T) {
	cfg := testGrafanaDatasourceConfig()
	cfg["monitoring"] = map[string]any{
		"adxDatasourceGeographies": "eus2",
	}
	adx := &types.GrafanaADXDatasource{
		Enabled:            types.Value{Value: "true"},
		DeleteWhenDisabled: true,
		DatasourceName:     types.Value{Value: "kusto-int-uks"},
		Geographies:        types.Value{ConfigRef: "monitoring.adxDatasourceGeographies"},
	}

	resolved, err := resolveGrafanaADXOptions("Microsoft.Azure.ARO.HCP.Geography", adx, cfg, nil)
	if err != nil {
		t.Fatalf("resolveGrafanaADXOptions returned error: %v", err)
	}
	if resolved.DatasourceName != "kusto-int-uks" {
		t.Fatalf("expected datasource name to stay available for delete path, got %q", resolved.DatasourceName)
	}

	opts := modify.DefaultAddDatasourceOptions()
	opts.SubscriptionID = "subscription-id"
	opts.ResourceGroup = "resource-group"
	opts.GrafanaName = "grafana-name"
	opts.AzureMonitorEnabled = false
	opts.ADXEnabled = resolved.Enabled
	opts.ADXDeleteWhenDisabled = resolved.DeleteWhenDisabled
	opts.ADXDatasourceName = resolved.DatasourceName
	opts.ADXGeographies = resolved.Geographies
	opts.ADXCurrentGeography = resolved.CurrentGeography

	validated, err := opts.Validate(t.Context())
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if validated.ADXEnabled {
		t.Fatal("expected ADX desired state disabled for disallowed geography")
	}
}

func TestResolveGrafanaADXOptionsDisabledByConfigRef(t *testing.T) {
	cfg := testGrafanaDatasourceConfig()
	cfg["monitoring"] = map[string]any{
		"adxDatasourceEnabled":     false,
		"adxDatasourceGeographies": "",
	}
	adx := &types.GrafanaADXDatasource{
		Enabled:            types.Value{ConfigRef: "monitoring.adxDatasourceEnabled"},
		DeleteWhenDisabled: true,
		DatasourceName:     types.Value{Value: "kusto-int-uks"},
		Geographies:        types.Value{ConfigRef: "monitoring.adxDatasourceGeographies"},
	}

	resolved, err := resolveGrafanaADXOptions("Microsoft.Azure.ARO.HCP.Geography", adx, cfg, nil)
	if err != nil {
		t.Fatalf("resolveGrafanaADXOptions returned error: %v", err)
	}
	if resolved.Enabled {
		t.Fatal("expected ADX desired state disabled by configRef")
	}

	opts := modify.DefaultAddDatasourceOptions()
	opts.SubscriptionID = "subscription-id"
	opts.ResourceGroup = "resource-group"
	opts.GrafanaName = "grafana-name"
	opts.AzureMonitorEnabled = false
	opts.ADXEnabled = resolved.Enabled
	opts.ADXDeleteWhenDisabled = resolved.DeleteWhenDisabled
	opts.ADXDatasourceName = resolved.DatasourceName
	opts.ADXGeographies = resolved.Geographies
	opts.ADXCurrentGeography = resolved.CurrentGeography

	if _, err := opts.Validate(t.Context()); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestResolveGrafanaADXOptionsRejectsInvalidGeographyAllowlist(t *testing.T) {
	cfg := testGrafanaDatasourceConfig()
	cfg["monitoring"] = map[string]any{
		"adxDatasourceGeographies": "uks,!",
	}
	adx := &types.GrafanaADXDatasource{
		Enabled:     types.Value{Value: "true"},
		Geographies: types.Value{ConfigRef: "monitoring.adxDatasourceGeographies"},
	}

	resolved, err := resolveGrafanaADXOptions("Microsoft.Azure.ARO.HCP.Geography", adx, cfg, nil)
	if err != nil {
		t.Fatalf("resolveGrafanaADXOptions returned error: %v", err)
	}

	opts := modify.DefaultAddDatasourceOptions()
	opts.SubscriptionID = "subscription-id"
	opts.ResourceGroup = "resource-group"
	opts.GrafanaName = "grafana-name"
	opts.AzureMonitorEnabled = false
	opts.ADXEnabled = resolved.Enabled
	opts.ADXGeographies = resolved.Geographies
	opts.ADXCurrentGeography = resolved.CurrentGeography

	_, err = opts.Validate(t.Context())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid entry") {
		t.Fatalf("expected invalid allowlist error, got %v", err)
	}
}

func TestResolveGrafanaADXOptionsFailsClosedOnMissingKustoURI(t *testing.T) {
	adx := &types.GrafanaADXDatasource{
		Enabled: types.Value{Value: "true"},
		ClusterURL: types.Value{Input: &types.Input{
			StepDependency: types.StepDependency{ResourceGroup: "kusto-infra", Step: "deploy"},
			Name:           "kustoUri",
		}},
		DefaultDatabase: types.Value{Value: "ServiceLogs"},
		DatasourceName:  types.Value{Value: "kusto-int-uks"},
	}

	resolved, err := resolveGrafanaADXOptions("Microsoft.Azure.ARO.HCP.Geography", adx, testGrafanaDatasourceConfig(), testGrafanaDatasourceOutputs(""))
	if err != nil {
		t.Fatalf("resolveGrafanaADXOptions returned error: %v", err)
	}

	opts := modify.DefaultAddDatasourceOptions()
	opts.SubscriptionID = "subscription-id"
	opts.ResourceGroup = "resource-group"
	opts.GrafanaName = "grafana-name"
	opts.AzureMonitorEnabled = false
	opts.ADXEnabled = resolved.Enabled
	opts.ADXClusterURL = resolved.ClusterURL
	opts.ADXDefaultDatabase = resolved.DefaultDatabase
	opts.ADXDatasourceName = resolved.DatasourceName
	opts.ADXGeographies = resolved.Geographies
	opts.ADXCurrentGeography = resolved.CurrentGeography

	_, err = opts.Validate(t.Context())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cluster URL is required") {
		t.Fatalf("expected missing cluster URL error, got %v", err)
	}
}
