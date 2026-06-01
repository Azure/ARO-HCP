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
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	"k8s.io/utils/ptr"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/prometheusrulegroups/armprometheusrulegroups"
)

var defaultEvaluationInterval = "1m"

type alertingRuleFile struct {
	DefaultEvaluationInterval string
	FolderName                string
	FileBaseName              string
	TestFileBaseName          string
	Rules                     monitoringv1.PrometheusRule
	TestFileContent           []byte
}

type GroupAlerts struct {
	GroupName string   `json:"groupName"`
	Alerts    []string `json:"alerts"`
}

type Options struct {
	promtoolPath            string
	outputBicep             string
	includedAlerts          map[string][]string
	ruleFiles               []alertingRuleFile
	outputReplacements      []Replacements
	regexOutputReplacements []RegexReplacements
	groupNamePrefix         string
}

type PrometheusRulesConfig struct {
	RulesFolders              []string       `json:"rulesFolders"`
	UntestedRules             []string       `json:"untestedRules,omitempty"`
	OutputBicep               string         `json:"outputBicep"`
	IncludedAlertsByGroup     []GroupAlerts  `json:"includedAlertsByGroup,omitempty"` // Optional: Only alerts listed here are included; if empty, all alerts are included
	OutputReplacements        []Replacements `json:"outputReplacements,omitempty"`
	RegexOutputReplacements   []Replacements `json:"regexOutputReplacements,omitempty"`
	DefaultEvaluationInterval string         `json:"defaultEvaluationInterval,omitempty"`
	GroupNamePrefix           string         `json:"groupNamePrefix,omitempty"`
}

type CliConfig struct {
	PrometheusRules PrometheusRulesConfig `json:"prometheusRules"`
}

func NewOptions() *Options {
	o := &Options{}
	return o
}

func readRulesFile(filename string) (*monitoringv1.PrometheusRule, error) {
	rawRules, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read input rules: %v", err)
	}
	var rules monitoringv1.PrometheusRule
	if err := yaml.Unmarshal(rawRules, &rules); err != nil {
		return nil, fmt.Errorf("failed to parse input rules: %v", err)
	}

	if rules.Spec.Groups == nil {
		return nil, fmt.Errorf("no groups found in rules file %s", filename)
	}

	return &rules, nil
}

