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

package editor

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

const sizingDocument = `apiVersion: scheduling.hypershift.openshift.io/v1alpha1
kind: ClusterSizingConfiguration
metadata:
  name: cluster
spec:
  sizes:
  - name: e2e_minimal
    effects:
      resourceRequests:
      - deploymentName: kas
        containerName: server
        cpu: 100m
        memory: 100Mi
`

func sizingTemplate(document string) string {
	return "---\n{{ if .Values.limitClusterSizes }}\n" + document + "{{ else }}\n" + sizingDocument + "{{ end }}\n"
}

func writeSizingTest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sizing.yaml")
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertSizingContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("file changed unexpectedly:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestSizingRealTemplate(t *testing.T) {
	data, err := os.ReadFile("../../../../hypershiftoperator/deploy/templates/cluster.clustersizingconfiguration.yaml")
	if err != nil {
		t.Fatal(err)
	}
	original := string(data)
	path := writeSizingTest(t, original)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := NewSizing(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := ed.Entries()
	if len(entries) != 7 || entries[0] != (SizingEntry{"kube-apiserver", "kube-apiserver", "300m", "1600Mi"}) {
		t.Fatalf("unexpected limited entries: %+v", entries)
	}
	entries[0].CPU = "invalid"
	if ed.Entries()[0].CPU != "300m" {
		t.Fatal("Entries exposed internal state")
	}
	if err := ed.Apply(nil); err != nil {
		t.Fatal(err)
	}
	assertSizingContent(t, path, original)
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("dry read/no-op touched the file: %v", err)
	}
	if err := ed.Apply([]SizingUpdate{
		{"kube-apiserver", "kube-apiserver", "cpu", "230m"},
		{"kube-apiserver", "kube-apiserver", "memory", "384Mi"},
	}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(original, "cpu: 300m", "cpu: 230m", 1)
	want = strings.Replace(want, "memory: 1600Mi", "memory: 384Mi", 1)
	assertSizingContent(t, path, want)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.SplitN(string(got), "{{ else }}", 2)[1] != strings.SplitN(original, "{{ else }}", 2)[1] {
		t.Fatal("unrestricted branch changed")
	}
	for _, limited := range []bool{false, true} {
		render := func(source, additional string) string {
			t.Helper()
			tmpl, err := template.New("sizing").Funcs(template.FuncMap{
				"include": func(name string, _ any) string {
					if name != "hypershift.additionalMinimalResourceRequests" {
						t.Fatalf("unexpected include %q", name)
					}
					return additional
				},
				"trim": strings.TrimSpace,
				"indent": func(spaces int, text string) string {
					pad := strings.Repeat(" ", spaces)
					return pad + strings.ReplaceAll(text, "\n", "\n"+pad)
				},
				"trimSuffix": func(suffix, text string) string { return strings.TrimSuffix(text, suffix) },
			}).Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			var rendered bytes.Buffer
			if err := tmpl.Execute(&rendered, map[string]any{"Values": map[string]any{"limitClusterSizes": limited}}); err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := yaml.Unmarshal(rendered.Bytes(), &document); err != nil {
				t.Fatalf("edited template rendered invalid YAML: %v", err)
			}
			for i, line := range strings.Split(rendered.String(), "\n") {
				if line != strings.TrimRight(line, " \t") {
					t.Fatalf("rendered trailing whitespace at line %d: %q", i+1, line)
				}
			}
			return rendered.String()
		}
		for _, additional := range []string{"", "\n- deploymentName: control-plane-operator\n  containerName: control-plane-operator\n  cpu: 25m"} {
			before, after := render(original, additional), render(string(got), additional)
			if !limited && before != after {
				t.Fatal("unrestricted rendered manifest changed")
			}
			if limited && (after != render(want, additional) || !strings.Contains(after, "cpu: 230m")) {
				t.Fatal("limited rendered manifest did not contain the selected edits")
			}
		}
	}
	reloaded, err := NewSizing(path)
	if err != nil || !reflect.DeepEqual(reloaded.Entries(), ed.Entries()) {
		t.Fatalf("updated entries do not match a fresh read: %v", err)
	}
}

func TestSizingPreservesFormatting(t *testing.T) {
	for _, quote := range []string{"", "'", `"`} {
		for _, newline := range []string{"\n", "\r\n"} {
			for _, trailing := range []bool{false, true} {
				t.Run(quote+newline+map[bool]string{false: "no-final-newline", true: "final-newline"}[trailing], func(t *testing.T) {
					doc := strings.Replace(sizingDocument, "cpu: 100m", "'cpu':\t  "+quote+"100m"+quote+"  # CPU comment  ", 1)
					doc = strings.Replace(doc, "memory: 100Mi", `"memory": `+quote+"100Mi"+quote+"\t# memory comment\t", 1)
					original := strings.ReplaceAll(sizingTemplate(doc), "\n", newline)
					if !trailing {
						original = strings.TrimSuffix(original, newline)
					}
					path := writeSizingTest(t, original)
					ed, err := NewSizing(path)
					if err != nil {
						t.Fatal(err)
					}
					for _, cpu := range []string{"2340m", "10m"} {
						if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", cpu}, {"kas", "server", "memory", "512Mi"}}); err != nil {
							t.Fatal(err)
						}
						want := strings.Replace(original, quote+"100m"+quote, quote+cpu+quote, 1)
						want = strings.Replace(want, quote+"100Mi"+quote, quote+"512Mi"+quote, 1)
						assertSizingContent(t, path, want)
					}
					info, err := os.Stat(path)
					if err != nil || info.Mode().Perm() != 0o640 {
						t.Fatalf("mode not preserved: %v, %v", info, err)
					}
					files, err := os.ReadDir(filepath.Dir(path))
					if err != nil || len(files) != 1 {
						t.Fatalf("temporary file leak: %v, %v", files, err)
					}
				})
			}
		}
	}
}

