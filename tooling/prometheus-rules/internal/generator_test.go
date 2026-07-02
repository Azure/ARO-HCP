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

package internal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/prometheusrulegroups/armprometheusrulegroups"
)

func TestNewOptions(t *testing.T) {
	opts := NewOptions()
	assert.NotNil(t, opts)
	assert.Empty(t, opts.ruleFiles)
	assert.Empty(t, opts.outputBicep)
}

func TestReadRulesFile(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid rules file",
			content: `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: test-rules
spec:
  groups:
  - name: test.rules
    rules:
    - alert: TestAlert
      expr: up == 0
      for: 5m
`,
			expectError: false,
		},
		{
			name: "no groups in spec",
			content: `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: test-rules
spec: {}
`,
			expectError: true,
			errorMsg:    "no groups found",
		},
		{
			name:        "invalid yaml",
			content:     "invalid: yaml: content:",
			expectError: true,
			errorMsg:    "failed to parse input rules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := filepath.Join(t.TempDir(), "test.yaml")
			require.NoError(t, os.WriteFile(tmpFile, []byte(tt.content), 0644))

			rules, err := readRulesFile(tmpFile)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, rules)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, rules)
				assert.NotEmpty(t, rules.Spec.Groups)
			}
		})
	}

	t.Run("file not found", func(t *testing.T) {
		rules, err := readRulesFile("nonexistent.yaml")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read input rules")
		assert.Nil(t, rules)
	})
}

