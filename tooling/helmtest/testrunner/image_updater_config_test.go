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

package testrunner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

// serviceMonitorSpec is the slice of the ServiceMonitor CRD this test pins;
// the repo has no generated Go types for monitoring.coreos.com.
type serviceMonitorSpec struct {
	Spec struct {
		Selector struct {
			MatchLabels map[string]string `json:"matchLabels"`
		} `json:"selector"`
		Endpoints []struct {
			Port     string `json:"port"`
			Interval string `json:"interval"`
			Path     string `json:"path"`
		} `json:"endpoints"`
	} `json:"spec"`
}

// TestImageUpdaterMonitorConfig renders the image-updater monitor chart from
// the real dev-CI configuration — the chart is registered in
// topology-dev-ci.yaml, which the generic helmtest fixture discovery (walking
// topology.yaml) does not cover — and pins the deployed image, the monitor
// arguments and the ServiceMonitor-to-Service wiring the alerts depend on.
func TestImageUpdaterMonitorConfig(t *testing.T) {
	const chartDir = "../../image-updater/deploy"

	provider, err := config.NewConfigProvider("../../../config/config-dev-ci.yaml")
	require.NoError(t, err)
	resolver, err := provider.GetResolver(&config.ConfigReplacements{
		CloudReplacement:       "dev",
		EnvironmentReplacement: "dev-ci",
		RegionReplacement:      "westus3",
		RegionShortReplacement: "usw3",
		StampReplacement:       "1",
	})
	require.NoError(t, err)
	cfg, err := resolver.GetRegionConfiguration("westus3")
	require.NoError(t, err)
	// The standalone schema describes layers, not the flattened runtime config.
	validateSchema := func(configuration map[string]any) error {
		return resolver.ValidateSchema(map[string]any{
			"clouds": map[string]any{"dev": map[string]any{"defaults": configuration}},
		})
	}
	require.NoError(t, validateSchema(cfg))

	resolved, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, resolved, 0600))
	manifest, err := runTest(t.Context(), &internal.Settings{ConfigPath: configPath}, internal.TestCase{
		Name:         "image-updater",
		Namespace:    "image-updater",
		Values:       filepath.Join(chartDir, "values.yaml.tmpl"),
		HelmChartDir: chartDir,
	})
	require.NoError(t, err)

	var monitorCfg struct {
		Opstool struct {
			ImageUpdaterMonitor struct {
				Image struct {
					Registry   string `json:"registry"`
					Repository string `json:"repository"`
					Digest     string `json:"digest"`
				} `json:"image"`
				Prow struct {
					BaseURL  string `json:"baseURL"`
					JobRegex string `json:"jobRegex"`
					Interval string `json:"interval"`
				} `json:"prow"`
				ResultsBucket string `json:"resultsBucket"`
			} `json:"imageUpdaterMonitor"`
		} `json:"opstool"`
	}
	require.NoError(t, yaml.Unmarshal(resolved, &monitorCfg))
	monitor := monitorCfg.Opstool.ImageUpdaterMonitor

	var deployment appsv1.Deployment
	var service corev1.Service
	var serviceMonitor serviceMonitorSpec
	for _, document := range releaseutil.SplitManifests(manifest) {
		var object metav1.TypeMeta
		require.NoError(t, yaml.Unmarshal([]byte(document), &object))
		switch object.Kind {
		case "Deployment":
			require.NoError(t, yaml.Unmarshal([]byte(document), &deployment))
		case "Service":
			require.NoError(t, yaml.Unmarshal([]byte(document), &service))
		case "ServiceMonitor":
			require.NoError(t, yaml.Unmarshal([]byte(document), &serviceMonitor))
		}
	}

	pod := deployment.Spec.Template
	require.Equal(t, "image-updater-monitor", pod.Labels["app"])
	require.Len(t, pod.Spec.Containers, 1)
	container := pod.Spec.Containers[0]

	// The deployed image must be the digest-pinned monitor from the config.
	// runTest normalizes digests to a placeholder so fixtures stay stable, so
	// pin the registry/repository and that the pin is a digest, not a tag.
	require.Equal(t, monitor.Image.Registry+"/"+monitor.Image.Repository+"@sha256:1234567890", container.Image)

	// The monitor arguments must match the configured Prow endpoint, job
	// selection, poll interval and results bucket.
	require.Equal(t, []string{
		"monitor",
		"--prow-base-url=" + monitor.Prow.BaseURL,
		"--job-regex=" + monitor.Prow.JobRegex,
		"--interval=" + monitor.Prow.Interval,
		"--results-bucket=" + monitor.ResultsBucket,
		"--port=8080",
	}, container.Args)
	require.Equal(t, corev1.ContainerPort{Name: "metrics", ContainerPort: 8080}, container.Ports[0])

	// The ServiceMonitor must select the Service by its labels and target the
	// Service's named port; a mismatch silently stops all scraping and every
	// image-updater alert goes blind.
	require.Equal(t, service.Spec.Selector, serviceMonitor.Spec.Selector.MatchLabels)
	require.NotEmpty(t, serviceMonitor.Spec.Endpoints)
	require.Equal(t, service.Spec.Ports[0].Name, serviceMonitor.Spec.Endpoints[0].Port)
	require.Equal(t, "30s", serviceMonitor.Spec.Endpoints[0].Interval,
		"scrape interval must stay well inside the alert rules' instant-vector lookback")
	require.Equal(t, "/metrics", serviceMonitor.Spec.Endpoints[0].Path)
}
