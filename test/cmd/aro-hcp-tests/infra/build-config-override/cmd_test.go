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

package buildconfigoverride

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasSuffix(key, "_IMAGE") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, key := range []string{
		"SHARED_DIR", "DEPLOY_ENV", "ARM_HELPER_POOL_CATALOG", "USE_OC_LOGIN_REGISTRIES",
		"LEASED_MSI_MOCK_SP", "LEASED_ARM_HELPER_SP", "LEASED_MSI_CONTAINERS", "LEASED_INFRASTRUCTURE_IDENTITY_BUNDLE",
		"BACKEND_IMAGE", "FRONTEND_IMAGE", "ADMIN_API_IMAGE", "SESSIONGATE_IMAGE", "HCP_RECOVERY_IMAGE",
		"FLEET_IMAGE", "MGMT_AGENT_IMAGE", "SWIFT_RECORDER_IMAGE", "KUBE_APPLIER_IMAGE", "EXPORTER_IMAGE",
	} {
		t.Setenv(key, "")
	}
}

func execute(args ...string) (string, error) {
	command := NewCommand()
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), err
}

func readDefaults(t *testing.T, path, environment string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"clouds", "dev", "environments", environment, "defaults"} {
		next, ok := document[key].(map[string]any)
		if !ok {
			t.Fatalf("expected configuration mapping at %q in %#v", key, document)
		}
		document = next
	}
	return document
}

func TestBuildConfigOverrideDefaults(t *testing.T) {
	clearEnvironment(t)
	shared := t.TempDir()
	t.Setenv("SHARED_DIR", shared)
	t.Setenv("DEPLOY_ENV", "test-env")
	t.Setenv("BACKEND_IMAGE", "registry.example/backend@sha256:1234")
	t.Setenv("USE_OC_LOGIN_REGISTRIES", "prior.example registry.example")
	stdout, err := execute()
	if err != nil {
		t.Fatal(err)
	}
	if want := "prior.example registry.example registry.example\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	defaults := readDefaults(t, filepath.Join(shared, "config-override.yaml"), "test-env")
	wantImage := map[string]any{"image": map[string]any{
		"registry": "registry.example", "repository": "backend", "digest": "sha256:1234",
	}}
	if !reflect.DeepEqual(defaults["backend"], wantImage) {
		t.Fatalf("backend override = %#v, want %#v", defaults["backend"], wantImage)
	}
}

func TestEnvironmentSnapshot(t *testing.T) {
	clearEnvironment(t)
	shared := t.TempDir()
	t.Setenv("SHARED_DIR", shared)
	t.Setenv("DEPLOY_ENV", "original")
	t.Setenv("USE_OC_LOGIN_REGISTRIES", "original.example")
	t.Setenv("BACKEND_IMAGE", "original.example/backend@sha256:1234")
	command := NewCommand()

	t.Setenv("SHARED_DIR", t.TempDir())
	t.Setenv("DEPLOY_ENV", "changed")
	t.Setenv("USE_OC_LOGIN_REGISTRIES", "changed.example")
	t.Setenv("BACKEND_IMAGE", "invalid")
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetArgs([]string{})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "original.example original.example\n" {
		t.Fatalf("environment snapshot changed: %q", stdout.String())
	}
	defaults := readDefaults(t, filepath.Join(shared, "config-override.yaml"), "original")
	wantImage := map[string]any{"image": map[string]any{
		"registry": "original.example", "repository": "backend", "digest": "sha256:1234",
	}}
	if !reflect.DeepEqual(defaults["backend"], wantImage) {
		t.Fatalf("backend snapshot = %#v, want %#v", defaults["backend"], wantImage)
	}
}

