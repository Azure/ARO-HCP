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

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/util/testutil"
)

func TestNonTestCommandsSkipMISchedulerBanner(t *testing.T) {
	root := setupCli()
	root.SetArgs([]string{"list", "suites", "--output", "names"})

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stderr pipe: %v", err)
	}
	defer stderrReader.Close()
	originalStderr := os.Stderr
	os.Stderr = stderrWriter
	defer func() {
		os.Stderr = originalStderr
		stderrWriter.Close()
	}()

	stdout, err := os.CreateTemp("", "test-output-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(stdout.Name())
	defer stdout.Close()

	originalStdout := os.Stdout
	os.Stdout = stdout
	defer func() {
		os.Stdout = originalStdout
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- root.Execute()
		stderrWriter.Close()
	}()

	var stderrBuf bytes.Buffer
	if _, err := io.Copy(&stderrBuf, stderrReader); err != nil {
		t.Fatalf("failed to read stderr: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("failed to execute list suites: %v", err)
	}
	if strings.Contains(stderrBuf.String(), "[scheduler]") {
		t.Fatalf("expected no scheduler banner for list command, got stderr: %s", stderrBuf.String())
	}
}

// captureStderr redirects os.Stderr to a temp file for the duration of fn and returns
// everything written to it. Unlike the os.Pipe pattern above, fn here runs synchronously
// with no concurrent writer, so a plain temp file avoids needing a reader goroutine.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	tmp, err := os.CreateTemp("", "stderr-capture-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	original := os.Stderr
	os.Stderr = tmp
	defer func() { os.Stderr = original }()
	fn()

	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("failed to read captured stderr: %v", err)
	}
	return string(data)
}

func TestParseMIContainersLabel(t *testing.T) {
	tests := []struct {
		name       string
		labels     []string
		wantDemand int
		wantFound  bool
	}{
		{name: "no MIContainers label", labels: nil, wantDemand: 0, wantFound: false},
		{name: "zero demand", labels: []string{"MIContainers:0"}, wantDemand: 0, wantFound: true},
		{name: "positive demand", labels: []string{"MIContainers:3"}, wantDemand: 3, wantFound: true},
		{name: "unrelated labels are ignored", labels: []string{"SLOW", "MIContainers:1"}, wantDemand: 1, wantFound: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &et.ExtensionTestSpec{Name: tt.name, Labels: sets.New(tt.labels...)}
			demand, found := parseMIContainersLabel(spec)
			if demand != tt.wantDemand || found != tt.wantFound {
				t.Fatalf("parseMIContainersLabel() = (%d, %v), want (%d, %v)", demand, found, tt.wantDemand, tt.wantFound)
			}
		})
	}
}

func TestConfigureMISchedulerWiresResourcePools(t *testing.T) {
	newSpecs := func() et.ExtensionTestSpecs {
		return et.ExtensionTestSpecs{
			{Name: "zero-demand", Labels: sets.New("MIContainers:0")},
			{Name: "high-demand", Labels: sets.New("MIContainers:2")},
			{Name: "low-demand", Labels: sets.New("MIContainers:1")},
		}
	}

	t.Run("pooled identities enabled", func(t *testing.T) {
		specs := newSpecs()
		stderr := captureStderr(t, func() {
			configureMIScheduler(specs, miSchedulerConfig{pooledIdentitiesEnabled: true, containerCount: 5, containerCountSource: "test"})
		})

		for _, spec := range specs {
			demand, _ := parseMIContainersLabel(spec)
			if got := spec.Resources.ResourcePools["mi-containers"]; got != demand {
				t.Fatalf("expected spec %q mi-containers pool demand %d, got %d", spec.Name, demand, got)
			}
		}
		if !strings.Contains(stderr, "[scheduler] pool mi-containers=5") {
			t.Fatalf("expected scheduler banner reporting the pool size, got: %s", stderr)
		}
	})

	t.Run("pooled identities disabled", func(t *testing.T) {
		specs := newSpecs()
		stderr := captureStderr(t, func() {
			configureMIScheduler(specs, miSchedulerConfig{pooledIdentitiesEnabled: false, containerCount: 5, containerCountSource: "test"})
		})

		for _, spec := range specs {
			if len(spec.Resources.ResourcePools) != 0 {
				t.Fatalf("expected no ResourcePools wiring when pooled identities are disabled, got %v on %q", spec.Resources.ResourcePools, spec.Name)
			}
		}
		if !strings.Contains(stderr, "pooled identities disabled") {
			t.Fatalf("expected scheduler banner noting pooled identities are disabled, got: %s", stderr)
		}
	})
}