func (o *Options) Complete(configFilePath string, promtoolPath string) error {
	if promtoolPath == "" {
		return fmt.Errorf("promtoolPath cannot be an empty string")
	}
	o.promtoolPath = promtoolPath

	o.ruleFiles = make([]alertingRuleFile, 0)

	cfgRaw, err := os.ReadFile(configFilePath)
	if err != nil {
		return fmt.Errorf("error reading configuration file %v", err)
	}

	baseDirectory := path.Dir(configFilePath)

	config := &CliConfig{}
	err = yaml.Unmarshal(cfgRaw, config)
	if err != nil {
		return fmt.Errorf("error unmarshaling configFile %s file %v", configFilePath, err)
	}

	o.outputReplacements = config.PrometheusRules.OutputReplacements
	for _, replacement := range o.outputReplacements {
		if replacement.From == "" || replacement.To == "" {
			return fmt.Errorf("expression replacement must have both from and to fields (from=%q, to=%q)", replacement.From, replacement.To)
		}
	}

	o.regexOutputReplacements = make([]RegexReplacements, len(config.PrometheusRules.RegexOutputReplacements))
	for i, regexReplacement := range config.PrometheusRules.RegexOutputReplacements {
		if regexReplacement.From == "" || regexReplacement.To == "" {
			return fmt.Errorf("regex expression replacement must have both from and to fields (from=%q, to=%q)", regexReplacement.From, regexReplacement.To)
		}
		compiledRegex, err := regexp.Compile(regexReplacement.From)
		if err != nil {
			return fmt.Errorf("invalid regex in regexOutputReplacements: %w", err)
		}
		o.regexOutputReplacements[i] = RegexReplacements{
			From: compiledRegex,
			To:   regexReplacement.To,
		}
	}

	o.outputBicep = path.Join(baseDirectory, config.PrometheusRules.OutputBicep)
	o.groupNamePrefix = config.PrometheusRules.GroupNamePrefix

	// Convert includedAlertsByGroup to a map
	o.includedAlerts = make(map[string][]string)
	for _, ga := range config.PrometheusRules.IncludedAlertsByGroup {
		o.includedAlerts[ga.GroupName] = ga.Alerts
	}

	for _, untestedRules := range config.PrometheusRules.UntestedRules {
		filePath := path.Join(baseDirectory, untestedRules)
		rules, err := readRulesFile(filePath)
		if err != nil {
			return fmt.Errorf("error reading rules file %v", err)
		}
		o.ruleFiles = append(o.ruleFiles, alertingRuleFile{
			FileBaseName: filePath,
			Rules:        *rules,
		})
	}

	for _, rulesDir := range config.PrometheusRules.RulesFolders {
		err = filepath.WalkDir(path.Join(baseDirectory, rulesDir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return fmt.Errorf("error reading rules directory %s, %v", path, err)
			}

			if d.Type().IsRegular() {
				if strings.Contains(path, "_test") {
					return nil
				}

				folderName := filepath.Dir(path)
				fileBaseName := filepath.Base(path)

				rules, err := readRulesFile(path)
				if err != nil {
					return fmt.Errorf("error reading rules file %v", err)
				}

				fileNameParts := strings.Split(fileBaseName, ".")
				if len(fileNameParts) != 2 {
					return fmt.Errorf("missing filename extension or using '.' in filename")
				}

				testFile := filepath.Join(folderName, fmt.Sprintf("%s_test.%s", fileNameParts[0], fileNameParts[1]))
				_, err = os.Stat(testFile)
				if err != nil {
					return fmt.Errorf("missing testfile %s for rule file %s", testFile, path)
				}
				testFileContent, err := os.ReadFile(testFile)
				if err != nil {
					return fmt.Errorf("error reading testfile %s: %v", testFile, err)
				}
				o.ruleFiles = append(o.ruleFiles, alertingRuleFile{
					DefaultEvaluationInterval: config.PrometheusRules.DefaultEvaluationInterval,
					FolderName:                folderName,
					FileBaseName:              fileBaseName,
					TestFileBaseName:          filepath.Base(testFile),
					TestFileContent:           testFileContent,
					Rules:                     *rules,
				})
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("error reading rules dir %s: %w", rulesDir, err)
		}
	}

	return nil
}

func (o *Options) RunTests() error {
	dir, err := os.MkdirTemp("/tmp", "prom-rule-test")
	if err != nil {
		return fmt.Errorf("error creating tempdir %v", err)
	}
	defer func() {
		os.RemoveAll(dir)
	}()

	logrus.Debugf("Created tempdir %s", dir)

	for _, irf := range o.ruleFiles {
		if irf.TestFileBaseName == "" {
			continue
		}
		ruleGroups, err := yaml.Marshal(irf.Rules.Spec)
		if err != nil {
			return fmt.Errorf("error Marshalling rule groups %v", err)
		}

		tmpFile := fmt.Sprintf("%s%s%s", dir, string(os.PathSeparator), irf.FileBaseName)

		err = os.WriteFile(tmpFile, ruleGroups, 0644)
		if err != nil {
			return fmt.Errorf("error writing rule groups file %v", err)
		}

		fileNameParts := strings.Split(irf.FileBaseName, ".")
		if len(fileNameParts) != 2 {
			return fmt.Errorf("missing filename extension or using '.' in filename")
		}

		testFile := filepath.Join(dir, irf.TestFileBaseName)
		err = os.WriteFile(testFile, irf.TestFileContent, 0644)
		if err != nil {
			return fmt.Errorf("error writing rule groups test file %v", err)
		}
		logrus.Debugf("running test %s", irf.TestFileBaseName)
		cmd := exec.Command(o.promtoolPath, "test", "rules", testFile)
		output, err := cmd.CombinedOutput()
		if err != nil {
			logrus.Error(string(output))
			return fmt.Errorf("error running promtool %v", err)
		}

	}

	return nil
}

var whitespaceMatcher = regexp.MustCompile(`\s*\n\s*`)

func (o *Options) Generate() error {
	output, err := os.Create(o.outputBicep)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create output file")
	}
	defer func() {
		if err := output.Close(); err != nil {
			logrus.WithError(err).Error("failed to close output file")
		}
	}()

	// Determine if we're generating recording rules or alerting rules based on filename
	isRecordingRulesFile := strings.Contains(o.outputBicep, "RecordingRules")
	isAlertingRulesFile := strings.Contains(o.outputBicep, "AlertingRules")

	// Validate that the filename contains the required keywords
	if !isRecordingRulesFile && !isAlertingRulesFile {
		return fmt.Errorf("output filename must contain either 'AlertingRules' or 'RecordingRules' to determine the rule type. Got: %s", o.outputBicep)
	}

	// Write parameters based on file type
	if isAlertingRulesFile {
		if _, err := output.Write([]byte(`#disable-next-line no-unused-params
param azureMonitoring string

#disable-next-line no-unused-params
param actionGroups array

#disable-next-line no-unused-params
param location string = resourceGroup().location
`)); err != nil {
			return err
		}
	} else {
		if _, err := output.Write([]byte(`
param azureMonitoring string

param location string = resourceGroup().location
`)); err != nil {
			return err
		}
	}

	for _, irf := range o.ruleFiles {
		for _, group := range irf.Rules.Spec.Groups {
			// Skip this group if not in includedAlerts
			if len(o.includedAlerts) > 0 {
				if _, exists := o.includedAlerts[group.Name]; !exists {
					continue
				}
			}

			logger := logrus.WithFields(logrus.Fields{
				"group": group.Name,
			})
			if group.QueryOffset != nil {
				logger.Warn("query offset is not supported in Microsoft.AlertsManagement/prometheusRuleGroups")
			}
			if group.Limit != nil {
				logger.Warn("alert limit is not supported in Microsoft.AlertsManagement/prometheusRuleGroups")
			}
			if group.Interval == nil {
				if irf.DefaultEvaluationInterval == "" {
					group.Interval = monitoringv1.DurationPointer(defaultEvaluationInterval)
				} else {
					group.Interval = monitoringv1.DurationPointer(irf.DefaultEvaluationInterval)
				}
			}
			armGroup := armprometheusrulegroups.PrometheusRuleGroupResource{
				Name: ptr.To(o.groupNamePrefix + group.Name),
				Properties: &armprometheusrulegroups.PrometheusRuleGroupProperties{
					Interval: parseToAzureDurationString(group.Interval),
					Enabled:  ptr.To(true),
				},
			}

			for _, rule := range group.Rules {
				// If includedAlerts is set for this group, ONLY include those alerts
				if len(o.includedAlerts) > 0 {
					if includedAlerts, exists := o.includedAlerts[group.Name]; exists {
						shouldInclude := false
						for _, includedAlert := range includedAlerts {
							if rule.Alert == includedAlert {
								shouldInclude = true
								break
							}
						}
						if !shouldInclude {
							continue
						}
					}
				}

				labels := map[string]*string{}
				for k, v := range group.Labels {
					labels[k] = ptr.To(strings.ReplaceAll(v, "'", "\\'"))
				}
				for k, v := range rule.Labels {
					labels[k] = ptr.To(strings.ReplaceAll(v, "'", "\\'"))
				}

				annotations := map[string]*string{}
				for k, v := range rule.Annotations {
					annotations[k] = ptr.To(strings.ReplaceAll(v, "'", "\\'"))
				}
				// Some part of the Azure Monitor stack consumes the `description` annotation, removing it from the
				// alert context. We want to use this value in our IcM connector, so we need to have it in the alert
				// context - simply duplicating it in the annotations and referring to our new copy is enough to side-
				// step the post-processing.
				if description, exists := annotations["description"]; exists {
					annotations["info"] = description
				}

				// If the summary annotation is present, use it as the title. Otherwise, use the alert name as the title.
				if summary, exists := annotations["summary"]; exists {
					annotations["title"] = summary
				} else {
					annotations["title"] = ptr.To(rule.Alert)
				}

				// Default correlationId groups all firings of an alert on the same cluster into one
				// IcM incident. Individual alerts can override this by setting a `correlationId`
				// annotation in their source rule — useful when finer-grained grouping is wanted
				// (e.g. one incident per hosted cluster, not per management cluster).
				if _, hasOverride := annotations["correlationId"]; !hasOverride {
					annotations["correlationId"] = ptr.To(rule.Alert + "/{{ $labels.cluster }}")
				}

				// Filter rules based on the output file type
				if rule.Alert != "" && isAlertingRulesFile {
					armGroup.Properties.Rules = append(armGroup.Properties.Rules, &armprometheusrulegroups.PrometheusRule{
						Alert:       ptr.To(rule.Alert),
						Enabled:     ptr.To(true),
						Labels:      labels,
						Annotations: annotations,
						For:         parseToAzureDurationString(rule.For),
						Expression: ptr.To(
							strings.TrimSpace(
								whitespaceMatcher.ReplaceAllString(rule.Expr.String(), " "),
							),
						),
						Severity: severityFor(labels),
					})
				} else if rule.Record != "" && isRecordingRulesFile {
					armGroup.Properties.Rules = append(armGroup.Properties.Rules, &armprometheusrulegroups.PrometheusRule{
						Record:  ptr.To(rule.Record),
						Enabled: ptr.To(true),
						Labels:  labels,
						Expression: ptr.To(
							strings.TrimSpace(
								whitespaceMatcher.ReplaceAllString(rule.Expr.String(), " "),
							),
						),
					})
				}
			}

			if len(armGroup.Properties.Rules) > 0 {
				// Use the file type to determine which function to call
				// Groups are guaranteed to contain only one type of rule

				replacementWriter := NewReplacementWriter(output, o.outputReplacements, o.regexOutputReplacements)

				if isRecordingRulesFile {
					if err := writeRecordingGroups(armGroup, replacementWriter); err != nil {
						return err
					}
				} else if isAlertingRulesFile {
					if err := writeAlertGroups(armGroup, replacementWriter); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// A note on IcM: the connection between prometheusRuleGroups to IcM via actionGroups is tenuous. Keep the following
// references in mind when working in this area:
// 1. a general document on how to customize what IcM alerts look like:  https://msazure.visualstudio.com/One/_git/EngSys-MDA-GenevaDocs?path=/documentation/alerts/HowDoI/CustomizeICMFields.md&_a=preview&version=GBmaster&anchor=using-template-parameters
// 2. the best reference for which IcM fields exist and how to set them: https://dev.azure.com/msazure/One/_git/EngSys-MDA-GenevaDocs?path=/documentation/metrics/Prometheus/PromIcMConnectorsetup.md&_a=preview&version=GBmaster
// 3. the official top-level document: https://eng.ms/docs/products/icm/developers/connectors/icmaction#edit-an-azure-monitor-icm-connector-definition-icm-action

func writeAlertGroups(groups armprometheusrulegroups.PrometheusRuleGroupResource, into io.Writer) error {
	tmpl, err := template.New("prometheusRuleGroup").Funcs(
		map[string]any{"contains": strings.Contains},
	).Parse(`
resource {{.name}} 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: '{{.groups.Name}}'
  location: location
  properties: {
    interval: '{{.groups.Properties.Interval}}'
    rules: [
{{- range .groups.Properties.Rules}}
      {
        actions: [for g in actionGroups: {
          actionGroupId: g
          actionProperties: {
            'IcM.Title': '#$.labels.cluster#: #$.annotations.title#'
            'IcM.CorrelationId': '#$.annotations.correlationId#'
          }
        }]
        alert: '{{.Alert}}'
        enabled: {{.Enabled}}
{{- if .Labels}}
        labels: {
{{- range $key, $value := .Labels}}
          {{$key}}: '{{$value}}'
{{- end }}
        }
{{- end -}}
{{- if .Annotations}}
        annotations: {
{{- range $key, $value := .Annotations}}
          {{- if contains $value "\n" }}
          {{$key}}: '''{{$value}}'''
          {{- else }}
          {{$key}}: '{{$value}}'
          {{- end }}
{{- end }}
        }
{{- end }}
        expression: '{{.Expression}}'
{{- if .For }}
        for: '{{.For}}'
{{- end }}
        severity: {{.Severity}}
      }
{{- end}}
    ]
    scopes: [
      azureMonitoring
    ]
  }
}
`)
	if err != nil {
		return err
	}

	return tmpl.Execute(into, map[string]any{
		"name":   bicepName(groups.Name),
		"groups": groups,
	})
}

func writeRecordingGroups(groups armprometheusrulegroups.PrometheusRuleGroupResource, into io.Writer) error {
	tmpl, err := template.New("prometheusRecordingRuleGroup").Parse(`
resource {{.name}} 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: '{{.groups.Name}}'
  location: location
  properties: {
{{- if .groups.Properties.Description }}
    description: '{{.groups.Properties.Description}}'
{{- end }}
    scopes: [
      azureMonitoring
    ]
    enabled: {{.groups.Properties.Enabled}}
    interval: '{{.groups.Properties.Interval}}'
    rules: [
{{- range .groups.Properties.Rules}}
      {
        record: '{{.Record}}'
        expression: '{{.Expression}}'
{{- if .Labels}}
        labels: {
{{- range $key, $value := .Labels}}
          {{$key}}: '{{$value}}'
{{- end }}
        }
{{- end }}
      }
{{- end}}
    ]
  }
}
`)
	if err != nil {
		return err
	}

	return tmpl.Execute(into, map[string]any{
		"name":   bicepName(groups.Name),
		"groups": groups,
	})
}

func bicepName(name *string) string {
	if name == nil {
		return "FIXME-NAME-NIL"
	}
	out := strings.Builder{}
	upper := false
	for _, c := range *name {
		if upper {
			out.WriteString(strings.ToUpper(string(c)))
			upper = false
			continue
		}
		if c == '-' || c == '.' || c == '_' {
			upper = true
			continue
		}
		out.WriteRune(c)
	}
	return out.String()
}

func parseToAzureDurationString(d *monitoringv1.Duration) *string {
	if d == nil {
		return nil
	}

	parsedDuration, err := model.ParseDuration(string(*d))
	if err != nil {
		logrus.Fatalf("Invalid duration %s", string(*d))
	}

	minduration, err := model.ParseDuration("1m")
	if err != nil {
		logrus.Fatalf("Invalid duration %s", string(*d))
	}

	if parsedDuration < minduration {
		logrus.Warningf("Duration '%s' is too short, setting default of 1M", parsedDuration.String())
		return ptr.To("PT1M")
	}

	// TODO: this is likely not precisely correct, but /shrug
	return ptr.To("PT" + strings.ToUpper(parsedDuration.String()))
}

func severityFor(labels map[string]*string) *int32 {
	severity, ok := labels["severity"]
	if !ok || severity == nil {
		return nil
	}

	// Severity level mapping
	// https://msazure.visualstudio.com/AzureRedHatOpenShift/_wiki/wikis/ARO.wiki/838022/IcM-best-practices?anchor=severity-levels

	switch *severity {
	case "critical":
		return ptr.To(int32(2)) // SEV 2: Single service SLA impact.
	case "warning":
		return ptr.To(int32(3)) // SEV 3: Urgent/high business impact, no SLA impact.
	case "info":
		return ptr.To(int32(4)) // SEV 4: Not urgent, no SLA impact.
	default:
		logrus.Warnf("unknown severity label %q, defaulting to verbose", *severity)
		return ptr.To(int32(4)) // Sev 4 - Verbose
	}
}
