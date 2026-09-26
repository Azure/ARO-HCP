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

package alertdiagnostics

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

type corpusEntry struct {
	Source     string `yaml:"source"`
	Group      string `yaml:"group"`
	Alert      string `yaml:"alert"`
	Occurrence int    `yaml:"occurrence"`
	Input      string `yaml:"input"`
	Plan       *Plan  `yaml:"plan,omitempty"`
	Error      string `yaml:"error,omitempty"`
}

// Bicep declarations use the restricted literal syntax checked below. YAML
// discovery is structural, including quoted keys and flow-style mappings.
var alertDeclaration = regexp.MustCompile(`(?m)^\s*(?:-\s*)?alert\s*:`)
var templateAlertDeclaration = regexp.MustCompile(`(?:^|[\s{,])['"]?alert['"]?\s*:`)

func TestRepositoryCorpus(t *testing.T) {
	root := filepath.Clean("../../..")
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	var entries []corpusEntry
	sources := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "vendor", "node_modules", "testdata", "rendered":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, "zz_fixture_") || strings.HasSuffix(name, ".upstream.yaml") || strings.HasSuffix(name, "_test.yaml") {
			return nil
		}
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" && ext != ".bicep" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if ext == ".bicep" && !alertDeclaration.Match(data) {
			return nil
		}
		// Helm templates are not YAML documents until rendered. The corpus
		// covers authored rule YAML and deployed Bicep, not chart rendering.
		if (strings.Contains(filepath.ToSlash(path), "/templates/") || strings.Contains(filepath.ToSlash(path), "/charts/") || name == "values.yaml") && bytes.Contains(data, []byte("{{")) {
			if templateAlertDeclaration.Match(data) {
				return fmt.Errorf("%s: alert in Helm template requires explicit corpus rendering support", path)
			}
			return nil
		}
		source, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		var discovered []corpusEntry
		if ext == ".bicep" {
			discovered, err = bicepAlerts(string(data))
		} else {
			discovered, err = yamlAlerts(data)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", source, err)
		}
		if ext == ".bicep" && len(discovered) != len(alertDeclaration.FindAll(data, -1)) {
			return fmt.Errorf("%s: discovered %d alerts but found %d declarations; update discovery for new syntax", source, len(discovered), len(alertDeclaration.FindAll(data, -1)))
		}
		occurrences := map[string]int{}
		for _, entry := range discovered {
			entry.Source = filepath.ToSlash(source)
			key := entry.Group + "/" + entry.Alert
			occurrences[key]++
			entry.Occurrence = occurrences[key]
			entries = append(entries, entry)
		}
		if len(discovered) != 0 {
			sources[source] = len(discovered)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no repository alerts discovered")
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Alert != b.Alert {
			return a.Alert < b.Alert
		}
		return a.Occurrence < b.Occurrence
	})
	reasons := map[string]int{}
	errors, conditions, queries, authoredYAML, bicep, unless, injected := 0, 0, 0, 0, 0, 0, 0
	for i := range entries {
		entry := &entries[i]
		if strings.HasSuffix(entry.Source, ".bicep") {
			bicep++
		} else {
			authoredYAML++
		}
		if strings.Contains(entry.Input, "unless") {
			unless++
		}
		if strings.HasSuffix(entry.Source, ".bicep") && strings.Contains(entry.Input, "unless on (subscription_id) internal_subscription:info") {
			injected++
		}
		plan, err := Extract(entry.Input)
		if err != nil {
			entry.Error = err.Error()
			errors++
			continue
		}
		entry.Plan = &plan
		validatePlan(t, plan)
		for _, condition := range plan.Conditions {
			conditions++
			queries += len(condition.Queries)
			if condition.Unsupported != "" {
				reasons[condition.Unsupported]++
				t.Logf("fallback %s/%s/%s#%d %s: %s", entry.Source, entry.Group, entry.Alert, entry.Occurrence, condition.Path, condition.Unsupported)
			}
		}
	}
	t.Logf("sources=%d alerts=%d authored-YAML=%d Bicep=%d inputs-with-unless=%d injected-subscription-exclusions=%d conditions=%d queries=%d errors=%d unsupported=%v", len(sources), len(entries), authoredYAML, bicep, unless, injected, conditions, queries, errors, reasons)
	compareCorpus(t, entries)
}

