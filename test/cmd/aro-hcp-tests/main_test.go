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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd/cmdrun"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/e2eidentities"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/testutil"
)

func TestNonTestCommandsSkipMISchedulerBanner(t *testing.T) {
	t.Setenv("ARO_HCP_SUITE_PARALLELISM", "")
	root, err := setupCli()
	if err != nil {
		t.Fatal(err)
	}
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

func specNames(specs et.ExtensionTestSpecs) []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

func assertSpecOrder(t *testing.T, specs et.ExtensionTestSpecs, want []string) {
	t.Helper()
	got := specNames(specs)
	if len(got) != len(want) {
		t.Fatalf("expected specs %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected specs sorted as %v, got %v", want, got)
		}
	}
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

func TestMIDemandPriority(t *testing.T) {
	labeled := &et.ExtensionTestSpec{Name: "labeled", Labels: sets.New("MIContainers:4")}
	if got := miDemandPriority(labeled); got != 4 {
		t.Fatalf("miDemandPriority() = %d, want 4", got)
	}
	unlabeled := &et.ExtensionTestSpec{Name: "unlabeled", Labels: sets.New[string]()}
	if got := miDemandPriority(unlabeled); got != 0 {
		t.Fatalf("miDemandPriority() for a spec missing the label = %d, want 0", got)
	}
}

func TestConfigureMISchedulerWiresResourcePoolsAndSortsByDemand(t *testing.T) {
	t.Setenv("ARO_HCP_DISABLE_MI_SORT", "")

	newSpecs := func() et.ExtensionTestSpecs {
		return et.ExtensionTestSpecs{
			{Name: "zero-demand", Labels: sets.New("MIContainers:0")},
			{Name: "high-demand", Labels: sets.New("MIContainers:2")},
			{Name: "low-demand", Labels: sets.New("MIContainers:1")},
		}
	}
	wantOrder := []string{"high-demand", "low-demand", "zero-demand"}

	t.Run("pooled identities enabled", func(t *testing.T) {
		specs := newSpecs()
		stderr := captureStderr(t, func() {
			configureMIScheduler(specs, miSchedulerConfig{pooledIdentitiesEnabled: true, containerCount: 5, containerCountSource: "test"})
		})

		assertSpecOrder(t, specs, wantOrder)
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

		assertSpecOrder(t, specs, wantOrder)
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

// TestMISchedulerSortReflectedInExtensionSpecs guards against the bug fixed in
// ARO-29044: initMIScheduler must be wired to ext.GetSpecs(), not the local specs
// slice from before ext.AddSpecs(specs). AddSpecs does e.specs = append(e.specs,
// specs...), which allocates a new backing array -- wiring the pre-AddSpecs slice
// would make the demand-priority sort reorder a slice run-suite/run-test never reads.
func TestMISchedulerSortReflectedInExtensionSpecs(t *testing.T) {
	origSpecs, origSetup := miSchedulerSpecs, miSchedulerSetup
	defer func() {
		miSchedulerSpecs, miSchedulerSetup = origSpecs, origSetup
		miSchedulerConfigure = sync.Once{}
	}()
	t.Setenv("ARO_HCP_DISABLE_MI_SORT", "")

	specs := et.ExtensionTestSpecs{
		{Name: "low-demand", Labels: sets.New("MIContainers:0")},
		{Name: "high-demand", Labels: sets.New("MIContainers:2")},
	}

	ext := e.NewExtension("test", "payload", "mi-scheduler-regression")
	ext.AddSpecs(specs)

	initMIScheduler(ext.GetSpecs(), miSchedulerConfig{pooledIdentitiesEnabled: false, containerCount: 1, containerCountSource: "test"})
	miSchedulerConfigure = sync.Once{}
	captureStderr(t, ensureMISchedulerConfigured)

	assertSpecOrder(t, ext.GetSpecs(), []string{"high-demand", "low-demand"})
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
			t.Setenv("ARO_HCP_SUITE_PARALLELISM", "")
			root, err := setupCli()
			if err != nil {
				t.Fatal(err)
			}
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

func listSuites(t *testing.T) map[string]e.Suite {
	t.Helper()
	output, err := os.CreateTemp(t.TempDir(), "suites-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	root, err := setupCli()
	if err != nil {
		t.Fatalf("setting up CLI: %v", err)
	}
	t.Setenv("ARO_HCP_SUITE_PARALLELISM", "invalid-after-construction")
	root.SetArgs([]string{"list", "suites", "--output", "json"})
	originalStdout := os.Stdout
	func() {
		os.Stdout = output
		defer func() { os.Stdout = originalStdout }()
		err = root.Execute()
	}()
	if err != nil {
		t.Fatalf("listing suites: %v", err)
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var suites []e.Suite
	if err := json.NewDecoder(output).Decode(&suites); err != nil {
		t.Fatalf("decoding listed suites: %v", err)
	}
	byName := make(map[string]e.Suite, len(suites))
	for _, suite := range suites {
		byName[suite.Name] = suite
	}
	return byName
}

func TestSuiteConcurrencyMetadata(t *testing.T) {
	t.Setenv("ARO_HCP_SUITE_PARALLELISM", "")
	t.Setenv(framework.UsePooledIdentitiesEnvvar, "false")
	baseline := listSuites(t)
	fallbacks := map[string]int{
		"integration/parallel": 24, "integration/parallel/slow": 24, "integration/parallel/all": 24,
		"stage/parallel": 34, "stage/parallel/slow": 34, "stage/parallel/all": 34,
		"prod/parallel": 19, "prod/parallel/slow": 19,
		"dev-cd-check/parallel": 20, "rp-api-compat-all/parallel": 24,
		"rp-api-compat-all/parallel/slow": 24, "hypershift-presubmit/parallel": 24,
		"upgrade/in-place": baseline["upgrade/in-place"].Parallelism,
	}
	for name, want := range fallbacks {
		if got := baseline[name].Parallelism; got != want {
			t.Errorf("%s fallback = %d, want %d", name, got, want)
		}
	}
	acquired := sourceIdentityLease(t)
	for _, pooled := range []string{"false", "true"} {
		for _, lease := range []string{"", " \t ", acquired} {
			for _, override := range []string{"unset", "", "1", "60"} {
				t.Run(fmt.Sprintf("pooled=%s/lease=%q/override=%s", pooled, lease, override), func(t *testing.T) {
					t.Setenv(framework.UsePooledIdentitiesEnvvar, pooled)
					t.Setenv(leaseenv.MSIContainers, lease)
					t.Setenv("ARO_HCP_SUITE_PARALLELISM", override)
					if override == "unset" {
						if err := os.Unsetenv("ARO_HCP_SUITE_PARALLELISM"); err != nil {
							t.Fatal(err)
						}
					}
					suites := listSuites(t)
					if len(suites) != len(fallbacks) {
						t.Fatalf("listed %d suites, want %d", len(suites), len(fallbacks))
					}
					for name, fallback := range fallbacks {
						want := fallback
						if override == "1" {
							want = 1
						}
						if override == "60" {
							want = 60
						}
						usesAcquired := strings.HasPrefix(name, "integration/") || strings.HasPrefix(name, "stage/") || strings.HasPrefix(name, "prod/") ||
							strings.HasPrefix(name, "rp-api-compat-all/") || name == "hypershift-presubmit/parallel"
						if override == "unset" && lease == acquired && usesAcquired {
							want = 2
						}
						if suites[name].Parallelism != want {
							t.Errorf("%s workers = %d, want %d", name, suites[name].Parallelism, want)
						}
						wantPool := 0
						if pooled == "true" && name != "upgrade/in-place" {
							wantPool = DefaultMIContainerCount
							if lease == acquired {
								wantPool = 2
							}
						}
						if got := suites[name].ResourcePools["mi-containers"]; got != wantPool {
							t.Errorf("%s MI pool = %d, want %d", name, got, wantPool)
						}
					}
				})
			}
		}
	}
}

// Source the actual handler's published runtime file, including the downstream export boundary.
func sourceIdentityLease(t *testing.T) string {
	t.Helper()
	contract := slots.NewRuntimeContractBuilder()
	request := assets.LeaseRequest{AcquiredSlotState: &slots.AcquiredSlotState{Slot: slots.ExpandedSlot{
		IdentityContainerCount: 10,
		Assets: slots.ResolvedAssets{E2EIdentities: &slots.ResolvedE2EIdentitiesAsset{
			ResourceGroups: []string{"identity-rg-02", "identity-rg-01"},
		}},
	}}}
	if err := e2eidentities.NewHandler().PublishLease(context.Background(), request, contract); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := slots.WriteRuntimeContract(dir, contract); err != nil {
		t.Fatal(err)
	}
	envFile, err := slots.EnvFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("sh", "-eu", "-c", `unset ARO_HCP_SUITE_PARALLELISM; . "$1"; sh -eu -c 'env -0'`, "sh", envFile).Output()
	if err != nil {
		t.Fatal(err)
	}
	var lease string
	for _, entry := range strings.Split(string(output), "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if key == "ARO_HCP_SUITE_PARALLELISM" {
			t.Fatalf("lease unexpectedly published global user override %q", value)
		}
		if key == leaseenv.MSIContainers {
			lease = value
		}
	}
	if lease != "identity-rg-02 identity-rg-01" {
		t.Fatalf("published lease = %q", lease)
	}
	return lease
}

func TestInvalidSuiteConcurrencyConstruction(t *testing.T) {
	for _, value := range []string{"invalid", "0", "-1", "1.5", " 75 ", `{"int":75}`, "999999999999999999999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ARO_HCP_SUITE_PARALLELISM", value)
			root, err := setupCli()
			if err == nil || !strings.Contains(err.Error(), "ARO_HCP_SUITE_PARALLELISM") || root != nil {
				t.Fatalf("construction = %v, %v; want nil CLI and actionable environment error before flags execute", root, err)
			}
		})
	}
}

func TestSuiteConcurrencyRunSuiteErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		missingFlag bool
		wantError   string
	}{
		{name: "missing concurrency flag", args: []string{"specialized"}, missingFlag: true, wantError: "missing the expected --max-concurrency flag"},
		{name: "unknown component", args: []string{"specialized", "--component", "unknown"}, wantError: "component not found: unknown"},
		{name: "unknown suite", args: []string{"unknown"}, wantError: `couldn't find suite "unknown": no such suite: unknown`},
		{name: "missing suite", wantError: "must specify one suite name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := e.NewExtension("aro-hcp", "payload", "errors")
			ext.AddSuite(e.Suite{Name: "specialized", Parallelism: 24})
			registry := e.NewRegistry()
			registry.Register(ext)
			command := cmdrun.NewRunSuiteCommand(registry)
			if tc.missingFlag {
				command.ResetFlags()
				command.Flags().String("component", e.DefaultExtension, "")
			}
			wrapCommandHooks(command, commandHooks{AfterPreRun: []commandHook{configureSuiteConcurrency(registry)}})
			command.SetArgs(tc.args)
			var err error
			captureStderr(t, func() { err = command.Execute() })
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("run-suite error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestSuiteConcurrencyUsesActualRunner(t *testing.T) {
	acquiredLease := sourceIdentityLease(t)
	for _, tc := range []struct {
		name        string
		suite       string
		parallelism int
		env         string
		pooled      bool
		acquired    bool
		explicit    string
		demand      int
		want        int
	}{
		{name: "original integration default without pooling", suite: "integration/parallel", parallelism: 24, want: 24},
		{name: "original stage default with pooling", suite: "stage/parallel/all", parallelism: 34, pooled: true, want: 34},
		{name: "environment without pooling", suite: "integration/parallel/slow", parallelism: 24, env: "60", want: 60},
		{name: "environment with pooling", suite: "stage/parallel/slow", parallelism: 34, env: "75", pooled: true, want: 75},
		{name: "explicit exceeds environment", suite: "integration/parallel/all", parallelism: 24, env: "60", explicit: "100", want: 60},
		{name: "explicit exceeds suite default", suite: "prod/parallel/slow", parallelism: 19, explicit: "100", want: 19},
		{name: "explicit below environment", suite: "integration/parallel", parallelism: 24, env: "75", explicit: "25", want: 25},
		{name: "leased pool still limits demand", suite: "stage/parallel", parallelism: 34, env: "100", explicit: "100", pooled: true, demand: 1, want: 75},
		{name: "upgrade explicit respects suite cap", suite: "upgrade/in-place", parallelism: 20, explicit: "100", want: 20},
		{name: "dev CD environment override", suite: "dev-cd-check/parallel", parallelism: 20, env: "60", want: 60},
		{name: "RP API explicit cap", suite: "rp-api-compat-all/parallel/slow", parallelism: 24, env: "60", explicit: "100", want: 60},
		{name: "hypershift original default", suite: "hypershift-presubmit/parallel", parallelism: 24, want: 24},
		{name: "unlisted future suite", suite: "future/specialized", parallelism: 3, explicit: "20", want: 3},
		{name: "positive environment boundary", suite: "prod/parallel", parallelism: 19, env: "1", want: 1},
		{name: "acquired ordinary workers without pooling", suite: "integration/parallel", acquired: true, env: "unset", want: 2},
		{name: "acquired ordinary workers with pooling", suite: "stage/parallel", acquired: true, pooled: true, env: "unset", want: 2},
		{name: "acquired explicit empty opt-out", suite: "prod/parallel", acquired: true, want: 19},
		{name: "explicit flag respects acquired default", suite: "integration/parallel", acquired: true, env: "unset", explicit: "4", want: 2},
		{name: "user override beats acquired default", suite: "integration/parallel", acquired: true, env: "4", want: 4},
		{name: "low lease dev CD default", suite: "dev-cd-check/parallel", acquired: true, env: "unset", want: 20},
		{name: "low lease RP API default", suite: "rp-api-compat-all/parallel", acquired: true, env: "unset", want: 2},
		{name: "low lease RP API slow default", suite: "rp-api-compat-all/parallel/slow", acquired: true, env: "unset", want: 2},
		{name: "low lease hypershift default", suite: "hypershift-presubmit/parallel", acquired: true, env: "unset", want: 2},
		{name: "large lease RP API default", suite: "rp-api-compat-all/parallel", env: "unset", explicit: "100", want: 75},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARO_HCP_SUITE_PARALLELISM", tc.env)
			if tc.env == "unset" {
				if err := os.Unsetenv("ARO_HCP_SUITE_PARALLELISM"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(leaseenv.MSIContainers, strings.Repeat("container ", 75))
			if tc.acquired {
				t.Setenv(leaseenv.MSIContainers, acquiredLease)
			}
			count, _ := parseMIContainerCount()
			var pools map[string]int
			if tc.pooled {
				pools = map[string]int{"mi-containers": count}
			}
			ext := e.NewExtension("aro-hcp", "payload", "concurrency-test")
			workers, err := parseSuiteConcurrency(count)
			if err != nil {
				t.Fatal(err)
			}
			registerSuites(ext, pools, 20, workers)
			if tc.suite == "future/specialized" {
				ext.AddSuite(e.Suite{Name: tc.suite, Parallelism: tc.parallelism})
			}
			registered, err := ext.GetSuite(tc.suite)
			if err != nil {
				t.Fatal(err)
			}
			// Later environment changes cannot affect the already-registered policy.
			t.Setenv("ARO_HCP_SUITE_PARALLELISM", "invalid-after-construction")
			specLabels := sets.New[string](labels.RequireNothing[0], labels.AroRpApiCompatible[0], labels.Positive[0])
			if strings.HasSuffix(tc.suite, "/slow") {
				specLabels.Insert(labels.Slow[0])
			}
			if strings.HasPrefix(tc.suite, "hypershift-") {
				specLabels.Insert(labels.HypershiftPresubmit[0])
			}
			if tc.suite == "upgrade/in-place" {
				specLabels.Insert(labels.UpgradeInPlace[0])
			}
			var mu sync.Mutex
			active, peak := 0, 0
			started := make(chan struct{}, 110)
			release := make(chan struct{})
			var specs et.ExtensionTestSpecs
			var demands map[string]int
			if tc.demand > 0 {
				demands = map[string]int{"mi-containers": tc.demand}
			}
			for i := range 110 {
				specs = append(specs, &et.ExtensionTestSpec{
					Name:   fmt.Sprintf("concurrency-%03d", i),
					Labels: specLabels,
					Resources: et.Resources{
						ResourcePools: demands,
					},
					Run: func(context.Context) *et.ExtensionTestResult {
						mu.Lock()
						active++
						if active > peak {
							peak = active
						}
						mu.Unlock()
						started <- struct{}{}
						<-release
						mu.Lock()
						active--
						mu.Unlock()
						return &et.ExtensionTestResult{Result: et.ResultPassed}
					},
				})
			}
			ext.AddSpecs(specs)
			registry := e.NewRegistry()
			// Select a non-default component so registry lookup cannot silently
			// target the first registered extension instead.
			registry.Register(e.NewExtension("other", "payload", "default"))
			registry.Register(ext)
			command := cmdrun.NewRunSuiteCommand(registry)
			wrapCommandHooks(command, commandHooks{AfterPreRun: []commandHook{configureSuiteConcurrency(registry)}})
			args := []string{tc.suite, "--component", ext.Component.Identifier()}
			if tc.explicit != "" {
				args = append(args, "--max-concurrency", tc.explicit)
			}
			command.SetArgs(args)
			output, err := os.CreateTemp(t.TempDir(), "results-*.json")
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			originalStdout := os.Stdout
			os.Stdout = output
			defer func() { os.Stdout = originalStdout }()
			done := make(chan error, 1)
			go func() { done <- command.Execute() }()
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			reached := 0
			for reached < tc.want {
				select {
				case <-started:
					reached++
				case <-timer.C:
					close(release)
					err := <-done
					t.Fatalf("runner started %d simultaneous tests, want %d; execution error: %v", reached, tc.want, err)
				}
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatalf("executing real run-suite: %v", err)
			}
			selected, err := ext.GetSuite(tc.suite)
			if err != nil {
				t.Fatal(err)
			}
			flagValue, err := command.Flags().GetInt("max-concurrency")
			wantFlag := fmt.Sprint(registered.Parallelism)
			if tc.explicit != "" {
				wantFlag = tc.explicit
			}
			if err != nil || fmt.Sprint(flagValue) != wantFlag || selected.Parallelism != registered.Parallelism {
				t.Errorf("worker flag/cap = %d/%d (%v), want %s/%d", flagValue, selected.Parallelism, err, wantFlag, registered.Parallelism)
			}
			wantPool := 0
			if tc.pooled {
				wantPool = count
			}
			if got := selected.ResourcePools["mi-containers"]; got != wantPool {
				t.Errorf("MI pool capacity = %d, want unchanged %d", got, wantPool)
			}
			if peak != tc.want {
				t.Errorf("actual runner peak concurrency = %d, want %d", peak, tc.want)
			}
		})
	}
}

func TestAcquiredLeaseUpgradeCohortUsesActualRunner(t *testing.T) {
	const participants = 3
	lease := sourceIdentityLease(t)
	for _, pooled := range []bool{false, true} {
		t.Run(fmt.Sprintf("pooled=%t", pooled), func(t *testing.T) {
			t.Setenv("ARO_HCP_SUITE_PARALLELISM", "")
			if err := os.Unsetenv("ARO_HCP_SUITE_PARALLELISM"); err != nil {
				t.Fatal(err)
			}
			t.Setenv(leaseenv.MSIContainers, lease)
			count, source := parseMIContainerCount()
			if source != leaseenv.MSIContainers || count != 2 {
				t.Fatalf("acquired count/source = %d/%s", count, source)
			}
			workers, err := parseSuiteConcurrency(count)
			if err != nil {
				t.Fatal(err)
			}
			var pools map[string]int
			if pooled {
				pools = map[string]int{"mi-containers": count}
			}
			ext := e.NewExtension("aro-hcp", "payload", "upgrade-cohort")
			registerSuites(ext, pools, participants, workers)
			arrived := 0
			var mu sync.Mutex
			barrier := make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var specs et.ExtensionTestSpecs
			for i := range participants {
				specs = append(specs, &et.ExtensionTestSpec{
					Name:   fmt.Sprintf("upgrade-participant-%d", i),
					Labels: sets.New(labels.UpgradeInPlace[0]),
					Run: func(context.Context) *et.ExtensionTestResult {
						mu.Lock()
						arrived++
						if arrived == participants {
							close(barrier)
						}
						mu.Unlock()
						select {
						case <-barrier:
							return &et.ExtensionTestResult{Result: et.ResultPassed}
						case <-ctx.Done():
							return &et.ExtensionTestResult{Result: et.ResultFailed}
						}
					},
				})
			}
			ext.AddSpecs(specs)
			registry := e.NewRegistry()
			registry.Register(ext)
			command := cmdrun.NewRunSuiteCommand(registry)
			commandHookRan, rootHook := false, false
			command.PreRun = func(*cobra.Command, []string) { commandHookRan = true }
			wrapCommandHooks(command, commandHooks{AfterPreRun: []commandHook{configureSuiteConcurrency(registry)}})
			root := &cobra.Command{PersistentPreRun: func(*cobra.Command, []string) { rootHook = true }}
			root.AddCommand(command)
			root.SetArgs([]string{"run-suite", "upgrade/in-place"})
			output, err := os.CreateTemp(t.TempDir(), "upgrade-results-*.json")
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			originalStdout := os.Stdout
			os.Stdout = output
			defer func() { os.Stdout = originalStdout }()
			err = root.ExecuteContext(ctx)
			if err != nil || ctx.Err() != nil {
				t.Fatalf("complete upgrade cohort failed to cross barrier: %v, context: %v", err, ctx.Err())
			}
			if arrived != participants {
				t.Fatalf("started %d participants, want %d", arrived, participants)
			}
			if !commandHookRan || !rootHook {
				t.Fatalf("preserved hooks: command=%t root=%t", commandHookRan, rootHook)
			}
		})
	}
}

func TestCombinedParallelAllSuitesIncludeFastAndSlow(t *testing.T) {
	listNames := func(suite string) map[string]bool {
		t.Helper()
		output, err := os.CreateTemp(t.TempDir(), "suite-names-*.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()

		t.Setenv("ARO_HCP_SUITE_PARALLELISM", "")
		root, err := setupCli()
		if err != nil {
			t.Fatal(err)
		}
		root.SetArgs([]string{"list", "tests", "--suite", suite, "--output", "names"})
		originalStdout := os.Stdout
		func() {
			os.Stdout = output
			defer func() { os.Stdout = originalStdout }()
			err = root.Execute()
		}()
		if err != nil {
			t.Fatalf("listing %s tests: %v", suite, err)
		}
		if _, err := output.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(output)
		if err != nil {
			t.Fatal(err)
		}
		names := make(map[string]bool)
		for _, name := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if name != "" {
				names[name] = true
			}
		}
		return names
	}

	cases := []struct {
		env  string
		fast string
		slow string
		all  string
	}{
		{
			env:  "Integration",
			fast: "integration/parallel",
			slow: "integration/parallel/slow",
			all:  "integration/parallel/all",
		},
		{
			env:  "Stage",
			fast: "stage/parallel",
			slow: "stage/parallel/slow",
			all:  "stage/parallel/all",
		},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			fast := listNames(tc.fast)
			slow := listNames(tc.slow)
			all := listNames(tc.all)
			if len(fast) == 0 || len(slow) == 0 {
				t.Fatalf("expected nonempty fast and Slow suites, got %d and %d", len(fast), len(slow))
			}
			for name := range slow {
				if fast[name] {
					t.Fatalf("test %q belongs to both fast and Slow suites", name)
				}
				fast[name] = true
			}
			if len(all) != len(fast) {
				t.Fatalf("combined %s suite has %d tests, expected %d", tc.env, len(all), len(fast))
			}
			for name := range fast {
				if !all[name] {
					t.Fatalf("combined %s suite is missing %q", tc.env, name)
				}
			}
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