func TestOptionsComplete(t *testing.T) {
	tests := []struct {
		name         string
		configFile   string
		setupFiles   func(tmpDir string) error
		expectError  bool
		errorMsg     string
		validateFunc func(t *testing.T, opts *Options)
	}{
		{
			name: "valid config with rules folders",
			configFile: `
prometheusRules:
  rulesFolders:
  - alerts
  outputBicep: generated.bicep
`,
			setupFiles: func(tmpDir string) error {
				alertsDir := filepath.Join(tmpDir, "alerts")
				if err := os.Mkdir(alertsDir, 0755); err != nil {
					return err
				}

				ruleContent := `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: test-rules
spec:
  groups:
  - name: test.rules
    rules:
    - alert: TestAlert
      expr: up == 0
`
				testContent := `
rule_files:
- test.yaml
tests:
- interval: 1m
  input_series:
  - series: up
    values: '0 0 0'
  alert_rule_test:
  - eval_time: 5m
    alertname: TestAlert
    exp_alerts:
    - exp_labels:
        severity: critical
`
				if err := os.WriteFile(filepath.Join(alertsDir, "test.yaml"), []byte(ruleContent), 0644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(alertsDir, "test_test.yaml"), []byte(testContent), 0644)
			},
			expectError: false,
			validateFunc: func(t *testing.T, opts *Options) {
				assert.Len(t, opts.ruleFiles, 1)
				assert.Contains(t, opts.outputBicep, "generated.bicep")
			},
		},
		{
			name: "untested rules",
			configFile: `
prometheusRules:
  untestedRules:
  - untested.yaml
  outputBicep: generated.bicep
`,
			setupFiles: func(tmpDir string) error {
				ruleContent := `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: untested-rules
spec:
  groups:
  - name: untested.rules
    rules:
    - alert: UntestedAlert
      expr: up == 0
`
				return os.WriteFile(filepath.Join(tmpDir, "untested.yaml"), []byte(ruleContent), 0644)
			},
			expectError: false,
			validateFunc: func(t *testing.T, opts *Options) {
				assert.Len(t, opts.ruleFiles, 1)
				assert.Equal(t, "", opts.ruleFiles[0].TestFileBaseName)
			},
		},
		{
			name: "invalid regex replace",
			configFile: `
prometheusRules:
  untestedRules:
  - untested.yaml
  outputBicep: generated.bicep
  regexOutputReplacements:
  - from: '(badRegex'
    to: 'replacement'
`,
			expectError: true,
			errorMsg:    "invalid regex in regexOutputReplacements",
		},
		{
			name: "valid regex",
			configFile: `
prometheusRules:
  untestedRules:
  - untested.yaml
  outputBicep: generated.bicep
  regexOutputReplacements:
  - from: 'good(.+)t'
    to: 'great$1t'
`,
			setupFiles: func(tmpDir string) error {
				ruleContent := `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: untested-rules
spec:
  groups:
  - name: untested.rules
    rules:
    - alert: goodAlert
      expr: up == 0
`
				return os.WriteFile(filepath.Join(tmpDir, "untested.yaml"), []byte(ruleContent), 0644)
			},
			expectError: false,
			validateFunc: func(t *testing.T, opts *Options) {
				assert.Len(t, opts.regexOutputReplacements, 1)
				assert.Equal(t, "good(.+)t", opts.regexOutputReplacements[0].From.String())
				assert.Equal(t, "great$1t", opts.regexOutputReplacements[0].To)
			},
		},
		{
			name:        "config file not found",
			expectError: true,
			errorMsg:    "error reading configuration file",
		},
		{
			name:        "invalid config yaml",
			configFile:  `invalid: yaml: content:`,
			expectError: true,
			errorMsg:    "error unmarshaling configFile",
		},
		{
			name: "config with labelsToExtract",
			configFile: `
prometheusRules:
  untestedRules:
  - untested.yaml
  outputBicep: generated.bicep
  labelsToExtract:
  - namespace
  - pod
  - container
`,
			setupFiles: func(tmpDir string) error {
				ruleContent := `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: test-rules
spec:
  groups:
  - name: test.rules
    rules:
    - alert: IncludedAlert
      expr: up == 0
`
				return os.WriteFile(filepath.Join(tmpDir, "untested.yaml"), []byte(ruleContent), 0644)
			},
			expectError: false,
			validateFunc: func(t *testing.T, opts *Options) {
				require.Equal(t, []string{"namespace", "pod", "container"}, opts.labelsToExtract)
			},
		},
		{
			name: "config with includedAlertsByGroup",
			configFile: `
prometheusRules:
  untestedRules:
  - untested.yaml
  outputBicep: generated.bicep
  includedAlertsByGroup:
  - groupName: test.rules
    alerts:
    - IncludedAlert
    - AnotherIncludedAlert
`,
			setupFiles: func(tmpDir string) error {
				ruleContent := `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: test-rules
spec:
  groups:
  - name: test.rules
    rules:
    - alert: IncludedAlert
      expr: up == 0
`
				return os.WriteFile(filepath.Join(tmpDir, "untested.yaml"), []byte(ruleContent), 0644)
			},
			expectError: false,
			validateFunc: func(t *testing.T, opts *Options) {
				assert.Len(t, opts.includedAlerts, 1)
				assert.Contains(t, opts.includedAlerts, "test.rules")
				assert.Len(t, opts.includedAlerts["test.rules"], 2)
				assert.Contains(t, opts.includedAlerts["test.rules"], "IncludedAlert")
				assert.Contains(t, opts.includedAlerts["test.rules"], "AnotherIncludedAlert")
			},
		},
		{
			name: "config with includedAlertsByGroup (multiple groups)",
			configFile: `
prometheusRules:
  untestedRules:
  - untested.yaml
  outputBicep: generated.bicep
  includedAlertsByGroup:
  - groupName: test.rules
    alerts:
    - IncludedAlert
    - AnotherIncludedAlert
  - groupName: other.rules
    alerts:
    - OtherAlert
`,
			setupFiles: func(tmpDir string) error {
				ruleContent := `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: test-rules
spec:
  groups:
  - name: test.rules
    rules:
    - alert: IncludedAlert
      expr: up == 0
`
				return os.WriteFile(filepath.Join(tmpDir, "untested.yaml"), []byte(ruleContent), 0644)
			},
			expectError: false,
			validateFunc: func(t *testing.T, opts *Options) {
				assert.Len(t, opts.includedAlerts, 2)
				assert.Contains(t, opts.includedAlerts, "test.rules")
				assert.Len(t, opts.includedAlerts["test.rules"], 2)
				assert.Contains(t, opts.includedAlerts["test.rules"], "IncludedAlert")
				assert.Contains(t, opts.includedAlerts["test.rules"], "AnotherIncludedAlert")
				assert.Contains(t, opts.includedAlerts, "other.rules")
				assert.Len(t, opts.includedAlerts["other.rules"], 1)
				assert.Contains(t, opts.includedAlerts["other.rules"], "OtherAlert")
			},
		},
		{
			name: "config with includedAlertsByGroup and namespaces",
			configFile: `
prometheusRules:
  untestedRules:
  - untested.yaml
  outputBicep: generated.bicep
  includedAlertsByGroup:
  - groupName: kubernetes-resources
    namespaces:
    - aro-hcp
    - clusters-service
    alerts:
    - KubeQuotaAlmostFull
  - groupName: kubernetes-system
    alerts:
    - KubeClientErrors
`,
			setupFiles: func(tmpDir string) error {
				ruleContent := `
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: test-rules
spec:
  groups:
  - name: kubernetes-resources
    rules:
    - alert: KubeQuotaAlmostFull
      expr: up == 0
`
				return os.WriteFile(filepath.Join(tmpDir, "untested.yaml"), []byte(ruleContent), 0644)
			},
			expectError: false,
			validateFunc: func(t *testing.T, opts *Options) {
				assert.Len(t, opts.includedAlerts, 2)
				assert.Len(t, opts.namespaceFilters, 1)
				assert.Contains(t, opts.namespaceFilters, "kubernetes-resources")
				assert.Equal(t, []string{"aro-hcp", "clusters-service"}, opts.namespaceFilters["kubernetes-resources"])
				// Group without namespaces should not appear in namespaceFilters
				assert.NotContains(t, opts.namespaceFilters, "kubernetes-system")
			},
		},
		{
			name:       "config with explicit deps for promtool",
			configFile: "prometheusRules:\n  rulesFolders:\n  - alerts\n  testDependencies:\n  - recording-rules.yaml\n  outputBicep: generated.bicep\n",
			setupFiles: func(tmpDir string) error {
				alertsDir := filepath.Join(tmpDir, "alerts")
				if err := os.Mkdir(alertsDir, 0755); err != nil {
					return err
				}
				recDir := filepath.Join(tmpDir, "recording")
				if err := os.Mkdir(recDir, 0755); err != nil {
					return err
				}

				ruleContent := "apiVersion: monitoring.coreos.com/v1\nkind: PrometheusRule\nmetadata:\n  name: test-rules\nspec:\n  groups:\n  - name: test.rules\n    rules:\n    - alert: TestAlert\n      expr: up == 0\n"
				testContent := "rule_files:\n- test.yaml\ntests: []\n"
				recordContent := "apiVersion: monitoring.coreos.com/v1\nkind: PrometheusRule\nmetadata:\n  name: recording-rules\nspec:\n  groups:\n  - name: recording.rules\n    rules:\n    - record: my_recording_rule\n      expr: sum(up)\n"
				recConfig := "prometheusRules:\n  rulesFolders:\n  - recording/record.yaml\n  untestedRules: []\n  outputBicep: out.bicep\n"

				if err := os.WriteFile(filepath.Join(alertsDir, "test.yaml"), []byte(ruleContent), 0644); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(alertsDir, "test_test.yaml"), []byte(testContent), 0644); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(recDir, "record.yaml"), []byte(recordContent), 0644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(tmpDir, "recording-rules.yaml"), []byte(recConfig), 0644)
			},
			expectError: false,
			validateFunc: func(t *testing.T, opts *Options) {
				assert.Len(t, opts.ruleFiles, 2)
				var hasDep bool
				for _, rf := range opts.ruleFiles {
					if rf.testDependency {
						hasDep = true
						assert.Equal(t, "record.yaml", rf.FileBaseName)
						assert.Empty(t, rf.TestFileBaseName)
					}
				}
				assert.True(t, hasDep, "expected a test dependency rule file")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")

			if tt.configFile != "" {
				require.NoError(t, os.WriteFile(configPath, []byte(tt.configFile), 0644))
			}

			if tt.setupFiles != nil {
				require.NoError(t, tt.setupFiles(tmpDir))
			}

			opts := NewOptions()
			err := opts.Complete(configPath, "promtool", false)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				if tt.validateFunc != nil {
					tt.validateFunc(t, opts)
				}
			}
		})
	}
}