func TestEnsureMISchedulerConfiguredRunsOnce(t *testing.T) {
	origSpecs, origSetup := miSchedulerSpecs, miSchedulerSetup
	defer func() {
		miSchedulerSpecs, miSchedulerSetup = origSpecs, origSetup
		miSchedulerConfigure = sync.Once{}
	}()

	miSchedulerSpecs = et.ExtensionTestSpecs{{Name: "once-test", Labels: sets.New("MIContainers:1")}}
	miSchedulerSetup = miSchedulerConfig{pooledIdentitiesEnabled: false, containerCount: 1, containerCountSource: "test"}
	miSchedulerConfigure = sync.Once{}

	stderr := captureStderr(t, func() {
		ensureMISchedulerConfigured()
		ensureMISchedulerConfigured()
		ensureMISchedulerConfigured()
	})

	if got := strings.Count(stderr, "[scheduler]"); got != 1 {
		t.Fatalf("expected configureMIScheduler to run exactly once across repeated PreRun invocations, got %d banners in stderr: %s", got, stderr)
	}
}

// TestConfigureMISchedulerFatalOnMissingLabel exercises the FATAL/os.Exit(1) path
// configureMIScheduler takes for a spec missing the MIContainers label -- the failure
// mode this PR's PreRun deferral is meant to preserve for run-suite/run-test while no
// longer triggering it for every other subcommand.
func TestConfigureMISchedulerFatalOnMissingLabel(t *testing.T) {
	if os.Getenv("ARO_HCP_TESTS_CRASH_TEST") == "1" {
		spec := &et.ExtensionTestSpec{Name: "unlabeled-test", Labels: sets.New[string]()}
		configureMIScheduler(et.ExtensionTestSpecs{spec}, miSchedulerConfig{pooledIdentitiesEnabled: true, containerCount: 1, containerCountSource: "test"})
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestConfigureMISchedulerFatalOnMissingLabel$")
	cmd.Env = append(os.Environ(), "ARO_HCP_TESTS_CRASH_TEST=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected configureMIScheduler to exit(1) for a spec missing the MIContainers label, got err=%v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "FATAL") || !strings.Contains(stderr.String(), "unlabeled-test") {
		t.Fatalf("expected a FATAL message naming the offending spec, got: %s", stderr.String())
	}
}

func TestMainListSuitesForEachSuite(t *testing.T) {
	type testCase struct {
		suite             string
		suffix            string
		setDevelopmentEnv bool
	}

	tests := []testCase{
		{suite: "integration/parallel", suffix: "integration-parallel"},
		{suite: "stage/parallel", suffix: "stage-parallel"},
		{suite: "prod/parallel", suffix: "prod-parallel"},
		{suite: "dev-cd-check/parallel", suffix: "dev-cd-check-parallel"},
		{suite: "rp-api-compat-all/parallel", suffix: "rp-api-compat-all-parallel"},
		{suite: "rp-api-compat-all/parallel", suffix: "rp-api-compat-all-parallel-development", setDevelopmentEnv: true},
		{suite: "hypershift-presubmit/parallel", suffix: "hypershift-presubmit-parallel"},
		{suite: "upgrade/in-place", suffix: "upgrade-in-place"},
	}

	for _, test := range tests {
		t.Run(test.suite, func(t *testing.T) {
			if test.setDevelopmentEnv {
				os.Setenv("AROHCP_ENV", "development")
			}
			root := setupCli()
			root.SetArgs([]string{"list", "tests", "--suite", test.suite, "--output", "names"})

			mktempfile, err := os.CreateTemp("", "test-output-*.txt")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			defer os.Remove(mktempfile.Name())
			defer mktempfile.Close()

			// Capture stdout to verify the command executes successfully
			originalStdout := os.Stdout
			os.Stdout = mktempfile
			defer func() {
				os.Stdout = originalStdout
			}()

			err = root.Execute()
			if err != nil {
				t.Fatalf("failed to execute command for suite %s: %v", test.suite, err)
			}
			testutil.CompareFileWithFixture(t, mktempfile.Name(), testutil.WithSuffix(test.suffix))

		})
	}
}

func TestWriteEV2RetryMetadata(t *testing.T) {
	sampleSummary := ev2SuiteSummary{Total: 3, Passed: 1, Failed: 2, Skipped: 0, DurationSeconds: 12.5}

	t.Run("empty artifact dir is skipped without error", func(t *testing.T) {
		if err := writeEV2RetryMetadata("", []string{"spec A"}, []string{"spec A"}, sampleSummary); err != nil {
			t.Fatalf("expected no error when ARTIFACT_DIR is unset, got %v", err)
		}
	})

	t.Run("writes a fresh metadata.json", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeEV2RetryMetadata(dir, []string{"spec A", "spec B"}, []string{"spec A"}, sampleSummary); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readMetadataFile(t, dir)
		failed, ok := got[ev2FailedTestsKey].([]interface{})
		if !ok || len(failed) != 2 {
			t.Fatalf("expected 2 failed test names recorded, got %v", got[ev2FailedTestsKey])
		}
		allowRetry, ok := got[ev2AllowRetryTestsKey].([]interface{})
		if !ok || len(allowRetry) != 1 {
			t.Fatalf("expected 1 allow-retry test name recorded, got %v", got[ev2AllowRetryTestsKey])
		}
		summary, ok := got[ev2SuiteSummaryKey].(map[string]interface{})
		if !ok {
			t.Fatalf("expected %s to be an object, got %v", ev2SuiteSummaryKey, got[ev2SuiteSummaryKey])
		}
		if summary["total"] != float64(3) {
			t.Fatalf("expected total=3, got %v", summary["total"])
		}
		if summary["passed"] != float64(1) {
			t.Fatalf("expected passed=1, got %v", summary["passed"])
		}
		if summary["failed"] != float64(2) {
			t.Fatalf("expected failed=2, got %v", summary["failed"])
		}
		if summary["skipped"] != float64(0) {
			t.Fatalf("expected skipped=0, got %v", summary["skipped"])
		}
		if summary["duration-seconds"] != 12.5 {
			t.Fatalf("expected duration-seconds=12.5, got %v", summary["duration-seconds"])
		}
	})

	t.Run("writes empty lists when nothing failed, so the keys are always present", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeEV2RetryMetadata(dir, nil, nil, ev2SuiteSummary{Total: 5, Passed: 5}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readMetadataFile(t, dir)
		failed, ok := got[ev2FailedTestsKey].([]interface{})
		if !ok || len(failed) != 0 {
			t.Fatalf("expected an empty failed-tests list, got %v", got[ev2FailedTestsKey])
		}
		allowRetry, ok := got[ev2AllowRetryTestsKey].([]interface{})
		if !ok || len(allowRetry) != 0 {
			t.Fatalf("expected an empty allow-retry-tests list, got %v", got[ev2AllowRetryTestsKey])
		}
		summary, ok := got[ev2SuiteSummaryKey].(map[string]interface{})
		if !ok {
			t.Fatalf("expected %s to be an object, got %v", ev2SuiteSummaryKey, got[ev2SuiteSummaryKey])
		}
		if summary["total"] != float64(5) {
			t.Fatalf("expected total=5, got %v", summary["total"])
		}
	})

	t.Run("merges into an existing metadata.json without clobbering other keys", func(t *testing.T) {
		dir := t.TempDir()
		existing := map[string]interface{}{"some-other-step": "wrote-this"}
		data, err := json.Marshal(existing)
		if err != nil {
			t.Fatalf("failed to marshal fixture: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, ev2RetryMetadataFile), data, 0o644); err != nil {
			t.Fatalf("failed to write fixture: %v", err)
		}

		if err := writeEV2RetryMetadata(dir, []string{"spec A"}, []string{"spec A"}, sampleSummary); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readMetadataFile(t, dir)
		if got["some-other-step"] != "wrote-this" {
			t.Fatalf("expected pre-existing key to survive the merge, got %v", got)
		}
		failed, ok := got[ev2FailedTestsKey].([]interface{})
		if !ok || len(failed) != 1 {
			t.Fatalf("expected 1 failed test name recorded, got %v", got[ev2FailedTestsKey])
		}
	})
}

func readMetadataFile(t *testing.T, dir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ev2RetryMetadataFile))
	if err != nil {
		t.Fatalf("failed to read %s: %v", ev2RetryMetadataFile, err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("failed to parse %s: %v", ev2RetryMetadataFile, err)
	}
	return got
}