func yamlAlerts(data []byte) ([]corpusEntry, error) {
	var entries []corpusEntry
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); err == io.EOF {
			return entries, nil
		} else if err != nil {
			return nil, err
		}
		if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
			continue
		}
		var header struct {
			Kind string `yaml:"kind"`
		}
		if err := node.Decode(&header); err != nil {
			return nil, err
		}
		if header.Kind != "PrometheusRule" {
			continue
		}
		var document struct {
			Spec struct {
				Groups []struct {
					Name  string `yaml:"name"`
					Rules []struct {
						Alert *string `yaml:"alert"`
						Expr  string  `yaml:"expr"`
					} `yaml:"rules"`
				} `yaml:"groups"`
			} `yaml:"spec"`
		}
		if err := node.Decode(&document); err != nil {
			return nil, err
		}
		for _, group := range document.Spec.Groups {
			for _, rule := range group.Rules {
				if rule.Alert != nil {
					if *rule.Alert == "" || rule.Expr == "" {
						return nil, fmt.Errorf("group %s: alert requires nonempty name and expression", group.Name)
					}
					entries = append(entries, corpusEntry{Group: group.Name, Alert: *rule.Alert, Input: rule.Expr})
				}
			}
		}
	}
}

// Only the repository's literal, single-line Bicep expression subset is
// supported. Unknown escapes, interpolation, or declaration syntax fail loudly
// instead of silently reducing corpus coverage. Multiline annotations are not
// expressions and are ignored. Alert must precede expression in the same
// conventionally indented object; a closing boundary rejects a pending alert.
func bicepAlerts(data string) ([]corpusEntry, error) {
	literal := `'((?:\\.|[^'\\])*)'`
	variables := regexp.MustCompile(`(?m)^var\s+(\w+)\s*=\s*(?:` + literal + `|([0-9]+))\s*(?://[^\n]*)?$`)
	values := map[string]string{}
	for _, match := range variables.FindAllStringSubmatch(data, -1) {
		value, err := decodeBicep(match[2])
		if err != nil {
			return nil, err
		}
		if match[3] != "" {
			value = match[3]
		}
		values[match[1]] = value
	}
	fields := regexp.MustCompile(`(?m)^(  name|[ \t]+alert|[ \t]+expression):[ \t]*` + literal + `[ \t]*$|^([ \t]*)\}[^\n]*$`)
	var entries []corpusEntry
	group, alert := "", ""
	alertIndent := 0
	for _, match := range fields.FindAllStringSubmatch(data, -1) {
		if match[1] == "" {
			if alert != "" && len(match[3]) < alertIndent {
				return nil, fmt.Errorf("missing expression after alert %s before object boundary; expression must follow alert in the same object", alert)
			}
			continue
		}
		value, err := decodeBicep(match[2])
		if err != nil {
			return nil, err
		}
		for strings.Contains(value, "${") {
			start := strings.Index(value, "${")
			end := strings.Index(value[start:], "}")
			if end == -1 {
				return nil, fmt.Errorf("unterminated Bicep interpolation: %q", value)
			}
			key := value[start+2 : start+end]
			replacement, ok := values[key]
			if !ok || strings.Contains(replacement, "${") {
				return nil, fmt.Errorf("unsupported Bicep interpolation %q", key)
			}
			value = value[:start] + replacement + value[start+end+1:]
		}
		switch strings.TrimSpace(match[1]) {
		case "name":
			group = value
		case "alert":
			if alert != "" {
				return nil, fmt.Errorf("missing expression for %s", alert)
			}
			alert = value
			alertIndent = len(match[1]) - len(strings.TrimLeft(match[1], " \t"))
		case "expression":
			if alert != "" {
				if len(match[1])-len(strings.TrimLeft(match[1], " \t")) != alertIndent {
					return nil, fmt.Errorf("expression for %s is not at the same object depth", alert)
				}
				if group == "" {
					return nil, fmt.Errorf("missing group for %s", alert)
				}
				entries = append(entries, corpusEntry{Group: group, Alert: alert, Input: value})
				alert = ""
			}
		}
	}
	if alert != "" {
		return nil, fmt.Errorf("missing expression for %s", alert)
	}
	return entries, nil
}

