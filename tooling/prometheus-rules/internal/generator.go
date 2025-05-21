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
	"strings"
	"text/template"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/yaml"

	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
	"k8s.io/utils/ptr"
)

type alertingRuleFile struct {
	FolderName       string
	FileBaseName     string
	TestFileBaseName string
	Rules            monitoringv1.PrometheusRule
	TestFileContent  []byte
}

type Options struct {
	outputBicep string
	ruleFiles   []alertingRuleFile
}

type PrometheusRulesConfig struct {
	RulesFolders  []string `yaml:"rulesFolders"`
	UntestedRules []string `yaml:"untestedRules,omitempty"`
	OutputBicep   string   `yaml:"outputBicep"`
}

type CliConfig struct {
	PrometheusRules PrometheusRulesConfig `yaml:"prometheusRules"`
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
	return &rules, nil
}

func (o *Options) Complete(configFilePath string) error {
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

	o.outputBicep = path.Join(baseDirectory, config.PrometheusRules.OutputBicep)

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
					FolderName:       folderName,
					FileBaseName:     fileBaseName,
					TestFileBaseName: filepath.Base(testFile),
					TestFileContent:  testFileContent,
					Rules:            *rules,
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
		cmd := exec.Command("promtool", "test", "rules", testFile)
		output, err := cmd.CombinedOutput()
		if err != nil {
			logrus.Error(string(output))
			return fmt.Errorf("error running promtool %v", err)
		}

	}

	return nil
}

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

	if _, err := output.Write([]byte(`param azureMonitoring string
`)); err != nil {
		return err
	}

	for _, irf := range o.ruleFiles {
		for _, group := range irf.Rules.Spec.Groups {
			logger := logrus.WithFields(logrus.Fields{
				"group": group.Name,
			})
			if group.QueryOffset != nil {
				logger.Warn("query offset is not supported in Microsoft.AlertsManagement/prometheusRuleGroups")
			}
			if group.Limit != nil {
				logger.Warn("alert limit is not supported in Microsoft.AlertsManagement/prometheusRuleGroups")
			}
			armGroup := armalertsmanagement.PrometheusRuleGroupResource{
				Name: ptr.To(group.Name),
				Properties: &armalertsmanagement.PrometheusRuleGroupProperties{
					Interval: formatDuration(group.Interval),
					Enabled:  ptr.To(true),
				},
			}

			for _, rule := range group.Rules {
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
				if rule.Alert != "" {
					armGroup.Properties.Rules = append(armGroup.Properties.Rules, &armalertsmanagement.PrometheusRule{
						Alert:       ptr.To(rule.Alert),
						Enabled:     ptr.To(true),
						Labels:      labels,
						Annotations: annotations,
						For:         formatDuration(rule.For),
						Expression: ptr.To(
							strings.TrimSpace(
								strings.ReplaceAll(rule.Expr.String(), "\n", " "),
							),
						),
						Severity: severityFor(labels),
					})
				}
			}

			if len(armGroup.Properties.Rules) > 0 {
				if err := writeGroups(armGroup, output); err != nil {
					return err
				}
			}
		}

	}
	return nil
}

func writeGroups(groups armalertsmanagement.PrometheusRuleGroupResource, into io.Writer) error {
	tmpl, err := template.New("prometheusRuleGroup").Parse(`
resource {{.name}} 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: '{{.groups.Name}}'
  location: resourceGroup().location
  properties: {
    rules: [
{{- range .groups.Properties.Rules}}
      {
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
          {{$key}}: '{{$value}}'
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

func formatDuration(d *monitoringv1.Duration) *string {
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

	switch *severity {
	case "critical":
		return ptr.To(int32(2))
	case "warning":
		return ptr.To(int32(3))
	case "info":
		return ptr.To(int32(4))
	default:
		logrus.Warnf("unknown severity label %q", *severity)
		return ptr.To(int32(5))
	}
}
