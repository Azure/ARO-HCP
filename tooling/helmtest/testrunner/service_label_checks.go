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
const (
	metricsRoutingLabel        = "microsoft_metrics_include_label"
	metricsRoutingServiceValue = "service"
)

var monitorKinds = sets.New(
	"ServiceMonitor",
	"PodMonitor",
)

type metricRelabeling struct {
	Action      string `json:"action"`
	Replacement string `json:"replacement"`
	TargetLabel string `json:"targetLabel"`
}

type monitorEndpoint struct {
	MetricRelabelings []metricRelabeling `json:"metricRelabelings"`
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
// PodMonitor endpoint that does not set microsoft_metrics_include_label=service.
// Monitors whose "Kind/name" key matches an allowlist pattern are skipped (for
// example self-monitoring ServiceMonitors shipped by upstream subcharts).
func checkMetricsRoutingLabel(manifest string, allowlist []string) []string {
	var violations []string

	// Validate allowlist patterns once so malformed globs don't produce repeated
	// errors per-monitor.
	for _, p := range allowlist {
		if _, err := path.Match(p, "validate"); err != nil {
			violations = append(violations, fmt.Sprintf("MetricsRoutingLabelAllowlist contains invalid pattern %q: %v", p, err))
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

		endpoints := m.Spec.Endpoints
		field := "spec.endpoints"
		if m.Kind == "PodMonitor" {
			endpoints = m.Spec.PodMetricsEndpoints
			field = "spec.podMetricsEndpoints"
		}

		if len(endpoints) == 0 {
			violations = append(violations, fmt.Sprintf("%s has no %s to carry the %s=%s routing label (add to MetricsRoutingLabelAllowlist if intentional)", key, field, metricsRoutingLabel, metricsRoutingServiceValue))
			continue
		}

		for i, ep := range endpoints {
			if !endpointSetsServiceLabel(ep) {
				violations = append(violations, fmt.Sprintf("%s %s[%d] is missing a metricRelabeling that sets %s=%s; metrics without it are dropped by the filtered services data collection rule (add to MetricsRoutingLabelAllowlist if intentional)", key, field, i, metricsRoutingLabel, metricsRoutingServiceValue))
			}
		}
	}

	return violations
}

func endpointSetsServiceLabel(ep monitorEndpoint) bool {
	for _, r := range ep.MetricRelabelings {
		// prometheus-operator defaults the relabeling action to "replace" when omitted.
		action := r.Action
		if action == "" {
			action = "replace"
		}
		if action == "replace" && r.TargetLabel == metricsRoutingLabel && r.Replacement == metricsRoutingServiceValue {
			return true
		}
	}
	return false
}