func TestSizingTrimmedActions(t *testing.T) {
	for _, delimiters := range [][2]string{{"{{- ", " }}"}, {"{{ ", " -}}"}, {"{{- ", " -}}"}, {"{{", "}}"}} {
		original := strings.ReplaceAll(sizingTemplate(sizingDocument), "{{ ", delimiters[0])
		original = strings.ReplaceAll(original, " }}", delimiters[1])
		path := writeSizingTest(t, original)
		ed, err := NewSizing(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", "101m"}}); err != nil {
			t.Fatal(err)
		}
		assertSizingContent(t, path, strings.Replace(original, "cpu: 100m", "cpu: 101m", 1))
	}
}

func TestSizingRejectsMalformedDocuments(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":                "",
		"wrong-kind":           strings.Replace(sizingDocument, "ClusterSizingConfiguration", "Deployment", 1),
		"wrong-api":            strings.Replace(sizingDocument, "v1alpha1", "v2", 1),
		"wrong-name":           strings.Replace(sizingDocument, "name: cluster", "name: wrong", 1),
		"no-metadata":          strings.Replace(sizingDocument, "metadata:\n  name: cluster\n", "", 1),
		"wrong-spec":           strings.Replace(sizingDocument, "spec:", "other:", 1),
		"no-target-size":       strings.Replace(sizingDocument, "e2e_minimal", "other", 1),
		"duplicate-size":       sizingDocument + strings.SplitN(sizingDocument, "  sizes:\n", 2)[1],
		"duplicate-entry":      sizingDocument + strings.SplitN(sizingDocument, "      resourceRequests:\n", 2)[1],
		"duplicate-resource":   sizingDocument + "        cpu: 200m\n",
		"duplicate-map-key":    strings.Replace(sizingDocument, "spec:", "kind: Other\nspec:", 1),
		"duplicate-name-key":   strings.Replace(sizingDocument, "containerName: server", "containerName: server\n        containerName: other", 1),
		"missing-container":    strings.Replace(sizingDocument, "        containerName: server\n", "", 1),
		"missing-deployment":   strings.Replace(sizingDocument, "deploymentName: kas", "wrongName: kas", 1),
		"non-string-name":      strings.Replace(sizingDocument, "deploymentName: kas", "deploymentName: 1", 1),
		"non-list":             strings.Replace(sizingDocument, "  - name:", "    name:", 1),
		"anchor":               strings.Replace(sizingDocument, "cpu: 100m", "cpu: &quantity 100m", 1),
		"alias":                strings.Replace(sizingDocument, "cpu: 100m", "cpu: *quantity", 1),
		"merge":                strings.Replace(sizingDocument, "cpu: 100m", "<<: {cpu: 100m}", 1),
		"flow":                 strings.Replace(sizingDocument, "metadata:\n  name: cluster", "metadata: {name: cluster}", 1),
		"tag":                  strings.Replace(sizingDocument, "cpu: 100m", "cpu: !!str 100m", 1),
		"literal":              strings.Replace(sizingDocument, "cpu: 100m", "cpu: |-\n          100m", 1),
		"folded":               strings.Replace(sizingDocument, "cpu: 100m", "cpu: >-\n          100m", 1),
		"multiline-quoted":     strings.Replace(sizingDocument, "cpu: 100m", "cpu: '100m\n          '", 1),
		"multiline-plain":      strings.Replace(sizingDocument, "cpu: 100m", "cpu: 100m\n          100m", 1),
		"escaped-quoted":       strings.Replace(sizingDocument, "cpu: 100m", `cpu: "100\u006d"`, 1),
		"null":                 strings.Replace(sizingDocument, "cpu: 100m", "cpu:", 1),
		"octal-number":         strings.Replace(sizingDocument, "cpu: 100m", "cpu: 010", 1),
		"invalid-cpu":          strings.Replace(sizingDocument, "cpu: 100m", "cpu: bananas", 1),
		"invalid-memory":       strings.Replace(sizingDocument, "memory: 100Mi", "memory: 100MB", 1),
		"mapping-cpu":          strings.Replace(sizingDocument, "cpu: 100m", "cpu:\n          value: 100m", 1),
		"sequence-cpu":         strings.Replace(sizingDocument, "cpu: 100m", "cpu:\n        - 100m", 1),
		"no-resources":         strings.Replace(sizingDocument, "        cpu: 100m\n        memory: 100Mi\n", "", 1),
		"no-requests":          strings.SplitN(sizingDocument, "      resourceRequests:", 2)[0],
		"extra-document":       sizingDocument + "---\nfoo: bar\n",
		"extra-empty-document": sizingDocument + "---\n",
		"invalid-yaml":         sizingDocument + " invalid:\n",
		"complex-key":          sizingDocument + "? [a, b]\n: value\n",
	} {
		t.Run(name, func(t *testing.T) {
			original := sizingTemplate(doc)
			path := writeSizingTest(t, original)
			if _, err := NewSizing(path); err == nil {
				t.Fatal("accepted malformed document")
			}
			assertSizingContent(t, path, original)
		})
	}
}