func TestOptionsRunTests(t *testing.T) {
	t.Run("no test files", func(t *testing.T) {
		opts := &Options{
			ruleFiles: []alertingRuleFile{
				{
					FileBaseName: "test.yaml",
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{Name: "test"},
							},
						},
					},
				},
			},
		}

		err := opts.RunTests()
		assert.NoError(t, err)
	})

	t.Run("with test files but no promtool", func(t *testing.T) {
		opts := &Options{
			promtoolPath: "definitely-not-a-real-promtool",
			ruleFiles: []alertingRuleFile{
				{
					FileBaseName:     "test.yaml",
					TestFileBaseName: "test_test.yaml",
					TestFileContent:  []byte("test content"),
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{Name: "test"},
							},
						},
					},
				},
			},
		}

		err := opts.RunTests()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error running promtool")
	})
}

func TestOptionsGenerate(t *testing.T) {
	t.Run("basic generation", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "AlertingRules_output.bicep")

		opts := &Options{
			outputBicep: outputFile,
			ruleFiles: []alertingRuleFile{
				{
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name:     "test.rules",
									Interval: (*monitoringv1.Duration)(ptr.To("30s")),
									Rules: []monitoringv1.Rule{
										{
											Alert: "TestAlert",
											Expr:  intstr.FromString("up == 0"),
											For:   (*monitoringv1.Duration)(ptr.To("5m")),
											Labels: map[string]string{
												"severity": "critical",
											},
											Annotations: map[string]string{
												"summary": "Test alert",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := opts.Generate()
		assert.NoError(t, err)

		content, err := os.ReadFile(outputFile)
		assert.NoError(t, err)

		generated := string(content)
		assert.Contains(t, generated, "#disable-next-line no-unused-params")
		assert.Contains(t, generated, "param azureMonitoring string")
		assert.Contains(t, generated, "param actionGroups array")
		assert.Contains(t, generated, "param severityCeiling int = 0")
		assert.Contains(t, generated, "Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01")
		assert.Contains(t, generated, "alert: 'TestAlert'")
		assert.Contains(t, generated, "severity: severityCeiling > 0 ? max(2, severityCeiling) : 2")
	})

	t.Run("with included alerts", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "generatedAlertingRules.bicep")

		opts := &Options{
			outputBicep: outputFile,
			includedAlerts: map[string][]string{
				"test-group": {"AllowedAlert"},
			},
			ruleFiles: []alertingRuleFile{
				{
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name: "test-group",
									Rules: []monitoringv1.Rule{
										{
											Alert: "AllowedAlert",
											Expr:  intstr.FromString("up == 0"),
											Labels: map[string]string{
												"severity": "critical",
											},
										},
										{
											Alert: "BlockedAlert",
											Expr:  intstr.FromString("down == 1"),
											Labels: map[string]string{
												"severity": "warning",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := opts.Generate()
		assert.NoError(t, err)

		content, err := os.ReadFile(outputFile)
		assert.NoError(t, err)

		generated := string(content)
		assert.Contains(t, generated, "alert: 'AllowedAlert'")
		assert.NotContains(t, generated, "alert: 'BlockedAlert'")
	})

	t.Run("preserves per-alert correlationId override", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "generatedAlertingRules.bicep")

		opts := &Options{
			outputBicep: outputFile,
			ruleFiles: []alertingRuleFile{
				{
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name: "hcp-hostedcluster-monitor-rules",
									Rules: []monitoringv1.Rule{
										{
											Alert: "hostedcluster-KubeAPIServer-ErrorBudgetBurn",
											Expr:  intstr.FromString("up == 0"),
											Labels: map[string]string{
												"severity": "info",
											},
											Annotations: map[string]string{
												"summary":       "High KubeAPIServer error budget burn for HostedCluster {{ $labels.name }}",
												"correlationId": "hostedcluster-KubeAPIServer-ErrorBudgetBurn/{{ $labels.cluster }}/{{ $labels._id }}",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := opts.Generate()
		assert.NoError(t, err)

		content, err := os.ReadFile(outputFile)
		assert.NoError(t, err)

		generated := string(content)
		assert.Contains(t, generated, "correlationId: 'hostedcluster-KubeAPIServer-ErrorBudgetBurn/{{ $labels.cluster }}/{{ $labels._id }}'")
		assert.NotContains(t, generated, "correlationId: 'hostedcluster-KubeAPIServer-ErrorBudgetBurn/{{ $labels.cluster }}'")
	})

	t.Run("enriches default correlationId and summary title using labelsToExtract", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "generatedAlertingRules.bicep")

		opts := &Options{
			outputBicep:     outputFile,
			labelsToExtract: []string{"cluster", "namespace", "pod", "container"},
			ruleFiles: []alertingRuleFile{
				{
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name: "test-group",
									Rules: []monitoringv1.Rule{
										{
											Alert: "EnrichedAlert",
											Expr:  intstr.FromString("up == 0"),
											Labels: map[string]string{
												"severity": "warning",
											},
											Annotations: map[string]string{
												"summary":     "Pod in namespace {{ $labels.namespace }} is unhealthy",
												"description": "Pod {{ $labels.namespace }}/{{ $labels.pod }} has issues",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := opts.Generate()
		assert.NoError(t, err)

		content, err := os.ReadFile(outputFile)
		assert.NoError(t, err)
		generated := string(content)

		assert.Contains(t, generated, "correlationId: 'EnrichedAlert/{{ $labels.cluster }}/{{ $labels.namespace }}/{{ $labels.pod }}'")
		assert.NotContains(t, generated, "{{ $labels.cluster }}/{{ $labels.cluster }}")
		assert.Contains(t, generated, "title: 'Pod in namespace {{ $labels.namespace }} is unhealthy pod:{{ $labels.pod }}'")
		assert.NotContains(t, generated, "namespace: {{ $labels.namespace }}")
	})

	t.Run("does not enrich title when summary is absent", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "generatedAlertingRules.bicep")

		opts := &Options{
			outputBicep:     outputFile,
			labelsToExtract: []string{"namespace", "pod"},
			ruleFiles: []alertingRuleFile{
				{
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name: "test-group",
									Rules: []monitoringv1.Rule{
										{
											Alert: "NoSummaryAlert",
											Expr:  intstr.FromString("up == 0"),
											Labels: map[string]string{
												"severity": "warning",
											},
											Annotations: map[string]string{
												"description": "Pod {{ $labels.namespace }}/{{ $labels.pod }} has issues",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := opts.Generate()
		assert.NoError(t, err)

		content, err := os.ReadFile(outputFile)
		assert.NoError(t, err)
		generated := string(content)

		assert.Contains(t, generated, "title: 'NoSummaryAlert'")
		assert.NotContains(t, generated, "title: 'NoSummaryAlert namespace:")
	})

	t.Run("deps excluded from output", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "AlertingRules_output.bicep")

		opts := &Options{
			outputBicep: outputFile,
			ruleFiles: []alertingRuleFile{
				{
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name: "alert-group",
									Rules: []monitoringv1.Rule{
										{
											Alert: "RealAlert",
											Expr:  intstr.FromString("up == 0"),
											Labels: map[string]string{
												"severity": "critical",
											},
										},
									},
								},
							},
						},
					},
				},
				{
					FileBaseName:   "recording.yaml",
					testDependency: true,
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name: "recording-group",
									Rules: []monitoringv1.Rule{
										{
											Alert: "DependencyAlert",
											Expr:  intstr.FromString("sum(up)"),
											Labels: map[string]string{
												"severity": "warning",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := opts.Generate()
		assert.NoError(t, err)

		content, err := os.ReadFile(outputFile)
		assert.NoError(t, err)
		generated := string(content)

		assert.Contains(t, generated, "alert: 'RealAlert'")
		assert.NotContains(t, generated, "DependencyAlert")
	})

	t.Run("with namespace filter", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "generatedAlertingRules.bicep")

		opts := &Options{
			outputBicep: outputFile,
			includedAlerts: map[string][]string{
				"test-group": {"QuotaAlert"},
			},
			namespaceFilters: map[string][]string{
				"test-group": {"aro-hcp", "clusters-service"},
			},
			ruleFiles: []alertingRuleFile{
				{
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name: "test-group",
									Rules: []monitoringv1.Rule{
										{
											Alert: "QuotaAlert",
											Expr:  intstr.FromString(`kube_resourcequota{job="kube-state-metrics", type="used"} > 0.9`),
											Labels: map[string]string{
												"severity": "warning",
											},
											Annotations: map[string]string{
												"summary": "Quota almost full",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := opts.Generate()
		assert.NoError(t, err)

		content, err := os.ReadFile(outputFile)
		assert.NoError(t, err)
		generated := string(content)

		assert.Contains(t, generated, "alert: 'QuotaAlert'")
		assert.Contains(t, generated, `namespace=~"aro-hcp|clusters-service"`)
	})

	t.Run("namespace filter does not modify selectors with existing namespace matcher", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "generatedAlertingRules.bicep")

		opts := &Options{
			outputBicep: outputFile,
			includedAlerts: map[string][]string{
				"test-group": {"ScopedAlert"},
			},
			namespaceFilters: map[string][]string{
				"test-group": {"aro-hcp"},
			},
			ruleFiles: []alertingRuleFile{
				{
					Rules: monitoringv1.PrometheusRule{
						Spec: monitoringv1.PrometheusRuleSpec{
							Groups: []monitoringv1.RuleGroup{
								{
									Name: "test-group",
									Rules: []monitoringv1.Rule{
										{
											Alert: "ScopedAlert",
											Expr:  intstr.FromString(`up{namespace="already-scoped"}`),
											Labels: map[string]string{
												"severity": "warning",
											},
											Annotations: map[string]string{
												"summary": "Already scoped alert",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		err := opts.Generate()
		assert.NoError(t, err)

		content, err := os.ReadFile(outputFile)
		assert.NoError(t, err)
		generated := string(content)

		assert.Contains(t, generated, `namespace="already-scoped"`)
		assert.NotContains(t, generated, `namespace=~"aro-hcp"`)
	})
}

func TestWriteGroups(t *testing.T) {
	group := armprometheusrulegroups.PrometheusRuleGroupResource{
		Name: ptr.To("test-group"),
		Properties: &armprometheusrulegroups.PrometheusRuleGroupProperties{
			Interval: ptr.To("PT30S"),
			Enabled:  ptr.To(true),
			Rules: []*armprometheusrulegroups.PrometheusRule{
				{
					Alert:   ptr.To("TestAlert"),
					Enabled: ptr.To(true),
					Labels: map[string]*string{
						"severity": ptr.To("critical"),
					},
					Annotations: map[string]*string{
						"summary":     ptr.To("Test summary"),
						"description": ptr.To("Multi\nline\ndescription"),
					},
					Expression: ptr.To("up == 0"),
					For:        ptr.To("PT5M"),
					Severity:   ptr.To(int32(2)),
				},
			},
		},
	}

	var buf bytes.Buffer
	err := writeAlertGroups(group, &buf)
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "resource testGroup 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01'")
	assert.Contains(t, output, "name: 'test-group'")
	assert.Contains(t, output, "alert: 'TestAlert'")
	assert.Contains(t, output, "severity: 'critical'")
	assert.Contains(t, output, "'''Multi\nline\ndescription'''")
}

func TestBicepName(t *testing.T) {
	tests := []struct {
		input    *string
		expected string
	}{
		{ptr.To("test-group"), "testGroup"},
		{ptr.To("test.group"), "testGroup"},
		{ptr.To("test_group"), "testGroup"},
		{ptr.To("test-group-name"), "testGroupName"},
		{ptr.To("simple"), "simple"},
		{nil, "FIXME-NAME-NIL"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("input_%v", tt.input), func(t *testing.T) {
			result := bicepName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseToAzureDurationString(t *testing.T) {
	tests := []struct {
		input    *monitoringv1.Duration
		expected *string
	}{
		{nil, nil},
		{(*monitoringv1.Duration)(ptr.To("30s")), ptr.To("PT1M")}, // too short, gets default
		{(*monitoringv1.Duration)(ptr.To("5m")), ptr.To("PT5M")},
		{(*monitoringv1.Duration)(ptr.To("1h")), ptr.To("PT1H")},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("input_%v", tt.input), func(t *testing.T) {
			result := parseToAzureDurationString(tt.input)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, *tt.expected, *result)
			}
		})
	}
}

func TestSeverityFor(t *testing.T) {
	tests := []struct {
		labels    map[string]*string
		expected  *int32
		expectErr bool
	}{
		// Canonical Azure CEN vocabulary: the severity label is the IcM Sev number.
		{map[string]*string{"severity": ptr.To("2")}, ptr.To(int32(2)), false},
		{map[string]*string{"severity": ptr.To("2.5")}, ptr.To(int32(25)), false},
		{map[string]*string{"severity": ptr.To("25")}, ptr.To(int32(25)), false},
		{map[string]*string{"severity": ptr.To("3")}, ptr.To(int32(3)), false},
		{map[string]*string{"severity": ptr.To("4")}, ptr.To(int32(4)), false},
		// Deprecated vocabulary, still accepted.
		{map[string]*string{"severity": ptr.To("critical")}, ptr.To(int32(2)), false},
		{map[string]*string{"severity": ptr.To("warning")}, ptr.To(int32(3)), false},
		{map[string]*string{"severity": ptr.To("info")}, ptr.To(int32(4)), false},
		// "1" (Sev 1) is rejected: Azure CEN reserves Sev 1 for declared incidents.
		{map[string]*string{"severity": ptr.To("1")}, nil, true},
		// Unknown values fail fast instead of silently defaulting to Sev 4.
		{map[string]*string{"severity": ptr.To("unknown")}, nil, true},
		// No severity label: nil, no error.
		{map[string]*string{}, nil, false},
		{map[string]*string{"other": ptr.To("value")}, nil, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("labels_%v", tt.labels), func(t *testing.T) {
			result, err := severityFor(tt.labels)
			if tt.expectErr {
				require.Error(t, err)
				assert.Nil(t, result)
				return
			}
			require.NoError(t, err)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, *tt.expected, *result)
			}
		})
	}
}