func TestFlagsOverrideEnvironment(t *testing.T) {
	clearEnvironment(t)
	environmentShared, flagShared := t.TempDir(), t.TempDir()
	t.Setenv("SHARED_DIR", environmentShared)
	t.Setenv("DEPLOY_ENV", "environment")
	_, err := execute("--shared-dir", flagShared, "--deploy-env", "flag")
	if err != nil {
		t.Fatal(err)
	}
	readDefaults(t, filepath.Join(flagShared, "config-override.yaml"), "flag")
	if _, err := os.Stat(filepath.Join(environmentShared, "config-override.yaml")); !os.IsNotExist(err) {
		t.Fatalf("environment output should not be published: %v", err)
	}
}

func TestSelectedSourceCatalogs(t *testing.T) {
	for _, selection := range []string{"default", "relative", "absolute", "environment", "flag-over-environment"} {
		t.Run(selection, func(t *testing.T) {
			clearEnvironment(t)
			source, shared := t.TempDir(), t.TempDir()
			relative := "dev-infrastructure/openshift-ci/arm-helper-pool.yaml"
			if selection != "default" {
				relative = "selected/arm.yaml"
			}
			catalog := filepath.Join(source, relative)
			if err := os.MkdirAll(filepath.Dir(catalog), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(catalog, []byte("armHelperPool:\n  selected:\n    clientId: baseline-client\n    principalId: baseline-principal\n    certName: baseline-cert\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LEASED_ARM_HELPER_SP", "selected")
			args := []string{"--source-dir", source, "--shared-dir", shared, "--deploy-env", "baseline"}
			switch selection {
			case "relative":
				args = append(args, "--arm-helper-pool-catalog", relative)
			case "absolute":
				args = append(args, "--arm-helper-pool-catalog", catalog)
			case "environment":
				t.Setenv("ARM_HELPER_POOL_CATALOG", relative)
			case "flag-over-environment":
				t.Setenv("ARM_HELPER_POOL_CATALOG", "missing.yaml")
				args = append(args, "--arm-helper-pool-catalog", relative)
			}
			if stdout, err := execute(args...); err != nil || stdout != "\n" {
				t.Fatalf("stdout = %q, err = %v", stdout, err)
			}
			defaults := readDefaults(t, filepath.Join(shared, "config-override.yaml"), "baseline")
			if defaults["armHelperClientId"] != "baseline-client" || defaults["clustersServiceArmHelperCertName"] != "baseline-cert" {
				t.Fatalf("selected catalog not used: %#v", defaults)
			}
		})
	}
}

type publicationWriter struct {
	t      *testing.T
	path   string
	output bytes.Buffer
}

func (w *publicationWriter) Write(data []byte) (int, error) {
	w.t.Helper()
	defaults := readDefaults(w.t, w.path, "dev")
	want := map[string]any{"mgmt": map[string]any{"aks": map[string]any{"userAgentPool": map[string]any{"minCount": 1}}}}
	if !reflect.DeepEqual(defaults, want) {
		w.t.Fatalf("stdout emitted before complete override publication: %#v", defaults)
	}
	return w.output.Write(data)
}

func TestOutputPublication(t *testing.T) {
	clearEnvironment(t)
	path := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(path, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USE_OC_LOGIN_REGISTRIES", "literal;$(false)")
	command := NewCommand()
	writer := &publicationWriter{t: t, path: path}
	command.SetOut(writer)
	command.SetArgs([]string{"--deploy-env", "dev", "--output-file", path})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if writer.output.String() != "literal;$(false)\n" {
		t.Fatalf("unexpected stdout: %q", writer.output.String())
	}
}

func TestFailureDoesNotPublish(t *testing.T) {
	for _, failure := range []string{"invalid-input", "unknown-image", "missing-catalog", "publication", "extra-argument", "missing-output"} {
		t.Run(failure, func(t *testing.T) {
			clearEnvironment(t)
			shared := t.TempDir()
			path := filepath.Join(shared, "config-override.yaml")
			if err := os.WriteFile(path, []byte("previous"), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--shared-dir", shared, "--deploy-env", "dev"}
			switch failure {
			case "invalid-input":
				t.Setenv("BACKEND_IMAGE", "not-a-digest")
			case "unknown-image":
				t.Setenv("UNSUPPORTED_IMAGE", "registry.example/unsupported@sha256:1234")
			case "missing-catalog":
				t.Setenv("LEASED_MSI_MOCK_SP", "missing")
				args = append(args, "--source-dir", t.TempDir())
			case "publication":
				args = append(args, "--output-file", shared)
			case "extra-argument":
				args = append(args, "unexpected")
			case "missing-output":
				args = []string{"--deploy-env", "dev"}
			}
			if stdout, err := execute(args...); err == nil || stdout != "" {
				t.Fatalf("stdout = %q, err = %v", stdout, err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "previous" {
				t.Fatalf("previous override changed: %q, %v", data, err)
			}
		})
	}
}

// Run the actual command implementation in a child process for the sourced
// adapter test; the launcher only selects this helper, never fabricates output.
func TestCommandProcess(t *testing.T) {
	if os.Getenv("ARO_HCP_INFRA_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			command := NewCommand()
			command.SetArgs(os.Args[i+3:]) // skip infra and build-config-override
			command.SilenceErrors = true
			if err := command.Execute(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestSourcedAdapter(t *testing.T) {
	clearEnvironment(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs("../../../../../hack/ci/build-config-override.sh")
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	if err := os.Mkdir(filepath.Join(checkout, "test"), 0755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(t.TempDir(), "aro-hcp-tests")
	if err := os.WriteFile(launcher, []byte("#!/bin/bash\nexec \"${CLI_TEST_BINARY}\" -test.run=^TestCommandProcess$ -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLI_TEST_BINARY", binary)
	t.Setenv("ARO_HCP_TESTS_BINARY", launcher)
	t.Setenv("ARO_HCP_INFRA_TEST_PROCESS", "1")
	t.Setenv("DEPLOY_ENV", "dev")
	t.Setenv("SHARED_DIR", t.TempDir())
	registry := "literal;$(touch MUST_NOT_EXIST) 'quoted'\n"
	t.Setenv("USE_OC_LOGIN_REGISTRIES", registry)
	command := exec.Command("bash", "-c", `set -euo pipefail
source "$1"
[[ "$OVERRIDE_CONFIG_FILE" == "$SHARED_DIR/config-override.yaml" ]]
[[ -f "$OVERRIDE_CONFIG_FILE" ]]
[[ ! -e MUST_NOT_EXIST ]]
printf '%s' "$USE_OC_LOGIN_REGISTRIES"
OVERRIDE_CONFIG_FILE=previous-output
export BACKEND_IMAGE=invalid
if source "$1"; then exit 20; fi
[[ "$OVERRIDE_CONFIG_FILE" == previous-output ]]
printf '%s' "$USE_OC_LOGIN_REGISTRIES"
unset BACKEND_IMAGE USE_OC_LOGIN_REGISTRIES
source "$1"
[[ ! -v USE_OC_LOGIN_REGISTRIES ]]
USE_OC_LOGIN_REGISTRIES=unexported.example
source "$1"
[[ "$USE_OC_LOGIN_REGISTRIES" == unexported.example ]]
bash -c '[[ ! -v USE_OC_LOGIN_REGISTRIES ]]'
export BACKEND_IMAGE=registry.example/backend@sha256:1234
source "$1"
bash -c '[[ "$USE_OC_LOGIN_REGISTRIES" == "unexported.example registry.example" ]]'
ln -s "$ARO_HCP_TESTS_BINARY" "$PWD/test/aro-hcp-tests"
unset ARO_HCP_TESTS_BINARY
source "$1"
[[ -f "$OVERRIDE_CONFIG_FILE" ]]
bash -c '[[ "$USE_OC_LOGIN_REGISTRIES" == "unexported.example registry.example registry.example" ]]'
`, "adapter-test", adapter)
	command.Dir = checkout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.Output()
	if err != nil {
		t.Fatalf("adapter: %v: %s", err, stderr.String())
	}
	if string(stdout) != strings.Repeat(registry, 2) {
		t.Fatalf("adapter changed registry data: %q", stdout)
	}
}