func TestSizingRejectsUnsupportedTemplates(t *testing.T) {
	valid := sizingTemplate(sizingDocument)
	for name, content := range map[string]string{
		"no-actions":         sizingDocument,
		"wrong-if":           strings.Replace(valid, ".Values.limitClusterSizes", ".Values.other", 1),
		"negated-if":         strings.Replace(valid, "if .Values", "if not .Values", 1),
		"else-if":            strings.Replace(valid, "{{ else }}", "{{ else if .Values.other }}", 1),
		"no-else":            strings.Replace(valid, "{{ else }}", "", 1),
		"no-end":             strings.Replace(valid, "{{ end }}", "", 1),
		"extra-end":          valid + "{{ end }}\n",
		"multiple":           valid + valid,
		"nested":             strings.Replace(valid, "cpu: 100m", "{{ if .Values.limitClusterSizes }}\n        cpu: 100m\n{{ end }}", 1),
		"inline":             strings.Replace(valid, "{{ else }}", "{{ else }} # comment", 1),
		"two-actions":        strings.Replace(valid, "{{ else }}", "{{ else }}{{ end }}", 1),
		"quantity":           strings.Replace(valid, "cpu: 100m", "cpu: {{ .Values.cpu }}", 1),
		"quantity-else":      strings.ReplaceAll(valid, "cpu: 100m", `cpu: "{{ .Values.cpu }}"`),
		"comment":            strings.Replace(valid, "cpu: 100m", "cpu: 100m # {{ .Values.cpu }}", 1),
		"shared-header":      "apiVersion: v1\n" + valid,
		"extra-footer":       valid + "foo: bar\n",
		"bad-else":           strings.Replace(valid, "{{ else }}\n", "{{ else }}\ninvalid: [\n", 1),
		"duplicate-else-key": strings.Replace(valid, "{{ else }}\n", "{{ else }}\nkind: Other\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeSizingTest(t, content)
			if _, err := NewSizing(path); err == nil {
				t.Fatal("accepted unsupported template")
			}
			assertSizingContent(t, path, content)
		})
	}
}

func TestSizingRejectsInvalidUpdatesAtomically(t *testing.T) {
	valid := SizingUpdate{"kas", "server", "cpu", "200m"}
	for _, update := range []SizingUpdate{
		{"unknown", "server", "cpu", "200m"},
		{"kas", "unknown", "cpu", "200m"},
		{"kas", "server", "storage", "200Mi"},
		{"kas", "server", "memory", "1Gi"},
		{"kas", "server", "memory", "100m"},
		{"kas", "server", "memory", "0Mi"},
		{"kas", "server", "memory", "020Mi"},
		{"kas", "server", "memory", "20Mi\nfoo: bar"},
		{"kas", "server", "memory", "{{ .Values.memory }}"},
		{"kas", "server", "memory", "'100Mi'"},
		valid,
	} {
		t.Run(update.Resource+update.NewValue+update.Deployment+update.Container, func(t *testing.T) {
			original := sizingTemplate(sizingDocument)
			path := writeSizingTest(t, original)
			ed, err := NewSizing(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := ed.Apply([]SizingUpdate{valid, update}); err == nil {
				t.Fatal("accepted invalid update")
			}
			assertSizingContent(t, path, original)
		})
	}
	for _, value := range []string{"0m", "01m", "-10m", "1.5m", "1", "100Mi", "10m ", " 10m", "10m # comment", "10m\n", ""} {
		path := writeSizingTest(t, sizingTemplate(sizingDocument))
		ed, err := NewSizing(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", value}}); err == nil {
			t.Fatalf("accepted invalid cpu %q", value)
		}
		assertSizingContent(t, path, sizingTemplate(sizingDocument))
	}
}

func TestSizingMissingResourceNotUpserted(t *testing.T) {
	original := sizingTemplate(strings.Replace(sizingDocument, "        cpu: 100m\n", "", 1))
	path := writeSizingTest(t, original)
	ed, err := NewSizing(path)
	if err != nil {
		t.Fatal(err)
	}
	if ed.Entries()[0].CPU != "" {
		t.Fatal("absent CPU was not represented as empty")
	}
	if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", "100m"}}); err == nil {
		t.Fatal("upserted missing resource")
	}
	assertSizingContent(t, path, original)
}

