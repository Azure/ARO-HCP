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
	"fmt"
	"io"
	"path"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// Every scraped series must carry microsoft_metrics_include_label=service so the
// services data collection rule's labelIncludeFilter forwards it to the Azure
// Monitor workspace. The services DCR is filtered, so a ServiceMonitor or
// PodMonitor that omits this relabeling produces metrics that are silently
// dropped from every workspace. This check guards that invariant across every
// rendered deployment.
//
// The label MUST be set via target relabeling (spec...endpoints[].relabelings,
// i.e. Prometheus relabel_configs), NOT metric relabeling (metricRelabelings /
// metric_relabel_configs). Prometheus does not run metric_relabel_configs over
// the synthetic per-target report series (up, scrape_duration_seconds,
// scrape_samples_scraped, ...); those are mutated only by target labels. A label
// set via metricRelabelings is therefore absent from `up`, so the filtered DCR
// drops `up` and target-health alerting (absent(up{...}), count(up == 1) == 0)
// silently stops working. Target relabeling applies to every series the target
// emits, including `up`.
//
// A monitor whose series are routed to a different data collection rule (for
// example the HCP DCR) must set a different replacement value; those are
// configured per-monitor via MetricsRoutingLabelOverrides.
const (
	metricsRoutingLabel        = "microsoft_metrics_include_label"
	metricsRoutingServiceValue = "service"
)

var monitorKinds = sets.New(
	"ServiceMonitor",
	"PodMonitor",
)

type relabeling struct {
	Action      string `json:"action"`
	Replacement string `json:"replacement"`
	TargetLabel string `json:"targetLabel"`
}

type monitorEndpoint struct {
	Relabelings []relabeling `json:"relabelings"`
}

type monitorResource struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Endpoints           []monitorEndpoint `json:"endpoints"`           // ServiceMonitor
		PodMetricsEndpoints []monitorEndpoint `json:"podMetricsEndpoints"` // PodMonitor
	} `json:"spec"`
}

// checkMetricsRoutingLabel returns a violation for every ServiceMonitor and
// PodMonitor endpoint that does not set microsoft_metrics_include_label to its
// expected value. The expected value defaults to "service", unless the monitor's
// "Kind/name" key matches a MetricsRoutingLabelOverrides pattern, in which case
// the configured override value is required instead. Monitors whose "Kind/name"
// key matches an allowlist pattern are skipped entirely (for example
// self-monitoring ServiceMonitors shipped by upstream subcharts).
func checkMetricsRoutingLabel(manifest string, allowlist []string, overrides map[string]string) []string {
	var violations []string

	// Validate allowlist and override patterns once so malformed globs don't
	// produce repeated errors per-monitor.
	for _, p := range allowlist {
		if _, err := path.Match(p, "validate"); err != nil {
			violations = append(violations, fmt.Sprintf("MetricsRoutingLabelAllowlist contains invalid pattern %q: %v", p, err))
			return violations
		}
	}
	for p := range overrides {
		if _, err := path.Match(p, "validate"); err != nil {
			violations = append(violations, fmt.Sprintf("MetricsRoutingLabelOverrides contains invalid pattern %q: %v", p, err))
			return violations
		}
	}

	decoder := utilyaml.NewYAMLToJSONDecoder(strings.NewReader(manifest))
	for {
		var m monitorResource
		if err := decoder.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			violations = append(violations, fmt.Sprintf("failed to decode manifest document: %v", err))
			continue
		}

		if !monitorKinds.Has(m.Kind) {
			continue
		}

		key := fmt.Sprintf("%s/%s", m.Kind, m.Metadata.Name)
		allowed, err := matchesAllowlist(key, allowlist)
		if err != nil {
			violations = append(violations, err.Error())
			continue
		}
		if allowed {
			continue
		}

		expected, err := expectedRoutingValue(key, overrides)
		if err != nil {
			violations = append(violations, err.Error())
			continue
		}

		endpoints := m.Spec.Endpoints
		field := "spec.endpoints"
		if m.Kind == "PodMonitor" {
			endpoints = m.Spec.PodMetricsEndpoints
			field = "spec.podMetricsEndpoints"
		}

		if len(endpoints) == 0 {
			violations = append(violations, fmt.Sprintf("%s has no %s to carry the %s=%s routing label (add to MetricsRoutingLabelAllowlist if intentional)", key, field, metricsRoutingLabel, expected))
			continue
		}

		for i, ep := range endpoints {
			if !endpointSetsLabel(ep, expected) {
				violations = append(violations, fmt.Sprintf("%s %s[%d] is missing a target relabeling (relabelings) that sets %s=%s; metrics without it are dropped by the filtered data collection rule, and setting it via metricRelabelings omits synthetic series like up (add to MetricsRoutingLabelAllowlist if intentional, or MetricsRoutingLabelOverrides to change the expected value)", key, field, i, metricsRoutingLabel, expected))
			}
		}
	}

	return violations
}

// expectedRoutingValue returns the microsoft_metrics_include_label replacement
// value a monitor must set. It is metricsRoutingServiceValue unless the monitor's
// "Kind/name" key matches an overrides pattern.
func expectedRoutingValue(key string, overrides map[string]string) (string, error) {
	for pattern, value := range overrides {
		matched, err := path.Match(pattern, key)
		if err != nil {
			return "", fmt.Errorf("MetricsRoutingLabelOverrides contains invalid pattern %q: %w", pattern, err)
		}
		if matched {
			return value, nil
		}
	}
	return metricsRoutingServiceValue, nil
}

func endpointSetsLabel(ep monitorEndpoint, value string) bool {
	setsLabel := false
	for _, r := range ep.Relabelings {
		// prometheus-operator defaults the relabeling action to "replace" when omitted.
		action := r.Action
		if action == "" {
			action = "replace"
		}
		if r.TargetLabel == metricsRoutingLabel {
			setsLabel = action == "replace" && r.Replacement == value
		}
	}
	return setsLabel
}
