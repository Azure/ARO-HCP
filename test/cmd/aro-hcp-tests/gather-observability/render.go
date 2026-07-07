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

package gatherobservability

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
)

var filterLabelKeys = []string{"alertname", "cluster", "namespace"}

//go:embed artifacts/*.html.tmpl
var templatesFS embed.FS

func mustReadArtifact(name string) []byte {
	ret, err := templatesFS.ReadFile("artifacts/" + name)
	if err != nil {
		panic(fmt.Sprintf("failed to read embedded template %q: %v", name, err))
	}
	return ret
}

type alertsSummary struct {
	Total      int                                  `json:"total"`
	Known      int                                  `json:"known"`
	Unknown    int                                  `json:"unknown"`
	BySeverity map[armalertsmanagement.Severity]int `json:"bySeverity"`
}

type timeWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// alertsOutput is written to alerts.json and passed to the HTML template.
type alertsOutput struct {
	TimeWindow    timeWindow          `json:"timeWindow"`
	Summary       alertsSummary       `json:"summary"`
	Alerts        []alert             `json:"alerts"`
	FilterKeys    []string            `json:"filterKeys"`
	FilterOptions map[string][]string `json:"filterOptions"`
}

// Template helpers for the HTML template.
func (o alertsOutput) SeverityCounts() map[armalertsmanagement.Severity]int {
	return o.Summary.BySeverity
}
func (o alertsOutput) HasAlerts() bool        { return o.Summary.Total > 0 }
func (o alertsOutput) HasUnknownAlerts() bool { return o.Summary.Unknown > 0 }
func (o alertsOutput) KnownCount() int        { return o.Summary.Known }
func (o alertsOutput) UnknownCount() int      { return o.Summary.Unknown }

// sanitizeTitle converts a title to a lowercase kebab-case string suitable for
// use in file names.
func sanitizeTitle(title string) string {
	title = strings.ToLower(title)
	title = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, title)
	// collapse multiple dashes
	for strings.Contains(title, "--") {
		title = strings.ReplaceAll(title, "--", "-")
	}
	return strings.Trim(title, "-")
}

var filterKeyOrder = append(filterLabelKeys, "workspace", "classification")

func collectFilterOptions(alerts []alert) ([]string, map[string][]string) {
	seen := map[string]map[string]bool{}
	for _, key := range filterLabelKeys {
		seen[key] = map[string]bool{}
	}
	seen["workspace"] = map[string]bool{}

	for _, a := range alerts {
		for _, key := range filterLabelKeys {
			if v, ok := a.Alert.Labels[key]; ok && len(v) > 0 {
				seen[key][v] = true
			} else if key == "alertname" && len(a.Alert.Name) > 0 {
				seen[key][a.Alert.Name] = true
			}
		}
		if len(a.Metadata.MonitoringWorkspaceType) > 0 {
			seen["workspace"][a.Metadata.MonitoringWorkspaceType] = true
		}
	}

	options := map[string][]string{}
	for key, vals := range seen {
		if len(vals) == 0 {
			continue
		}
		sorted := make([]string, 0, len(vals))
		for v := range vals {
			sorted = append(sorted, v)
		}
		slices.Sort(sorted)
		options[key] = sorted
	}
	options["classification"] = []string{"unknown", "known"}

	keys := make([]string, 0, len(options))
	for _, key := range filterKeyOrder {
		if _, ok := options[key]; ok {
			keys = append(keys, key)
		}
	}
	return keys, options
}

func alertFilterJSON(a alert) template.JS {
	m := map[string]string{}
	for _, key := range filterLabelKeys {
		if v, ok := a.Alert.Labels[key]; ok {
			m[key] = v
		} else if key == "alertname" && len(a.Alert.Name) > 0 {
			m[key] = a.Alert.Name
		}
	}
	if len(a.Metadata.MonitoringWorkspaceType) > 0 {
		m["workspace"] = a.Metadata.MonitoringWorkspaceType
	}
	if a.Metadata.KnownIssue {
		m["classification"] = "known"
	} else {
		m["classification"] = "unknown"
	}
	data, err := json.Marshal(m)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(data)
}

func renderTemplate(outputPath string, data any) error {
	funcMap := template.FuncMap{
		"formatTime": func(t *time.Time) string {
			if t == nil {
				return "-"
			}
			return t.UTC().Format("2006-01-02 15:04:05")
		},
		"severityClass": severityCSSClass,
		"conditionClass": func(s string) string {
			switch s {
			case "Fired":
				return "condition-fired"
			case "Resolved":
				return "condition-resolved"
			default:
				return ""
			}
		},
		"label": func(labels map[string]string, key string) string {
			return labels[key]
		},
		"annotation": func(annotations map[string]string, key string) string {
			return annotations[key]
		},
		"alertFilterJSON": alertFilterJSON,
		"relativeTime": func(windowStart string, t *time.Time) string {
			if t == nil {
				return ""
			}
			start, err := time.Parse(time.RFC3339, windowStart)
			if err != nil {
				return ""
			}
			minutes := int(t.Sub(start).Minutes())
			if minutes < 0 {
				return fmt.Sprintf("T%dm", minutes)
			}
			return fmt.Sprintf("T+%dm", minutes)
		},
	}

	tmplContent := mustReadArtifact("alerts.html.tmpl")
	tmpl, err := template.New("alerts").Funcs(funcMap).Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}
	if err := os.WriteFile(outputPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}
	return nil
}
