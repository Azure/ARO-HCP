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

package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initFixtureRepository creates a minimal pin layout for command tests.
func initFixtureRepository(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	files := map[string]string{
		".bingo/go.mod":   "module _\n",
		".bingo/tool.mod": "module _\n\ngo 1.26.0\n\nrequire example.com/tool v1.0.0\n",
		"dev-infrastructure/openshift-ci/update-tool-versions.sh": "#!/usr/bin/env bash\n" +
			"if [[ \"${1:-}\" == \"--metadata-json\" ]]; then\n" +
			"  printf '[{\"variable\":\"BICEP_VERSION\",\"repository\":\"Azure/bicep\"}]\\n'\n" +
			"  exit 0\nfi\nexit 1\n",
		"dev-infrastructure/openshift-ci/versions.mk": "# versions\nBICEP_VERSION ?= 0.48.1\n",
	}
	for name, content := range files {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for _, arguments := range [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"add", "--all"},
		{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "base"},
	} {
		command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", arguments, err, output)
		}
	}
	return directory
}

func TestInventoryWithoutOutputPrintsTheRecord(t *testing.T) {
	repositoryPath := initFixtureRepository(t)
	command, err := NewCommand("workflow")
	if err != nil {
		t.Fatalf("NewCommand() error = %v", err)
	}
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetContext(context.Background())
	command.SetArgs([]string{"--repo", repositoryPath, "--offline"})
	if err := command.Execute(); err != nil {
		t.Fatalf("inventory error = %v (%s)", err, output.String())
	}
	printed := strings.TrimSpace(output.String())
	if printed == "" {
		t.Fatal("inventory without --output must print its record")
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(printed), &record); err != nil {
		t.Fatalf("printed output is not a record: %v\n%s", err, printed)
	}
	if record["kind"] != "Inventory" {
		t.Fatalf("unexpected kind %v", record["kind"])
	}
}

func TestInvalidFlagIsReportedAsInvalidInput(t *testing.T) {
	command, err := NewCommand("workflow")
	if err != nil {
		t.Fatalf("NewCommand() error = %v", err)
	}
	command.SetArgs([]string{"--nope"})
	if err := command.Execute(); err == nil {
		t.Fatal("an unknown flag must fail")
	}
}