func TestSizingOtherSizesAreNotTargets(t *testing.T) {
	other := strings.SplitN(sizingDocument, "  sizes:\n", 2)[1]
	other = strings.ReplaceAll(other, "e2e_minimal", "other")
	other = strings.ReplaceAll(other, "deploymentName: kas", "deploymentName: other")
	original := sizingTemplate(sizingDocument + other)
	path := writeSizingTest(t, original)
	ed, err := NewSizing(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ed.Entries()) != 1 {
		t.Fatalf("included entries from another size: %+v", ed.Entries())
	}
	if err := ed.Apply([]SizingUpdate{{"other", "server", "cpu", "200m"}}); err == nil {
		t.Fatal("accepted target in another size")
	}
	assertSizingContent(t, path, original)
	if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", "200m"}}); err != nil {
		t.Fatal(err)
	}
	assertSizingContent(t, path, strings.Replace(original, "cpu: 100m", "cpu: 200m", 1))
}

func TestSizingRejectsMalformedUnrestrictedBranch(t *testing.T) {
	for name, doc := range map[string]string{
		"duplicate-size":     sizingDocument + strings.SplitN(sizingDocument, "  sizes:\n", 2)[1],
		"duplicate-entry":    sizingDocument + strings.SplitN(sizingDocument, "      resourceRequests:\n", 2)[1],
		"duplicate-resource": sizingDocument + "        memory: 200Mi\n",
		"wrong-kind":         strings.Replace(sizingDocument, "ClusterSizingConfiguration", "Deployment", 1),
		"templated-quantity": strings.Replace(sizingDocument, "cpu: 100m", "cpu: {{ .Values.cpu }}", 1),
		"extra-document":     sizingDocument + "---\nfoo: bar\n",
	} {
		t.Run(name, func(t *testing.T) {
			original := "{{ if .Values.limitClusterSizes }}\n" + sizingDocument + "{{ else }}\n" + doc + "{{ end }}\n"
			path := writeSizingTest(t, original)
			if _, err := NewSizing(path); err == nil {
				t.Fatal("accepted malformed unrestricted branch")
			}
			assertSizingContent(t, path, original)
		})
	}
}

func TestSizingRefusesSymlinkWrite(t *testing.T) {
	original := sizingTemplate(sizingDocument)
	path := writeSizingTest(t, original)
	link := filepath.Join(filepath.Dir(path), "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	ed, err := NewSizing(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", "200m"}}); err == nil {
		t.Fatal("accepted symlink write")
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v", err)
	}
	assertSizingContent(t, path, original)
}

func TestSizingStaleFile(t *testing.T) {
	path := writeSizingTest(t, sizingTemplate(sizingDocument))
	ed, err := NewSizing(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := sizingTemplate(sizingDocument) + "# concurrent edit\n"
	if err := os.WriteFile(path, []byte(changed), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", "200m"}}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected stale-file error, got %v", err)
	}
	assertSizingContent(t, path, changed)
}

func TestSizingAdditionalInclude(t *testing.T) {
	valid := sizingTemplate(sizingDocument + additionalSizingInclude + "\n")
	for name, source := range map[string]string{
		"exact":         valid,
		"indented CRLF": strings.ReplaceAll(strings.Replace(valid, additionalSizingInclude, "  "+additionalSizingInclude, 1), "\n", "\r\n"),
		"before target": sizingTemplate(strings.Replace(sizingDocument, "      - deploymentName:", additionalSizingInclude+"\n      - deploymentName:", 1)),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeSizingTest(t, source)
			ed, err := NewSizing(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(ed.Entries()) != 1 {
				t.Fatal("include added editable targets")
			}
			if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", "200m"}}); err != nil {
				t.Fatal(err)
			}
			assertSizingContent(t, path, strings.Replace(source, "cpu: 100m", "cpu: 200m", 1))
		})
	}
	for name, source := range map[string]string{
		"twice":          strings.Replace(valid, additionalSizingInclude, additionalSizingInclude+"\n"+additionalSizingInclude, 1),
		"before":         additionalSizingInclude + "\n" + sizingTemplate(sizingDocument),
		"after":          sizingTemplate(sizingDocument) + additionalSizingInclude + "\n",
		"else":           strings.Replace(sizingTemplate(sizingDocument), "{{ else }}", "{{ else }}\n"+additionalSizingInclude, 1),
		"other name":     strings.Replace(valid, "hypershift.additionalMinimalResourceRequests", "other", 1),
		"other indent":   strings.Replace(valid, "indent 10", "indent 12", 1),
		"missing trim":   strings.Replace(valid, " | trim |", " |", 1),
		"other context":  strings.Replace(valid, `" . |`, `" .Values |`, 1),
		"trim marker":    strings.Replace(valid, "{{ include", "{{- include", 1),
		"inline comment": strings.Replace(valid, additionalSizingInclude, additionalSizingInclude+" # comment", 1),
		"inline value":   strings.Replace(valid, additionalSizingInclude, "value: "+additionalSizingInclude, 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeSizingTest(t, source)
			if _, err := NewSizing(path); err == nil {
				t.Fatal("accepted unsupported include")
			}
			assertSizingContent(t, path, source)
		})
	}
}