func decodeBicep(value string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out.WriteByte(value[i])
			continue
		}
		i++
		if i == len(value) {
			return "", fmt.Errorf("trailing Bicep escape")
		}
		switch value[i] {
		case '\\', '\'', '$':
			out.WriteByte(value[i])
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		default:
			return "", fmt.Errorf("unsupported Bicep escape %s", strconv.Quote(value[i-1:i+1]))
		}
	}
	return out.String(), nil
}

func compareCorpus(t *testing.T, entries []corpusEntry) {
	t.Helper()
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(entries); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "zz_fixture_TestRepositoryCorpus.yaml")
	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buffer.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(expected), buffer.String()); diff != "" {
		t.Errorf("repository corpus changed (-want +got):\n%s\nRegenerate with UPDATE=1 go test ./util/alertdiagnostics", diff)
	}
}

func TestBicepDiscovery(t *testing.T) {
	input := `var threshold = 5 // a comment
var selector = 'metric{label="a\\\\b\'c"}'
resource rules 'Microsoft.AlertsManagement/prometheusRuleGroups@2023-03-01' = {
  name: 'example'
      {
        alert: 'First'
        expression: '${selector} > ${threshold}'
      }
      {
        alert: 'Second'
        expression: '(a > 1 and b > 2) unless on(subscription_id) internal_subscription:info'
      }
}`
	entries, err := bicepAlerts(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Group != "example" || entries[0].Input != `metric{label="a\\b'c"} > 5` {
		t.Fatalf("unexpected decoded Bicep alerts: %+v", entries)
	}
	for _, entry := range entries {
		plan, err := Extract(entry.Input)
		if err != nil {
			t.Fatalf("decoded Bicep expression %q is invalid: %v", entry.Input, err)
		}
		validatePlan(t, plan)
	}
	if !strings.Contains(entries[1].Input, "unless on(subscription_id)") {
		t.Fatalf("lost injected exclusion: %+v", entries[1])
	}
	for _, bad := range []string{
		"  name: 'example'\n alert: 'Missing'\n expression: variable",
		"  name: 'example'\n alert: 'Unknown'\n expression: '${unknown}'",
		"  name: 'example'\n alert: 'Escape'\n expression: 'a\\q'",
		`  name: 'example'
      {
        expression: 'a > 1'
        alert: 'ExpressionBeforeAlert'
      }
      {
        record: 'unrelated'
        expression: 'b > 2'
      }`,
		`  name: 'example'
      {
        alert: 'Missing'
      }
      {
        record: 'unrelated'
        expression: 'b > 2'
      }`,
	} {
		if _, err := bicepAlerts(bad); err == nil {
			t.Errorf("expected unrecognized Bicep to fail: %s", bad)
		}
	}
}

func TestYAMLDiscovery(t *testing.T) {
	entries, err := yamlAlerts([]byte(`kind: PrometheusRule
spec:
  groups:
  - name: first
    rules:
    - record: recording_rule
      expr: sum(a)
    - alert: Repeated
      expr: a > 1
    - alert: Repeated
      expr: a > 2
---
kind: PrometheusRule
spec:
  groups:
  - name: second
    rules:
    - alert: Other
      expr: |
        a > 3 and b > 4
    - 'alert': Quoted
      expr: a > 5
    - {"alert": Flow, "expr": "b > 6"}
---
kind: ConfigMap
spec:
  groups:
  - name: not-a-rule
    rules:
    - alert: Ignore
      expr: not_promql
---
rule_files: [rules.yaml]
tests:
- alert_rule_test:
  - alert: IgnorePromtool
    expr: not_promql
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []corpusEntry{
		{Group: "first", Alert: "Repeated", Input: "a > 1"},
		{Group: "first", Alert: "Repeated", Input: "a > 2"},
		{Group: "second", Alert: "Other", Input: "a > 3 and b > 4\n"},
		{Group: "second", Alert: "Quoted", Input: "a > 5"},
		{Group: "second", Alert: "Flow", Input: "b > 6"},
	}
	if diff := cmp.Diff(want, entries); diff != "" {
		t.Fatalf("YAML discovery (-want +got): %s", diff)
	}
}
