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

package prometheus_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/types"
)

// TestMonitoringCRDsShipped ensures the chart always ships the prometheus-operator
// CRDs we depend on — PodMonitor and ServiceMonitor — regardless of cluster or
// monitoring backend. In AMA mode (monitoringApiGroup=azmonitoring.coreos.com,
// which the rendered dev config now selects) no Prometheus agent or operator is
// deployed, so these CRD definitions are the only thing we still need from this
// chart: Azure Managed Prometheus consumes PodMonitor/ServiceMonitor resources.
// This guards against a regression that drops the kube-prometheus-stack
// dependency (or disables its crds subchart) and silently removes the CRDs.
func TestMonitoringCRDsShipped(t *testing.T) {
	raw, err := os.ReadFile("../../config/rendered/dev/dev/westus3.yaml")
	require.NoError(t, err)

	wantCRDs := []string{
		"podmonitors.monitoring.coreos.com",
		"servicemonitors.monitoring.coreos.com",
	}

	// Dev selects AMA for both svc and mgmt; assert the CRDs ship in both.
	for _, cluster := range []string{"svc", "mgmt"} {
		t.Run(cluster, func(t *testing.T) {
			var cfg types.Configuration
			require.NoError(t, yaml.Unmarshal(raw, &cfg))

			values, err := config.PreprocessFile("values-"+cluster+".yaml", cfg)
			require.NoError(t, err)
			var overrides map[string]any
			require.NoError(t, yaml.Unmarshal(values, &overrides))

			chart, err := loader.Load("deploy")
			require.NoError(t, err)
			// ProcessDependencies honors the values' dependency conditions (e.g.
			// crds.enabled), so the resolved CRD set reflects what we deploy.
			require.NoError(t, chartutil.ProcessDependencies(chart, overrides))

			shipped := map[string]bool{}
			for _, crd := range chart.CRDObjects() {
				var meta struct {
					Metadata struct {
						Name string `json:"name"`
					} `json:"metadata"`
				}
				require.NoError(t, yaml.Unmarshal(crd.File.Data, &meta), crd.Filename)
				shipped[meta.Metadata.Name] = true
			}

			for _, name := range wantCRDs {
				assert.Contains(t, shipped, name, "chart must ship the %s CRD", name)
			}
		})
	}
}