const additionalConfig = `defaults:
  hypershift:
    additionalMinimalResourceRequests:
      scheduler:
        deploymentName: kube-scheduler
        containerName: kube-scheduler
        cpu: 200m # baseline CPU
        memory: 200Mi
clouds:
  dev:
    defaults:
      hypershift:
        additionalMinimalResourceRequests: {} # dev requests
  public:
    defaults:
      other: untouched
`

func TestExperimentalCPUStaging(t *testing.T) {
	for _, scenario := range []string{"apply", "stale", "stale editor", "memory", "collision", "duplicate", "identity conflict", "missing CPU", "invalid annotation"} {
		t.Run(scenario, func(t *testing.T) {
			before := additionalConfig
			if scenario == "identity conflict" {
				before = strings.Replace(before, "additionalMinimalResourceRequests: {} # dev requests", "additionalMinimalResourceRequests:\n          scheduler:\n            deploymentName: other", 1)
			}
			if scenario == "missing CPU" {
				before = strings.Replace(before, "        cpu: 200m # baseline CPU\n", "", 1)
			}
			path := writeSizingTest(t, before)
			ed, err := NewAdditionalSizing(path)
			if err != nil {
				t.Fatal(err)
			}
			updates := []AdditionalSizingEntry{
				{ID: "scheduler", SizingEntry: SizingEntry{Deployment: "kube-scheduler", Container: "kube-scheduler", CPU: "80m"}},
				{ID: "etcd-etcd-metrics", SizingEntry: SizingEntry{Deployment: "etcd", Container: "etcd-metrics", CPU: "10m"}},
			}
			switch scenario {
			case "invalid annotation":
				updates[1].Deployment, updates[1].Container = "openshift-route-controller-manager", "openshift-route-controller-manager"
			case "memory":
				updates[1].Memory = "10Mi"
			case "collision":
				updates[1].ID = "scheduler"
			case "duplicate":
				updates = append(updates, updates[0])
			}
			staged, err := ed.StageExperimentalCPU(updates)
			assertSizingContent(t, path, before)
			if scenario == "memory" || scenario == "collision" || scenario == "duplicate" || scenario == "identity conflict" || scenario == "invalid annotation" {
				if err == nil {
					t.Fatal("accepted unsafe CPU staging")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "stale editor" {
				if err := ed.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "cpu", "90m"}}); err != nil {
					t.Fatal(err)
				}
				if err := staged.Apply(); err == nil || !strings.Contains(err.Error(), "changed since staging") {
					t.Fatalf("expected stale editor check: %v", err)
				}
				return
			}
			if scenario == "stale" {
				before += "# concurrent edit\n"
				if err := os.WriteFile(path, []byte(before), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := staged.Apply(); err == nil || !strings.Contains(err.Error(), "changed") {
					t.Fatalf("expected optimistic stale check: %v", err)
				}
				assertSizingContent(t, path, before)
				return
			}
			if err := staged.Apply(); err != nil {
				t.Fatal(err)
			}
			fresh, err := NewAdditionalSizing(path)
			if err != nil {
				t.Fatal(err)
			}
			want := []AdditionalSizingEntry{updates[1], updates[0]}
			want[1].Memory = "200Mi"
			if !reflect.DeepEqual(fresh.Entries(), want) {
				t.Fatalf("wrong staged identities/resources: %+v", fresh.Entries())
			}
			data, err := os.ReadFile(path)
			if err != nil || !strings.HasPrefix(string(data), strings.Split(before, "clouds:\n")[0]) || !strings.HasSuffix(string(data), "  public:\n    defaults:\n      other: untouched\n") {
				t.Fatalf("changed defaults/public: %v\n%s", err, data)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o640 {
				t.Fatalf("mode not preserved: %v", err)
			}
		})
	}
}

func TestAdditionalSizingEditor(t *testing.T) {
	path := writeSizingTest(t, additionalConfig)
	ed, err := NewAdditionalSizing(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := ed.Entries()
	if !reflect.DeepEqual(entries, []AdditionalSizingEntry{{"scheduler", SizingEntry{"kube-scheduler", "kube-scheduler", "200m", "200Mi"}}}) {
		t.Fatalf("wrong effective objects: %+v", entries)
	}
	entries[0].CPU = "invalid"
	if err := ed.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "cpu", "240m"}}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(additionalConfig, "additionalMinimalResourceRequests: {} # dev requests", "additionalMinimalResourceRequests:  # dev requests\n          scheduler:\n            deploymentName: \"kube-scheduler\"\n            containerName: \"kube-scheduler\"\n            cpu: 240m", 1)
	assertSizingContent(t, path, want)
	if err := ed.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "memory", "250Mi"}}); err != nil {
		t.Fatal(err)
	}
	assertSizingContent(t, path, strings.Replace(want, "            cpu: 240m", "            cpu: 240m\n            memory: 250Mi", 1))
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode not preserved: %v %v", info, err)
	}
}

