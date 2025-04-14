package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

type options struct {
	inputRules  string
	outputBicep string

	rules  monitoringv1.PrometheusRule
	output *os.File
}

func newOptions() *options {
	o := &options{}
	return o
}

func (o *options) addFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.inputRules, "input-rules", "", "path to a file containing input rules")
	fs.StringVar(&o.outputBicep, "output-bicep", "", "path to a file where bicep will be written")
}

func (o *options) complete(args []string) error {
	if len(args) != 0 {
		return errors.New("no arguments are supported")
	}
	rawRules, err := os.ReadFile(o.inputRules)
	if err != nil {
		return fmt.Errorf("failed to read input rules: %v", err)
	}
	if err := yaml.Unmarshal(rawRules, &o.rules); err != nil {
		return fmt.Errorf("failed to parse input rules: %v", err)
	}
	o.output, err = os.Create(o.outputBicep)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	return nil
}

func (o *options) validate() error {
	if o.inputRules == "" {
		return errors.New("--input-rules is required")
	}
	if o.outputBicep == "" {
		return errors.New("--output-bicep is required")
	}
	return nil
}

func main() {
	o := newOptions()
	o.addFlags(flag.CommandLine)
	flag.Parse()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	if err := o.complete(flag.Args()); err != nil {
		logrus.WithError(err).Fatal("could not complete options")
	}
	if err := generate(o.rules, o.output); err != nil {
		logrus.WithError(err).Fatal("failed to generate bicep")
	}
}

func generate(input monitoringv1.PrometheusRule, output io.WriteCloser) error {
	defer func() {
		if err := output.Close(); err != nil {
			logrus.WithError(err).Error("failed to close output file")
		}
	}()

	if _, err := output.Write([]byte(`param azureMonitoring string
`)); err != nil {
		return err
	}

	for _, group := range input.Spec.Groups {
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
{{- end -}}
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
	// TODO: this is likely not precisely correct, but /shrug
	return ptr.To("PT" + strings.ToUpper(string(*d)))
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
