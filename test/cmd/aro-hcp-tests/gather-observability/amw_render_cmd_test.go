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

package gatherobservability

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderAMWCommand(t *testing.T) {
	// Remove local credentials and CLI access as well as omitting all live flags.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AZURE_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "")
	for _, name := range []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID", "AZURE_FEDERATED_TOKEN_FILE"} {
		t.Setenv(name, "")
	}
	report := amwRenderFixture()
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "amw.json"), filepath.Join(dir, "observability-summary.html")
	if err := os.WriteFile(input, append(data, '\n', '\t'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("previous report"), 0600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetArgs([]string{"render-amw", "--input", input, "--output", output})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("render saved report without configuration or credentials: %v", err)
	}
	actual, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	_, payload, ok := strings.Cut(string(actual), "const TABS =")
	if !ok || !bytes.Contains(actual, []byte(`id="tabbar"`)) {
		t.Fatal("output is not the combined observability page")
	}
	var tabs []observabilityTab
	if err := json.NewDecoder(strings.NewReader(payload)).Decode(&tabs); err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 1 || tabs[0].Title != "AMW" {
		t.Fatalf("expected exactly one AMW tab, got %d tabs", len(tabs))
	}
	want, err := renderAMWHTMLWithEvidence(report, "./amw.json")
	if err != nil {
		t.Fatal(err)
	}
	if tabs[0].HTML != string(want) {
		t.Fatal("saved SDK report did not use the shared AMW renderer")
	}
	current, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(previous, current) || current.Mode().Perm() != 0644 {
		t.Fatal("output must be atomically replaced with mode 0644")
	}
	saved, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(saved, append(data, '\n', '\t')) {
		t.Fatalf("input changed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("temporary output was not cleaned up: %v, %v", entries, err)
	}
}

func TestRenderAMWCommandRejectsInputAliases(t *testing.T) {
	for _, alias := range []string{"same path", "relative path", "symlink", "hard link", "parent symlink"} {
		t.Run(alias, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "amw.json")
			data := []byte(`{"Start":"2026-09-24T12:00:00Z","End":"2026-09-24T12:01:00Z"}`)
			if err := os.WriteFile(input, data, 0600); err != nil {
				t.Fatal(err)
			}
			output := input
			switch alias {
			case "relative path":
				t.Chdir(dir)
				output = "./amw.json"
			case "symlink", "hard link":
				output = filepath.Join(dir, "alias.html")
				link := os.Symlink
				if alias == "hard link" {
					link = os.Link
				}
				if err := link(input, output); err != nil {
					t.Fatal(err)
				}
			case "parent symlink":
				parent := filepath.Join(dir, "alias")
				if err := os.Symlink(dir, parent); err != nil {
					t.Fatal(err)
				}
				output = filepath.Join(parent, "amw.json")
			}
			cmd := newRenderAMWCommand()
			cmd.SetArgs([]string{"--input", input, "--output", output})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "must not overwrite") {
				t.Fatalf("expected alias rejection, got %v", err)
			}
			for _, path := range []string{input, output} {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, data) {
					t.Fatalf("alias rejection changed %s: %v", path, err)
				}
			}
		})
	}
}

func TestRenderAMWCommandEvidenceLink(t *testing.T) {
	for _, filename := range []string{"evidence.json", "evidence #1?.json", "javascript:example.json"} {
		t.Run(filename, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, filename)
			outputDir := filepath.Join(dir, "reports")
			if err := os.Mkdir(outputDir, 0700); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(outputDir, "report.html")
			if err := os.WriteFile(input, []byte(`{"Start":"2026-09-24T12:00:00Z","End":"2026-09-24T12:01:00Z"}`), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := newRenderAMWCommand()
			cmd.SetArgs([]string{"--input", input, "--output", output})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			_, payload, _ := strings.Cut(string(data), "const TABS =")
			var tabs []observabilityTab
			if err := json.NewDecoder(strings.NewReader(payload)).Decode(&tabs); err != nil {
				t.Fatal(err)
			}
			_, link, ok := strings.Cut(tabs[0].HTML, `<a href="`)
			if !ok {
				t.Fatal("JSON link missing")
			}
			link, _, _ = strings.Cut(link, `"`)
			parsed, err := url.Parse(link)
			if err != nil || parsed.IsAbs() || parsed.Fragment != "" || parsed.RawQuery != "" {
				t.Fatalf("unsafe evidence URL %q: %v", link, err)
			}
			resolved := (&url.URL{Scheme: "file", Path: output}).ResolveReference(parsed)
			if resolved.Path != input {
				t.Fatalf("link points at %q, want %q", resolved.Path, input)
			}
		})
	}
}

func TestRenderAMWCommandValidation(t *testing.T) {
	valid := `{"Start":"2026-09-24T12:00:00Z","End":"2026-09-24T12:01:00Z"}`
	for _, tc := range []struct{ name, input, want string }{
		{"empty", "", "decode AMW input"},
		{"invalid JSON", "{", "decode AMW input"},
		{"trailing object", valid + "{}", "decode AMW input"},
		{"trailing null", valid + "null", "decode AMW input"},
		{"trailing junk", valid + "!", "decode AMW input"},
		{"invalid timestamp", `{"Start":"invalid"}`, "decode AMW input"},
		{"null", "null", "AMW report requires"},
		{"invalid window", `{"Start":"2026-09-24T12:00:00Z"}`, "AMW report requires"},
		{"oversized", valid, "64 MiB limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "amw.json"), filepath.Join(dir, "report.html")
			if err := os.WriteFile(input, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.name == "oversized" {
				if err := os.Truncate(input, amwMaxInputBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(output, []byte("previous report"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := newRenderAMWCommand()
			cmd.SetArgs([]string{"--input", input, "--output", output})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
			actual, err := os.ReadFile(output)
			if err != nil || string(actual) != "previous report" {
				t.Fatalf("invalid input changed previous output: %v", err)
			}
		})
	}
	for _, args := range [][]string{nil, {"--input", "unused"}, {"--output", "unused"}, {"--input", " ", "--output", "unused"}, {"extra"}} {
		cmd := newRenderAMWCommand()
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil {
			t.Errorf("expected argument validation error for %v", args)
		}
	}
}

func TestRenderAMWCommandReplaceFailure(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "amw.json"), filepath.Join(dir, "report.html")
	if err := os.WriteFile(input, []byte(`{"Start":"2026-09-24T12:00:00Z","End":"2026-09-24T12:01:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(output, "keep")
	if err := os.WriteFile(marker, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := newRenderAMWCommand()
	cmd.SetArgs([]string{"--input", input, "--output", output})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "replace AMW output") {
		t.Fatalf("expected replacement error, got %v", err)
	}
	actual, err := os.ReadFile(marker)
	if err != nil || string(actual) != "unchanged" {
		t.Fatalf("failed replacement changed existing output: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("failed replacement left temporary output: %v, %v", entries, err)
	}
}