func TestAdditionalSizingMerge(t *testing.T) {
	for _, dev := range []string{
		"          scheduler:\n            cpu: 300m\n",
		"          scheduler:\n            deploymentName: kube-scheduler\n            containerName: kube-scheduler\n            cpu: 300m\n",
	} {
		before := strings.Replace(additionalConfig, "additionalMinimalResourceRequests: {} # dev requests\n", "additionalMinimalResourceRequests:\n"+dev, 1)
		path := writeSizingTest(t, before)
		ed, err := NewAdditionalSizing(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := ed.Entries()[0]; got.CPU != "300m" || got.Memory != "200Mi" {
			t.Fatalf("not merged by stable ID: %+v", got)
		}
		if err := ed.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "cpu", "240m"}}); err != nil {
			t.Fatal(err)
		}
		fresh, err := NewAdditionalSizing(path)
		if err != nil || fresh.Entries()[0].CPU != "240m" {
			t.Fatalf("invalid edited config: %v", err)
		}
	}
}

func TestAdditionalSizingRejectsMalformedConfig(t *testing.T) {
	for name, before := range map[string]string{
		"invalid annotation": strings.ReplaceAll(additionalConfig, "kube-scheduler", "openshift-route-controller-manager"),
		"dot ID":             strings.Replace(additionalConfig, "scheduler:", "scheduler.injected:", 1),
		"YAML ID injection":  strings.Replace(additionalConfig, "scheduler:", "'scheduler: injected':", 1),
		"quoted key":         strings.Replace(additionalConfig, "scheduler:", "'scheduler':", 1),
		"duplicate ID":       strings.Replace(additionalConfig, "      scheduler:", "      scheduler: {}\n      scheduler:", 1),
		"duplicate identity": strings.Replace(additionalConfig, "additionalMinimalResourceRequests: {} # dev requests", "additionalMinimalResourceRequests:\n          another:\n            deploymentName: kube-scheduler\n            containerName: kube-scheduler", 1),
		"missing identity":   strings.Replace(additionalConfig, "        containerName: kube-scheduler\n", "", 1),
		"unknown field":      strings.Replace(additionalConfig, "cpu: 200m", "resources: 200m", 1),
		"nested resources":   strings.Replace(additionalConfig, "cpu: 200m", "resources:\n          requests:\n            cpu: 200m", 1),
		"duplicate resource": strings.Replace(additionalConfig, "cpu: 200m", "cpu: 200m\n        cpu: 300m", 1),
		"template":           strings.Replace(additionalConfig, "cpu: 200m", `cpu: "{{ .Values.cpu }}"`, 1),
		"anchor":             strings.Replace(additionalConfig, "cpu: 200m", "cpu: &cpu 200m", 1),
		"merge":              strings.Replace(additionalConfig, "cpu: 200m", "<<: {cpu: 200m}", 1),
		"flow object":        strings.Replace(additionalConfig, "additionalMinimalResourceRequests: {} # dev requests", "additionalMinimalResourceRequests: {scheduler: {cpu: 300m}}", 1),
		"null map":           strings.Replace(additionalConfig, "additionalMinimalResourceRequests: {}", "additionalMinimalResourceRequests: null", 1),
		"scalar ancestor":    strings.Replace(additionalConfig, "additionalMinimalResourceRequests: {} # dev requests", "additionalMinimalResourceRequests: value", 1),
		"multiline":          strings.Replace(additionalConfig, "cpu: 200m", "cpu: |-\n          200m", 1),
		"octal-number":       strings.Replace(additionalConfig, "cpu: 200m", "cpu: 010", 1),
		"extra document":     additionalConfig + "---\nfoo: bar\n",
		"sequence root":      "- value\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeSizingTest(t, before)
			if _, err := NewAdditionalSizing(path); err == nil {
				t.Fatal("accepted unsafe config")
			}
			assertSizingContent(t, path, before)
		})
	}
}

