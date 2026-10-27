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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckMetricsRoutingLabel_ServiceMonitorWithLabel(t *testing.T) {
	manifest := `
apiVersion: azmonitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: aro-hcp-frontend
spec:
  endpoints:
  - port: metrics
    relabelings:
    - action: replace
      replacement: service
      targetLabel: microsoft_metrics_include_label
`
	assert.Empty(t, checkMetricsRoutingLabel(manifest, emptyAllowlist, nil))
}

func TestCheckMetricsRoutingLabel_PodMonitorWithLabel(t *testing.T) {
	manifest := `
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: maestro-agent
spec:
  podMetricsEndpoints:
  - port: metrics
    relabelings:
    - action: replace
      replacement: service
      targetLabel: microsoft_metrics_include_label
`
	assert.Empty(t, checkMetricsRoutingLabel(manifest, emptyAllowlist, nil))
}

func TestCheckMetricsRoutingLabel_ServiceMonitorMissingLabel(t *testing.T) {
	manifest := `
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: aro-hcp-frontend
spec:
  endpoints:
  - port: metrics
`
	assert.Equal(t, []string{
		`ServiceMonitor/aro-hcp-frontend spec.endpoints[0] is missing a target relabeling (relabelings) that sets microsoft_metrics_include_label=service; metrics without it are dropped by the filtered data collection rule, and setting it via metricRelabelings omits synthetic series like up (add to MetricsRoutingLabelAllowlist if intentional, or MetricsRoutingLabelOverrides to change the expected value)`,
	}, checkMetricsRoutingLabel(manifest, emptyAllowlist, nil))
}

func TestCheckMetricsRoutingLabel_PodMonitorMissingLabel(t *testing.T) {
	manifest := `
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: maestro-agent
spec:
  podMetricsEndpoints:
  - port: metrics
`
	assert.Equal(t, []string{
		`PodMonitor/maestro-agent spec.podMetricsEndpoints[0] is missing a target relabeling (relabelings) that sets microsoft_metrics_include_label=service; metrics without it are dropped by the filtered data collection rule, and setting it via metricRelabelings omits synthetic series like up (add to MetricsRoutingLabelAllowlist if intentional, or MetricsRoutingLabelOverrides to change the expected value)`,
	}, checkMetricsRoutingLabel(manifest, emptyAllowlist, nil))
}

func TestCheckMetricsRoutingLabel_WrongValue(t *testing.T) {
	manifest := `
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: aro-hcp-frontend
spec:
  endpoints:
  - port: metrics
    relabelings:
    - action: replace
      replacement: hcp
      targetLabel: microsoft_metrics_include_label
`
	assert.Len(t, checkMetricsRoutingLabel(manifest, emptyAllowlist, nil), 1)
}

func TestCheckMetricsRoutingLabel_OverrideSatisfied(t *testing.T) {
	// A monitor matching an override must set the override value instead of "service".
	manifest := `
apiVersion: azmonitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kube-state-metrics-crs
spec:
  endpoints:
  - port: http-metrics
    relabelings:
    - action: replace
      replacement: hcp
      targetLabel: microsoft_metrics_include_label
`
	overrides := map[string]string{"ServiceMonitor/kube-state-metrics-crs": "hcp"}
	assert.Empty(t, checkMetricsRoutingLabel(manifest, emptyAllowlist, overrides))
}

func TestCheckMetricsRoutingLabel_OverrideViolatedWithDefaultValue(t *testing.T) {
	// The default "service" value is a violation when an override expects "hcp".
	manifest := `
apiVersion: azmonitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kube-state-metrics-crs
spec:
  endpoints:
  - port: http-metrics
    relabelings:
    - action: replace
      replacement: service
      targetLabel: microsoft_metrics_include_label
`
	overrides := map[string]string{"ServiceMonitor/kube-state-metrics-crs": "hcp"}
	assert.Equal(t, []string{
		`ServiceMonitor/kube-state-metrics-crs spec.endpoints[0] is missing a target relabeling (relabelings) that sets microsoft_metrics_include_label=hcp; metrics without it are dropped by the filtered data collection rule, and setting it via metricRelabelings omits synthetic series like up (add to MetricsRoutingLabelAllowlist if intentional, or MetricsRoutingLabelOverrides to change the expected value)`,
	}, checkMetricsRoutingLabel(manifest, emptyAllowlist, overrides))
}

func TestCheckMetricsRoutingLabel_OverrideDoesNotAffectOtherMonitors(t *testing.T) {
	// A monitor that does not match any override still requires the default "service" value.
	manifest := `
apiVersion: azmonitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: aro-hcp-frontend
spec:
  endpoints:
  - port: metrics
    relabelings:
    - action: replace
      replacement: service
      targetLabel: microsoft_metrics_include_label
`
	overrides := map[string]string{"ServiceMonitor/kube-state-metrics-crs": "hcp"}
	assert.Empty(t, checkMetricsRoutingLabel(manifest, emptyAllowlist, overrides))
}

func TestCheckMetricsRoutingLabel_EveryEndpointChecked(t *testing.T) {
	// The first endpoint sets the label, the second does not.
	manifest := `
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: multi-endpoint
spec:
  endpoints:
  - port: metrics
    relabelings:
    - action: replace
      replacement: service
      targetLabel: microsoft_metrics_include_label
  - port: metrics-extra
`
	assert.Equal(t, []string{
		`ServiceMonitor/multi-endpoint spec.endpoints[1] is missing a target relabeling (relabelings) that sets microsoft_metrics_include_label=service; metrics without it are dropped by the filtered data collection rule, and setting it via metricRelabelings omits synthetic series like up (add to MetricsRoutingLabelAllowlist if intentional, or MetricsRoutingLabelOverrides to change the expected value)`,
	}, checkMetricsRoutingLabel(manifest, emptyAllowlist, nil))
}

func TestCheckMetricsRoutingLabel_Allowlisted(t *testing.T) {
	manifest := `
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: prometheus-operator
spec:
  endpoints:
  - port: http
`
	assert.Empty(t, checkMetricsRoutingLabel(manifest, []string{"ServiceMonitor/prometheus-operator"}, nil))
}

func TestCheckMetricsRoutingLabel_IgnoresOtherKinds(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    spec:
      containers:
      - name: web
`
	assert.Empty(t, checkMetricsRoutingLabel(manifest, emptyAllowlist, nil))
}
