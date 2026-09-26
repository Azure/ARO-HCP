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
	if len(entries) != 7 || entries[0] != (SizingEntry{"kube-apiserver", "kube-apiserver", "100m", "100Mi"}) {
		t.Fatalf("unexpected limited entries: %+v", entries)
	}
	entries[0].CPU = "invalid"
	if ed.Entries()[0].CPU != "100m" {
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
	want := strings.Replace(original, "cpu: 100m", "cpu: 230m", 1)
	want = strings.Replace(want, "memory: 100Mi", "memory: 384Mi", 1)
	assertSizingContent(t, path, want)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.SplitN(string(got), "{{ else }}", 2)[1] != strings.SplitN(original, "{{ else }}", 2)[1] {
		t.Fatal("unrestricted branch changed")
	}
	for _, limited := range []bool{false, true} {
		render := func(source string) string {
			t.Helper()
			tmpl, err := template.New("sizing").Parse(source)
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
			return rendered.String()
		}
		before, after := render(original), render(string(got))
		if !limited && before != after {
			t.Fatal("unrestricted rendered manifest changed")
		}
		if limited && (after != render(want) || !strings.Contains(after, "cpu: 230m")) {
			t.Fatal("limited rendered manifest did not contain the selected edits")
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
		"number":               strings.Replace(sizingDocument, "cpu: 100m", "cpu: 100", 1),
		"invalid-cpu":          strings.Replace(sizingDocument, "cpu: 100m", "cpu: bananas", 1),
		"invalid-memory":       strings.Replace(sizingDocument, "memory: 100Mi", "memory: 100m", 1),
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