func TestAdditionalSizingRejectsUpdates(t *testing.T) {
	valid := SizingUpdate{"kube-scheduler", "kube-scheduler", "cpu", "240m"}
	for _, bad := range []SizingUpdate{
		{"fake", "fake", "cpu", "240m"},
		{"kube-scheduler", "fake", "cpu", "240m"},
		{"kube-scheduler", "kube-scheduler", "limits", "240m"},
		{"kube-scheduler", "kube-scheduler", "memory", "1Gi"},
		{"kube-scheduler", "kube-scheduler", "cpu", "240m\nnew: value"},
		valid,
	} {
		path := writeSizingTest(t, additionalConfig)
		ed, err := NewAdditionalSizing(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := ed.Apply([]SizingUpdate{valid, bad}); err == nil {
			t.Fatalf("accepted invalid update %+v", bad)
		}
		assertSizingContent(t, path, additionalConfig)
	}
	for _, before := range []string{
		strings.Replace(additionalConfig, "        cpu: 200m # baseline CPU\n", "", 1),
		"defaults:\n  hypershift:\n    additionalMinimalResourceRequests: {}\n",
	} {
		path := writeSizingTest(t, before)
		ed, err := NewAdditionalSizing(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := ed.Apply([]SizingUpdate{valid}); err == nil {
			t.Fatal("inserted unconfigured entry/resource")
		}
		assertSizingContent(t, path, before)
	}
}

func TestAdditionalSizingStaleAndSymlink(t *testing.T) {
	for _, stale := range []bool{true, false} {
		path := writeSizingTest(t, additionalConfig)
		source := path
		if !stale {
			source = filepath.Join(filepath.Dir(path), "link.yaml")
			if err := os.Symlink(path, source); err != nil {
				t.Fatal(err)
			}
		}
		ed, err := NewAdditionalSizing(source)
		if err != nil {
			t.Fatal(err)
		}
		want := additionalConfig
		if stale {
			want += "# concurrent change\n"
			if err := os.WriteFile(path, []byte(want), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		if err := ed.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "cpu", "240m"}}); err == nil {
			t.Fatal("accepted stale or symlink write")
		}
		assertSizingContent(t, path, want)
	}
}

func TestAdditionalSizingEmptyAncestorsAndFormatting(t *testing.T) {
	defaults := strings.Split(additionalConfig, "clouds:\n")[0]
	for _, dev := range []string{
		"", "clouds: {}\n", "clouds:\n  dev: {}\n", "clouds:\n  dev:\n    defaults: {}\n",
		"clouds:\n  dev:\n    defaults:\n      hypershift: {}\n",
		"clouds:\n  dev:\n    defaults:\n      hypershift:\n        additionalMinimalResourceRequests: {}\n",
		"clouds:\n  dev:\n    defaults:\n      hypershift:\n        additionalMinimalResourceRequests:\n          scheduler: {}\n",
	} {
		path := writeSizingTest(t, defaults+dev)
		ed, err := NewAdditionalSizing(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := ed.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "cpu", "240m"}}); err != nil {
			t.Fatalf("empty ancestor %q: %v", dev, err)
		}
		if data, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(data), defaults) {
			t.Fatalf("default baseline changed: %v", err)
		}
	}
	before := defaults + `clouds:
  dev:
    defaults:
      hypershift:
        additionalMinimalResourceRequests:
          scheduler:
            deploymentName: 'kube-scheduler' # identity stays as written
            containerName: kube-scheduler
            cpu: 300m # dev CPU
          other: {} # untouched inherited object
`
	before = strings.Replace(before, "clouds:\n", "      other:\n        deploymentName: cluster-api\n        containerName: manager\n        cpu: 100m\nclouds:\n", 1)
	path := writeSizingTest(t, before)
	ed, err := NewAdditionalSizing(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ed.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "cpu", "240m"}}); err != nil {
		t.Fatal(err)
	}
	assertSizingContent(t, path, strings.Replace(before, "cpu: 300m", "cpu: 240m", 1))
}

func TestSizingCurrentQuantitySyntax(t *testing.T) {
	for _, quantity := range []string{"1", ".5", "0.5", "1.5", "1.", "+1", "500m", "500000u", "500000000n", "128M", "128Mi", "1Gi", "1.5Gi", "1k", "1G", "1T", "1P", "1E", "1Ki", "1Ti", "1Pi", "1Ei", "1e3", "1E+3", "1e-3"} {
		for _, quote := range []string{"", "'", `"`} {
			t.Run(quote+quantity+quote, func(t *testing.T) {
				token := quote + quantity + quote
				doc := strings.NewReplacer("100m", token, "100Mi", token).Replace(sizingDocument)
				before := sizingTemplate(doc)
				path := writeSizingTest(t, before)
				ed, err := NewSizing(path)
				if err != nil {
					t.Fatal(err)
				}
				if got := ed.Entries()[0]; got.CPU != quantity || got.Memory != quantity {
					t.Fatalf("baseline tokens not preserved: %+v", got)
				}
				if err := ed.Apply([]SizingUpdate{{"kas", "server", "cpu", "600m"}, {"kas", "server", "memory", "150Mi"}}); err != nil {
					t.Fatal(err)
				}
				want := strings.Replace(before, "cpu: "+token, "cpu: "+quote+"600m"+quote, 1)
				want = strings.Replace(want, "memory: "+token, "memory: "+quote+"150Mi"+quote, 1)
				assertSizingContent(t, path, want)
				if _, err := NewSizing(path); err != nil {
					t.Fatalf("edited template cannot be reloaded: %v", err)
				}

				before = strings.NewReplacer("200m", token, "200Mi", token).Replace(additionalConfig)
				path = writeSizingTest(t, before)
				additional, err := NewAdditionalSizing(path)
				if err != nil {
					t.Fatal(err)
				}
				if got := additional.Entries()[0]; got.CPU != quantity || got.Memory != quantity {
					t.Fatalf("additional baseline tokens not preserved: %+v", got)
				}
				if err := additional.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "cpu", "600m"}, {"kube-scheduler", "kube-scheduler", "memory", "150Mi"}}); err != nil {
					t.Fatal(err)
				}
				want = strings.Replace(before, "additionalMinimalResourceRequests: {} # dev requests", "additionalMinimalResourceRequests:  # dev requests\n          scheduler:\n            deploymentName: \"kube-scheduler\"\n            containerName: \"kube-scheduler\"\n            cpu: 600m\n            memory: 150Mi", 1)
				assertSizingContent(t, path, want)
			})
		}
	}
}

