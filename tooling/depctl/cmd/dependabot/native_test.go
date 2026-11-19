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

package dependabot

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

const nativeCommandGroup = "0123456789abcdef01234567"

// NativeCommandInventory writes a complete input fixture for actual command execution.
func nativeCommandInventory(t *testing.T) string {
	t.Helper()
	payload := `{
		"repository":"Azure/ARO-HCP","base_sha":"` + strings.Repeat("a", 40) + `",
		"groups":[{
			"group":"` + nativeCommandGroup + `","target":"v1.2.3","ecosystem":"npm","family":"typespec",
			"next_action":{"action":"maintain_replacement"},"blockers":[],"future":9007199254740993,
			"sources":[{"number":7,"head_sha":"` + strings.Repeat("b", 40) + `",
				"group":"` + nativeCommandGroup + `","target":"v1.2.3","ecosystem":"npm","family":"typespec",
				"intents":[{"ecosystem":"npm","package":"@typespec/compiler","manifest":"api/package.json",
					"lock_manifests":["api/package-lock.json","api/package.json"]}]}]
		}]
	}`
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ExecuteNativeCommand runs the public subcommand tree with captured output.
func executeNativeCommand(t *testing.T, args []string) (string, error) {
	t.Helper()
	command, err := NewCommand("")
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&errOut)
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs(args)
	err = command.Execute()
	return out.String(), err
}

func TestNativeSelectGroupCommandEmitsJSON(t *testing.T) {
	path := nativeCommandInventory(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeNativeCommand(t, []string{"native", "select-group", "--repository", "Azure/ARO-HCP", "--inventory", path, "--group", nativeCommandGroup, "--target", "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	var selection struct {
		BaseSHA   string                     `json:"base_sha"`
		Group     map[string]json.RawMessage `json:"group"`
		Manifests []string                   `json:"manifests"`
	}
	if err := json.Unmarshal([]byte(out), &selection); err != nil {
		t.Fatalf("stdout must be only JSON: %q: %v", out, err)
	}
	if selection.BaseSHA != strings.Repeat("a", 40) || string(selection.Group["future"]) != "9007199254740993" ||
		strings.Join(selection.Manifests, ",") != "api/package-lock.json,api/package.json" {
		t.Fatalf("unexpected selection: %s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("inventory changed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("selector wrote extra files: %v, %v", entries, err)
	}
}

func TestNativeSelectGroupCommandRejectsInvalidArguments(t *testing.T) {
	path := nativeCommandInventory(t)
	valid := []string{"native", "select-group", "--repository", "Azure/ARO-HCP", "--inventory", path, "--group", nativeCommandGroup, "--target", "v1.2.3"}
	for _, flag := range []string{"--repository", "--inventory", "--group", "--target"} {
		args := make([]string, 0, len(valid)-2)
		for i := 0; i < len(valid); i++ {
			if valid[i] == flag {
				i++
				continue
			}
			args = append(args, valid[i])
		}
		t.Run("missing "+flag, func(t *testing.T) {
			out, err := executeNativeCommand(t, args)
			var exit *depctl.ExitError
			if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid || out != "" {
				t.Fatalf("stdout=%q error=%v; want invalid input without stdout", out, err)
			}
		})
	}
	for name, args := range map[string][]string{
		"extra positional": append(append([]string{}, valid...), "extra"),
		"unknown flag":     append(append([]string{}, valid...), "--unknown"),
		"unknown target":   append(append([]string{}, valid[:len(valid)-1]...), "v9.9.9"),
		"missing file":     {"native", "select-group", "--repository", "Azure/ARO-HCP", "--inventory", path + ".missing", "--group", nativeCommandGroup, "--target", "v1.2.3"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := executeNativeCommand(t, args)
			if err == nil || out != "" {
				t.Fatalf("stdout=%q error=%v; want rejection without stdout", out, err)
			}
		})
	}
}

func TestNativeSelectGroupCommandRejectsMalformedInventory(t *testing.T) {
	path := nativeCommandInventory(t)
	if err := os.WriteFile(path, []byte(`{"groups":[],"groups":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := executeNativeCommand(t, []string{"native", "select-group", "--repository", "Azure/ARO-HCP", "--inventory", path, "--group", nativeCommandGroup, "--target", "v1.2.3"})
	var exit *depctl.ExitError
	if !errors.As(err, &exit) || exit.Code != depctl.ExitInvalid || out != "" {
		t.Fatalf("stdout=%q error=%v; want invalid input without stdout", out, err)
	}
}
