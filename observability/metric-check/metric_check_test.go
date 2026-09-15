// Copyright 2025 Microsoft Corporation
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

package metriccheck_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/require"

	"sigs.k8s.io/yaml"
)

type metricRef struct {
	Metric string
	Source string
}

type dropRule struct {
	Component string
	Regex     *regexp.Regexp
	RawRegex  string
}

type cliConfig struct {
	PrometheusRules struct {
		RulesFolders  []string `json:"rulesFolders"`
		UntestedRules []string `json:"untestedRules,omitempty"`
	} `json:"prometheusRules"`
}

type relabelRule struct {
	Action       string   `yaml:"action"`
	Regex        string   `yaml:"regex"`
	SourceLabels []string `yaml:"sourceLabels"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../..")
	require.NoError(t, err)
	return abs
}

func extractMetricsFromPromQL(expr string) []string {
	p := parser.NewParser(parser.Options{})
	parsed, err := p.ParseExpr(expr)
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	parser.Inspect(parsed, func(node parser.Node, _ []parser.Node) error {
		vs, ok := node.(*parser.VectorSelector)
		if !ok {
			return nil
		}
		if vs.Name != "" {
			seen[vs.Name] = true
		}
		for _, m := range vs.LabelMatchers {
			if m.Name == "__name__" && m.Type == 0 {
				seen[m.Value] = true
			}
		}
		return nil
	})

	var metrics []string
	for m := range seen {
		metrics = append(metrics, m)
	}
	sort.Strings(metrics)
	return metrics
}

func collectAlertAndRecordingMetrics(t *testing.T, observabilityDir string) ([]metricRef, map[string]bool) {
	t.Helper()

	configPattern := filepath.Join(observabilityDir, "alerts-*.yaml")
	alertConfigs, err := filepath.Glob(configPattern)
	require.NoError(t, err)

	recordingPattern := filepath.Join(observabilityDir, "recording-rules-*.yaml")
	recordingConfigs, err := filepath.Glob(recordingPattern)
	require.NoError(t, err)

	allConfigs := append(alertConfigs, recordingConfigs...)

	processedFiles := map[string]bool{}
	var refs []metricRef
	recordingOutputs := map[string]bool{}

	for _, configPath := range allConfigs {
		raw, err := os.ReadFile(configPath)
		require.NoError(t, err, "reading config %s", configPath)

		var cfg cliConfig
		require.NoError(t, yaml.Unmarshal(raw, &cfg), "parsing config %s", configPath)

		baseDir := filepath.Dir(configPath)
		allRulePaths := append(cfg.PrometheusRules.RulesFolders, cfg.PrometheusRules.UntestedRules...)

		for _, rulePath := range allRulePaths {
			fullPath := filepath.Join(baseDir, rulePath)
			absPath, err := filepath.Abs(fullPath)
			require.NoError(t, err)

			if processedFiles[absPath] {
				continue
			}
			processedFiles[absPath] = true

			rawRule, err := os.ReadFile(absPath)
			require.NoError(t, err, "reading rule file %s", absPath)

			var rule monitoringv1.PrometheusRule
			require.NoError(t, yaml.Unmarshal(rawRule, &rule), "parsing rule file %s", absPath)

			relPath, _ := filepath.Rel(observabilityDir, absPath)
			if relPath == "" {
				relPath = absPath
			}

			for _, group := range rule.Spec.Groups {
				for _, r := range group.Rules {
					if r.Record != "" {
						recordingOutputs[r.Record] = true
					}

					exprStr := r.Expr.String()
					metrics := extractMetricsFromPromQL(exprStr)

					source := ""
					if r.Alert != "" {
						source = fmt.Sprintf("alert:%s (in %s)", r.Alert, relPath)
					} else if r.Record != "" {
						source = fmt.Sprintf("recording-rule:%s (in %s)", r.Record, relPath)
					}

					for _, m := range metrics {
						refs = append(refs, metricRef{Metric: m, Source: source})
					}
				}
			}
		}
	}

	return refs, recordingOutputs
}

func loadDropRules(t *testing.T, root string) []dropRule {
	t.Helper()

	configPath := filepath.Join(root, "hypershiftoperator/deploy/templates/sre-metrics-set.configmap.yaml")
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)

	content := strings.ReplaceAll(string(raw), "{{ .Release.Namespace }}", "placeholder")

	var cm struct {
		Data map[string]string `yaml:"data"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(content), &cm))

	configYAML, ok := cm.Data["config"]
	require.True(t, ok, "sre-metrics-set ConfigMap missing 'config' key")

	var components map[string][]relabelRule
	require.NoError(t, yaml.Unmarshal([]byte(configYAML), &components))

	var rules []dropRule
	for component, relabels := range components {
		for _, rl := range relabels {
			if strings.ToLower(rl.Action) != "drop" {
				continue
			}
			hasNameLabel := false
			for _, sl := range rl.SourceLabels {
				if sl == "__name__" {
					hasNameLabel = true
					break
				}
			}
			if !hasNameLabel {
				continue
			}
			compiled, err := regexp.Compile("^(" + rl.Regex + ")$")
			require.NoError(t, err, "compiling drop regex %q for component %s", rl.Regex, component)
			rules = append(rules, dropRule{
				Component: component,
				Regex:     compiled,
				RawRegex:  rl.Regex,
			})
		}
	}

	require.NotEmpty(t, rules, "expected at least one drop rule in sre-metrics-set")
	return rules
}

func TestMetricsNotDropped(t *testing.T) {
	root := repoRoot(t)
	observabilityDir := filepath.Join(root, "observability")

	alertRefs, recordingOutputs := collectAlertAndRecordingMetrics(t, observabilityDir)
	rules := loadDropRules(t, root)

	type violation struct {
		metric    string
		sources   []string
		component string
		dropRegex string
	}

	var violations []violation
	checked := map[string]bool{}

	for _, ref := range alertRefs {
		if recordingOutputs[ref.Metric] {
			continue
		}

		for _, rule := range rules {
			key := ref.Metric + "|" + rule.Component + "|" + rule.RawRegex
			if checked[key] {
				continue
			}

			if rule.Regex.MatchString(ref.Metric) {
				checked[key] = true

				var sources []string
				seen := map[string]bool{}
				for _, r := range alertRefs {
					if r.Metric == ref.Metric && !seen[r.Source] {
						sources = append(sources, r.Source)
						seen[r.Source] = true
					}
				}

				violations = append(violations, violation{
					metric:    ref.Metric,
					sources:   sources,
					component: rule.Component,
					dropRegex: rule.RawRegex,
				})
			}
		}
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].component != violations[j].component {
			return violations[i].component < violations[j].component
		}
		return violations[i].metric < violations[j].metric
	})

	for _, v := range violations {
		t.Errorf("metric %q is dropped by %s (regex %q) but used by:\n  %s",
			v.metric, v.component, v.dropRegex, strings.Join(v.sources, "\n  "))
	}
}