func TestSizingRejectsUnsafeCurrentQuantities(t *testing.T) {
	for _, quantity := range []string{"", "0", "0m", "0Mi", "0e3", "-1", "-.5", "-128M", "NONE", "unlimited", "NaN", ".nan", ".inf", "0x10", "0o10", "010", "01.5", "01Mi", "1_000", "1MB", "1K", "1mi", "1e", "1e1.5", "1 m", "1m ", " 1m", "1m#comment", "1m\nfoo: bar", "{{ .Values.cpu }}"} {
		t.Run(quantity, func(t *testing.T) {
			for _, key := range []string{"cpu", "memory"} {
				old := "100m"
				if key == "memory" {
					old = "100Mi"
				}
				// Quoting prevents YAML itself from rejecting malformed tokens,
				// exercising the quantity and raw single-line safety checks too.
				before := sizingTemplate(strings.Replace(sizingDocument, key+": "+old, key+": '"+quantity+"'", 1))
				path := writeSizingTest(t, before)
				if _, err := NewSizing(path); err == nil {
					t.Fatal("template accepted unsafe current quantity")
				}
				assertSizingContent(t, path, before)
				before = strings.Replace(additionalConfig, key+": "+strings.Replace(old, "100", "200", 1), key+": '"+quantity+"'", 1)
				path = writeSizingTest(t, before)
				if _, err := NewAdditionalSizing(path); err == nil {
					t.Fatal("additional config accepted unsafe current quantity")
				}
				assertSizingContent(t, path, before)
			}
		})
	}
}

func TestSizingUpdatesStillRequireCanonicalUnits(t *testing.T) {
	for _, quantity := range []string{"1", ".5", "128M", "1Gi", "500000u", "500000000n", "1e3", "1.5m", "1.5Mi"} {
		for _, key := range []string{"cpu", "memory"} {
			path := writeSizingTest(t, sizingTemplate(sizingDocument))
			ed, err := NewSizing(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := ed.Apply([]SizingUpdate{{"kas", "server", key, quantity}}); err == nil {
				t.Fatalf("accepted noncanonical %s update %q", key, quantity)
			}
			assertSizingContent(t, path, sizingTemplate(sizingDocument))
			path = writeSizingTest(t, additionalConfig)
			additional, err := NewAdditionalSizing(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := additional.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", key, quantity}}); err == nil {
				t.Fatalf("accepted noncanonical additional %s update %q", key, quantity)
			}
			assertSizingContent(t, path, additionalConfig)
		}
	}
}

func TestAdditionalSizingDecimalEffectiveBaseline(t *testing.T) {
	before := strings.Replace(additionalConfig, "cpu: 200m", "cpu: 1", 1)
	before = strings.Replace(before, "additionalMinimalResourceRequests: {} # dev requests", `additionalMinimalResourceRequests:
          scheduler:
            cpu: .5 # effective half core
            memory: 128M # decimal bytes`, 1)
	path := writeSizingTest(t, before)
	ed, err := NewAdditionalSizing(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := ed.Entries()[0]; got.CPU != ".5" || got.Memory != "128M" {
		t.Fatalf("wrong effective baseline for observed request comparison: %+v", got)
	}
	if err := ed.Apply([]SizingUpdate{{"kube-scheduler", "kube-scheduler", "cpu", "600m"}, {"kube-scheduler", "kube-scheduler", "memory", "150Mi"}}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(before, "cpu: .5", "cpu: 600m", 1)
	want = strings.Replace(want, "memory: 128M # decimal bytes", "memory: 150Mi # decimal bytes\n            deploymentName: \"kube-scheduler\"\n            containerName: \"kube-scheduler\"", 1)
	assertSizingContent(t, path, want)
	fresh, err := NewAdditionalSizing(path)
	if err != nil || fresh.Entries()[0].CPU != "600m" || fresh.Entries()[0].Memory != "150Mi" {
		t.Fatalf("cannot read emitted quantities: %v", err)
	}
}
