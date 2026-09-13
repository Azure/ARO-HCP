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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/Azure/ARO-HCP/test/e2e"

	"github.com/go-logr/stdr"
	"github.com/onsi/gomega/format"
	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	// If using ginkgo, import your tests here
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/cleanup"
	customlinktools "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/custom-link-tools"
	gatherobservability "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/gather-observability"
	gathersnapshot "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/gather-snapshot"
	mergegate "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/merge-gate"
	slotmanager "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/visualize"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

func fastTestsOnly(query string) string {
	return fmt.Sprintf("%s && !labels.exists(l, l==\"%s\")", query, labels.Slow[0])
}

func slowTestsOnly(query string) string {
	return fmt.Sprintf("%s && labels.exists(l, l==\"%s\")", query, labels.Slow[0])
}

// parseSuiteParallelismOverride reads ARO_HCP_SUITE_PARALLELISM and
// returns a non-nil pointer when a valid override is present.
func parseSuiteParallelismOverride() *int {
	v := os.Getenv("ARO_HCP_SUITE_PARALLELISM")
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		fmt.Fprintf(os.Stderr, "WARNING: ARO_HCP_SUITE_PARALLELISM=%q is not a valid positive integer, ignoring override\n", v)
		return nil
	}
	return &n
}

// DefaultMIContainerCount is the assumed MI container pool size when
// the LEASED_MSI_CONTAINERS envvar is not set. It matches the typical
// personal-dev environment slot count so local runs work out of the box.
const DefaultMIContainerCount = 15

func parseMIContainersLabel(spec *et.ExtensionTestSpec) (int, bool) {
	var seen []string
	var parsed int
	var found bool

	for label := range spec.Labels {
		if v, ok := strings.CutPrefix(label, "MIContainers:"); ok {
			seen = append(seen, label)
			n, err := strconv.Atoi(v)
			if err == nil {
				if n < 0 {
					fmt.Fprintf(os.Stderr, "FATAL: test %q has MIContainers:%d but N must be >= 0\n", spec.Name, n)
					os.Exit(1)
				}
				parsed = n
				found = true
			}
		}
	}

	if len(seen) > 1 {
		sort.Strings(seen)
		fmt.Fprintf(os.Stderr, "FATAL: test %q has multiple MIContainers labels (%s); exactly one is required\n",
			spec.Name, strings.Join(seen, ", "))
		os.Exit(1)
	}
	if found {
		return parsed, true
	}
	return 0, false
}

// parseMIContainerCount returns the MI container pool size and a
// human-readable source string. The pool size is the number of
// space-delimited entries in LEASED_MSI_CONTAINERS, or
// DefaultMIContainerCount when the envvar is unset or empty.
func parseMIContainerCount() (int, string) {
	v := os.Getenv(framework.LeasedMSIContainersEnvvar)
	if v == "" {
		return DefaultMIContainerCount, fmt.Sprintf("default (%s not set)", framework.LeasedMSIContainersEnvvar)
	}
	count := len(strings.Fields(v))
	if count == 0 {
		return DefaultMIContainerCount, fmt.Sprintf("default (%s empty)", framework.LeasedMSIContainersEnvvar)
	}
	return count, framework.LeasedMSIContainersEnvvar
}

// ev2FailedTestsKey and ev2AllowRetryTestsKey are the finished.json metadata keys
// prow-job-executor reads (see AROSLSRE-1721) to decide whether an EV2 gating job failure
// is safe to auto-retry. They are written into $ARTIFACT_DIR/metadata.json, which Prow's
// sidecar merges verbatim into the job's finished.json under the top-level "metadata"
// object - the standard Prow custom-metadata mechanism (see sigs.k8s.io/prow/pkg/sidecar),
// rather than a log line prow-job-executor would otherwise have to scrape out of the build
// log.
//
// aro-hcp-tests only reports raw facts here - which specs failed, and which of those carry
// labels.AllowRetry - it does not decide whether a run qualifies for auto-retry. That
// decision (how many failures are tolerable, etc.) is policy that belongs to
// prow-job-executor, which can evolve independently of an ARO-HCP release. See
// retrymarker.go in ARO-Tools for the eligibility logic that consumes these keys.
//
// ev2SuiteSummaryKey is a third, purely informational key reported alongside the two
// above. It isn't used by the EV2 retry decision, but gives anyone looking at a gating
// run's finished.json (a human triaging a failure, or a future dashboard) the basic shape
// of the run - how many specs ran, how many of each result, and how long the suite took
// wall-clock - without having to open the Prow job UI. It's a nested object rather than
// more flat ev2-* keys so its field names (total/passed/failed/skipped/duration-seconds)
// don't read as confusingly similar to the neighboring ev2-failed-tests list (a name list,
// not a count).
const (
	ev2FailedTestsKey     = "ev2-failed-tests"
	ev2AllowRetryTestsKey = "ev2-allow-retry-tests"
	ev2SuiteSummaryKey    = "ev2-suite-summary"
)

// ev2RetryMetadataFile is where Prow's sidecar picks up per-step custom metadata to merge
// into finished.json. See sigs.k8s.io/prow/pkg/pod-utils/decorate.metadataFile.
const ev2RetryMetadataFile = "metadata.json"

// registerEV2RetryCatcher watches test results as they complete and, once the full suite has
// finished, always writes ev2FailedTestsKey, ev2AllowRetryTestsKey, and ev2SuiteSummaryKey
// into $ARTIFACT_DIR/metadata.json - even when nothing failed, in which case both failure
// lists are empty. Writing unconditionally means the keys' presence tells prow-job-executor
// the run reached this point at all, so an absent key is unambiguously "this step didn't
// run", never "the run failed but wasn't retry-eligible". prow-job-executor reads the
// failure lists back out of the job's finished.json to decide whether to resubmit the job
// once, instead of requiring a human to notice the failure, review Prow output, and
// manually retrigger. ev2SuiteSummaryKey isn't used by that decision, but saves a trip to
// the Prow job UI for anyone triaging a run straight from finished.json. See AROSLSRE-1721.
//
// This must only run in the long-lived parent run-suite process. openshift-tests-extension
// spawns each spec as a separate "run-test" worker subprocess, and that subprocess calls
// specs.Run() itself with just its own single spec - which would re-trigger AddAfterEach/
// AddAfterAll in the worker too, reporting one spec's result as if it were the whole
// suite's, and racing multiple workers writing the same file. Guard registration with
// isRunSuiteProcess(), the same pattern used for the upgrade coordinator's AddBeforeAll
// hook above.
func registerEV2RetryCatcher(specs et.ExtensionTestSpecs) {
	if !isRunSuiteProcess() {
		return
	}

	allowRetryNames := map[string]bool{}
	for _, spec := range specs {
		if spec.Labels.Has(labels.AllowRetry[0]) {
			allowRetryNames[spec.Name] = true
		}
	}

	var mu sync.Mutex
	var failedNames []string
	var allowRetryFailedNames []string
	var passedCount, failedCount, skippedCount int
	var suiteStart time.Time

	specs.AddBeforeAll(func() {
		mu.Lock()
		defer mu.Unlock()
		suiteStart = time.Now()
	})

	specs.AddAfterEach(func(res *et.ExtensionTestResult) {
		mu.Lock()
		defer mu.Unlock()
		switch res.Result {
		case et.ResultPassed:
			passedCount++
		case et.ResultSkipped:
			skippedCount++
		case et.ResultFailed:
			failedCount++
			failedNames = append(failedNames, res.Name)
			if allowRetryNames[res.Name] {
				allowRetryFailedNames = append(allowRetryFailedNames, res.Name)
			}
		}
	})

	specs.AddAfterAll(func() {
		mu.Lock()
		defer mu.Unlock()
		// Specs run in parallel, so failedNames/allowRetryFailedNames are accumulated in
		// nondeterministic completion order. Sort before logging/writing metadata.json so
		// the emitted metadata is stable between runs of the same failure set.
		sort.Strings(failedNames)
		sort.Strings(allowRetryFailedNames)
		var durationSeconds float64
		if !suiteStart.IsZero() {
			durationSeconds = roundToDecisecond(time.Since(suiteStart))
		}
		summary := ev2SuiteSummary{
			Total:           passedCount + failedCount + skippedCount,
			Passed:          passedCount,
			Failed:          failedCount,
			Skipped:         skippedCount,
			DurationSeconds: durationSeconds,
		}
		artifactDir := os.Getenv("ARTIFACT_DIR")
		verb, destination := "writing", filepath.Join(artifactDir, ev2RetryMetadataFile)
		if artifactDir == "" {
			verb, destination = "skipping", ev2RetryMetadataFile+" (ARTIFACT_DIR not set)"
		}
		fmt.Fprintf(os.Stderr, "%d test(s) failed (%d labeled %q) out of %d total, suite took %.1fs: %s %s/%s/%s to %s: failed=%v allow-retry=%v\n",
			summary.Failed, len(allowRetryFailedNames), labels.AllowRetry[0], summary.Total, summary.DurationSeconds,
			verb, ev2FailedTestsKey, ev2AllowRetryTestsKey, ev2SuiteSummaryKey, destination, failedNames, allowRetryFailedNames)
		if err := writeEV2RetryMetadata(artifactDir, failedNames, allowRetryFailedNames, summary); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to write EV2 retry metadata: %v\n", err)
		}
	})
}

// ev2SuiteSummary is the basic shape of a suite run, reported under ev2SuiteSummaryKey
// alongside the retry facts so a human (or a future dashboard) can see run size/duration
// without opening the Prow job UI. Field names are deliberately short (no repeated ev2-/
// tests- prefix) since they're already scoped under the ev2-suite-summary object.
type ev2SuiteSummary struct {
	Total           int     `json:"total"`
	Passed          int     `json:"passed"`
	Failed          int     `json:"failed"`
	Skipped         int     `json:"skipped"`
	DurationSeconds float64 `json:"duration-seconds"`
}

// roundToDecisecond rounds d to one decimal place of a second. A suite runs for minutes to
// hours, so the sub-millisecond precision time.Duration.Seconds() returns is meaningless
// noise in the reported metadata.
func roundToDecisecond(d time.Duration) float64 {
	return d.Round(100 * time.Millisecond).Seconds()
}

// writeEV2RetryMetadata merges ev2FailedTestsKey and ev2AllowRetryTestsKey (always present,
// even as empty lists), plus the ev2SuiteSummary, into $ARTIFACT_DIR/metadata.json, creating
// the file if it doesn't exist yet and preserving any keys another step may have already
// written there. artifactDir empty (not running under Prow, e.g. a local run) is not an
// error - it just means there's nowhere to write the signal, so we skip it.
func writeEV2RetryMetadata(artifactDir string, failedNames, allowRetryFailedNames []string, summary ev2SuiteSummary) error {
	if artifactDir == "" {
		fmt.Fprintln(os.Stderr, "WARNING: ARTIFACT_DIR is not set, skipping EV2 retry metadata")
		return nil
	}

	path := filepath.Join(artifactDir, ev2RetryMetadataFile)

	metadata := map[string]interface{}{}
	if existing, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(existing, &metadata); err != nil {
			return fmt.Errorf("failed to parse existing %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing %s: %w", path, err)
	}

	// Always write both failure-list keys, even when empty, so their presence is unambiguous:
	// prow-job-executor can tell "the suite ran and nothing/little failed" apart from "this
	// step never ran".
	metadata[ev2FailedTestsKey] = orEmptySlice(failedNames)
	metadata[ev2AllowRetryTestsKey] = orEmptySlice(allowRetryFailedNames)
	metadata[ev2SuiteSummaryKey] = summary

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", artifactDir, err)
	}
	// Write atomically: this file is an external signal consumed by prow-job-executor for
	// EV2 gating, so a crash or interruption mid-write must never leave a truncated or
	// invalid metadata.json behind for it to read.
	tmp, err := os.CreateTemp(artifactDir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file for %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if n, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp file for %s: %w", path, err)
	} else if n != len(data) {
		tmp.Close()
		return fmt.Errorf("short write to temp file for %s: wrote %d of %d bytes", path, n, len(data))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file for %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("failed to chmod temp file for %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("failed to rename temp file to %s: %w", path, err)
	}
	return nil
}

// orEmptySlice returns a non-nil empty slice in place of nil, so json.Marshal emits "[]"
// instead of "null" for metadata consumers that expect a list.
func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type miSchedulerConfig struct {
	pooledIdentitiesEnabled bool
	containerCount          int
	containerCountSource    string
}

var (
	miSchedulerSetup     miSchedulerConfig
	miSchedulerSpecs     et.ExtensionTestSpecs
	miSchedulerConfigure sync.Once
)

func initMIScheduler(specs et.ExtensionTestSpecs, cfg miSchedulerConfig) {
	miSchedulerSpecs = specs
	miSchedulerSetup = cfg
}

// configureMIScheduler walks specs to wire up per-test MI container demands for the
// openshift-tests-extension resource-aware scheduler. Each spec's MIContainers(N) label
// declares how many pooled identity containers it will lease. When pooled identities are
// enabled, we set spec.Resources.ResourcePools["mi-containers"] = N so the scheduler
// won't start the test until N slots are free in the pool.
func configureMIScheduler(specs et.ExtensionTestSpecs, cfg miSchedulerConfig) {
	var missingLabel []string
	var demand0, demand1, demandN int
	specs.Walk(func(spec *et.ExtensionTestSpec) {
		demand, ok := parseMIContainersLabel(spec)
		if !ok {
			missingLabel = append(missingLabel, spec.Name)
			return
		}
		switch demand {
		case 0:
			demand0++
		case 1:
			demand1++
		default:
			demandN++
		}
		if cfg.pooledIdentitiesEnabled && demand > 0 {
			if spec.Resources.ResourcePools == nil {
				spec.Resources.ResourcePools = make(map[string]int)
			}
			spec.Resources.ResourcePools["mi-containers"] = demand
		}
	})
	if len(missingLabel) > 0 {
		fmt.Fprintf(os.Stderr, "FATAL: %d tests missing MIContainers label:\n", len(missingLabel))
		for _, name := range missingLabel {
			fmt.Fprintf(os.Stderr, "  - %s\n", name)
		}
		os.Exit(1)
	}
	total := demand0 + demand1 + demandN
	if cfg.pooledIdentitiesEnabled {
		fmt.Fprintf(os.Stderr, "[scheduler] pool mi-containers=%d (source: %s), %d specs (%d×0, %d×1, %d×2+)\n",
			cfg.containerCount, cfg.containerCountSource, total, demand0, demand1, demandN)
	} else {
		fmt.Fprintf(os.Stderr, "[scheduler] pooled identities disabled (%s!=true), skipping mi-containers pool demands; %d specs (%d×0, %d×1, %d×2+)\n",
			framework.UsePooledIdentitiesEnvvar, total, demand0, demand1, demandN)
	}
}

func ensureMISchedulerConfigured() {
	miSchedulerConfigure.Do(func() {
		configureMIScheduler(miSchedulerSpecs, miSchedulerSetup)
	})
}

func wrapMISchedulerPreRun(cmd *cobra.Command) {
	existingPreRun := cmd.PersistentPreRun
	cmd.PersistentPreRun = func(c *cobra.Command, args []string) {
		ensureMISchedulerConfigured()
		if existingPreRun != nil {
			existingPreRun(c, args)
		}
	}
}

// isRunSuiteProcess returns true when this is the long-lived parent run-suite
// process (os.Args[1] == "run-suite"), not a per-spec run-test worker subprocess.
// The openshift-tests-extension framework spawns each spec as a separate
// "run-test" OS process; only the parent process may start the UpgradeCoordinator.
func isRunSuiteProcess() bool {
	return len(os.Args) > 1 && os.Args[1] == "run-suite"
}

// isUpgradeInPlaceSuiteInvocation returns true when the current invocation is
// specifically for the upgrade/in-place suite. It scans the command-line
// arguments because the suite name is passed as a positional argument:
//
//	./aro-hcp-tests run-suite upgrade/in-place [flags...]
func isUpgradeInPlaceSuiteInvocation() bool {
	if !isRunSuiteProcess() {
		return false
	}
	for _, arg := range os.Args {
		if arg == "upgrade/in-place" {
			return true
		}
	}
	return false
}

func setupCli() *cobra.Command {
	// Configure Ginkgo to be verbose - when we're emitting a full object to stdout on failure, there's no real value in truncating its
	// content at some arbitrary length.
	format.MaxLength = 0
	format.MaxDepth = 0

	parallelismOverride := parseSuiteParallelismOverride()
	parallelism := func(defaultValue int) int {
		if parallelismOverride != nil {
			return *parallelismOverride
		}
		return defaultValue
	}

	containerCount, containerCountSource := parseMIContainerCount()
	pooledIdentitiesRaw := strings.TrimSpace(os.Getenv(framework.UsePooledIdentitiesEnvvar))
	pooledIdentitiesEnabled, err := strconv.ParseBool(pooledIdentitiesRaw)
	if err != nil && pooledIdentitiesRaw != "" {
		fmt.Fprintf(os.Stderr, "WARNING: %s=%q is not a valid boolean, treating as false\n", framework.UsePooledIdentitiesEnvvar, pooledIdentitiesRaw)
	}
	var miPools map[string]int
	if pooledIdentitiesEnabled {
		miPools = map[string]int{"mi-containers": containerCount}
	}

	// Extension registry
	registry := e.NewRegistry()

	// You can declare multiple extensions, but most people will probably only need to create one.
	ext := e.NewExtension("aro-hcp", "payload", "cuj-e2e-tests")

	// Build extension specs once, upfront. This reads the Ginkgo spec tree that was
	// populated at import time, so it is safe to call before adding suites.
	// We use the full spec list to count UpgradeInPlace specs dynamically so that
	// the suite Parallelism and the barrier total are always in sync with the real
	// spec count — no constant or env var needs updating when specs are added.
	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	upgradeInPlaceCount := 0
	for _, spec := range specs {
		if spec.Labels.Has(labels.UpgradeInPlace[0]) {
			upgradeInPlaceCount++
		}
	}
	// Store the count so NewUpgradeBarrier can read it at spec-run time.
	framework.SetUpgradeInPlaceSpecCount(upgradeInPlaceCount)

	// Remember that the label constants are (currently) slices, not items.

	// The tests that a suite is composed of can be filtered by CEL expressions. By
	// default, the qualifiers only apply to tests from this extension.
	integrationQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.DevelopmentOnly[0], labels.StageAndProdOnly[0], labels.HypershiftPresubmit[0])
	integrationTestTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name: "integration/parallel",
		Qualifiers: []string{
			fastTestsOnly(integrationQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Override parallelism at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(24),
		TestTimeout:   &integrationTestTimeout,
		ResourcePools: miPools,
	})

	ext.AddSuite(e.Suite{
		Name: "integration/parallel/slow",
		Qualifiers: []string{
			slowTestsOnly(integrationQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Override parallelism at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(24),
		TestTimeout:   &integrationTestTimeout,
		ResourcePools: miPools,
	})

	stageQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.IntegrationOnly[0], labels.DevelopmentOnly[0], labels.HypershiftPresubmit[0])
	stageTestTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name: "stage/parallel",
		Qualifiers: []string{
			fastTestsOnly(stageQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Override parallelism at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(34),
		TestTimeout:   &stageTestTimeout,
		ResourcePools: miPools,
	})
	ext.AddSuite(e.Suite{
		Name: "stage/parallel/slow",
		Qualifiers: []string{
			slowTestsOnly(stageQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Override parallelism at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(34),
		TestTimeout:   &stageTestTimeout,
		ResourcePools: miPools,
	})

	prodQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.IntegrationOnly[0], labels.DevelopmentOnly[0], labels.HypershiftPresubmit[0])
	prodTestTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name: "prod/parallel",
		Qualifiers: []string{
			fastTestsOnly(prodQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Override parallelism at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(19),
		TestTimeout:   &prodTestTimeout,
		ResourcePools: miPools,
	})
	ext.AddSuite(e.Suite{
		Name: "prod/parallel/slow",
		Qualifiers: []string{
			slowTestsOnly(prodQuery),
		},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Override parallelism at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(19),
		TestTimeout:   &prodTestTimeout,
		ResourcePools: miPools,
	})

	ext.AddSuite(e.Suite{
		Name: "dev-cd-check/parallel",
		Qualifiers: []string{
			// Subset of E2E tests to be executed as a final step during ARO
			// HCP Continous Deployment GitHub Action Workflow.
			// TODO: revisit labels to tweak which tests to select here
			fmt.Sprintf(`labels.exists(l, l=="%s" ) && labels.exists(l, l=="%s")`, labels.AroRpApiCompatible[0], labels.Positive[0]),
		},
		// Override at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(20),
		ResourcePools: miPools,
	})

	rpApiCompatBaseQualifier := fmt.Sprintf(`labels.exists(l, l=="%s")`, labels.AroRpApiCompatible[0])

	if framework.IsDevelopmentEnvironment() {
		rpApiCompatBaseQualifier = fmt.Sprintf(`%s || labels.exists(l, l=="%s")`, rpApiCompatBaseQualifier, labels.DevelopmentOnly[0])
	} else {
		rpApiCompatBaseQualifier = fmt.Sprintf(`%s && !labels.exists(l, l=="%s")`, rpApiCompatBaseQualifier, labels.DevelopmentOnly[0])
	}

	rpApiCompatTestTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name:       "rp-api-compat-all/parallel",
		Qualifiers: []string{fastTestsOnly(rpApiCompatBaseQualifier)},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Override parallelism at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(24),
		TestTimeout:   &rpApiCompatTestTimeout,
		ResourcePools: miPools,
	})
	ext.AddSuite(e.Suite{
		Name:       "rp-api-compat-all/parallel/slow",
		Qualifiers: []string{slowTestsOnly(rpApiCompatBaseQualifier)},
		// The resource-aware scheduler caps concurrent MI container usage via ResourcePools.
		// Override parallelism at runtime via ARO_HCP_SUITE_PARALLELISM.
		Parallelism:   parallelism(24),
		TestTimeout:   &rpApiCompatTestTimeout,
		ResourcePools: miPools,
	})

	hypershiftPresubmitQuery := fmt.Sprintf(`labels.exists(l, l=="%s")`, labels.HypershiftPresubmit[0])
	hypershiftPresubmitTimeout := 150 * time.Minute
	ext.AddSuite(e.Suite{
		Name:          "hypershift-presubmit/parallel",
		Qualifiers:    []string{hypershiftPresubmitQuery},
		Parallelism:   parallelism(24),
		TestTimeout:   &hypershiftPresubmitTimeout,
		ResourcePools: miPools,
	})

	// upgrade/in-place runs UpgradeInPlace specs in parallel. Each spec provisions
	// its own cluster+nodepool and captures a baseline, then all specs synchronise
	// at an UpgradeBarrier while the UpgradeCoordinator (parent run-suite process)
	// runs the Region entrypoint pipeline once for the suite. After the upgrade every
	// spec validates its own cluster independently (hash, haproxy image, DataSecretName).
	//
	// Parallelism equals the number of UpgradeInPlace specs counted above so every
	// spec can provision concurrently. If parallelism < spec count, specs block
	// forever at the barrier waiting for a queued spec that can never start —
	// a guaranteed deadlock. upgradeInPlaceCount is computed dynamically so
	// adding a new UpgradeInPlace spec automatically updates both the parallelism
	// and the barrier total without any manual constant to maintain.
	upgradeInPlaceTimeout := 120 * time.Minute
	ext.AddSuite(e.Suite{
		Name: "upgrade/in-place",
		Qualifiers: []string{
			fmt.Sprintf(`labels.exists(l, l=="%s")`, labels.UpgradeInPlace[0]),
		},
		Parallelism: parallelism(upgradeInPlaceCount),
		TestTimeout: &upgradeInPlaceTimeout,
	})

	// If using Ginkgo, specs were already built above. Hooks can be added here.

	// For the upgrade/in-place suite, register a BeforeAll that starts the
	// UpgradeCoordinator in the long-lived parent run-suite process. The
	// coordinator polls the barrier state file, waits for all specs to check in,
	// runs the Region entrypoint pipeline, then signals UpgradeDone so specs can
	// unblock.
	//
	// The hook is guarded by isUpgradeInPlaceSuiteInvocation() so it is a no-op
	// when any other suite runs. AddBeforeAll re-executes in every worker
	// subprocess spawned by openshift-tests-extension (an unintended upstream
	// behaviour), but the guard prevents duplicate coordinator goroutines.
	specs.AddBeforeAll(func() {
		if !isUpgradeInPlaceSuiteInvocation() {
			return
		}
		// Set a stderr-backed logger for the coordinator before constructing it.
		framework.SetUpgradeCoordinatorLogger(
			stdr.New(log.New(os.Stderr, "[upgrade-coordinator] ", log.LstdFlags)),
		)
		coord, err := framework.NewUpgradeCoordinator()
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to create upgrade coordinator: %v\n", err)
			return
		}
		go func() {
			if err := coord.Run(context.Background()); err != nil {
				fmt.Fprintf(os.Stderr, "upgrade coordinator: %v\n", err)
			}
		}()
	})

	// You can add hooks to run before/after tests. There are BeforeEach, BeforeAll, AfterEach,
	// and AfterAll. "Each" functions must be thread safe.
	//
	// specs.AddBeforeAll(func() {
	// })
	//
	// specs.AddBeforeEach(func(spec ExtensionTestSpec) {
	//	if spec.Name == "my test" {
	//		// do stuff
	//	}
	// })
	//
	// specs.AddAfterEach(func(res *ExtensionTestResult) {
	// 	if res.Result == ResultFailed && apiTimeoutRegexp.Matches(res.Output) {
	// 		res.AddDetails("api-timeout", collectDiagnosticInfo())
	// 	}
	// })

	// You can also manually build a test specs list from other testing tooling
	// TODO: example

	// Modify specs, such as adding a label to all specs
	// 	specs = specs.AddLabel("SLOW")

	// Specs can be globally filtered...
	// specs = specs.MustFilter([]string{`name.contains("filter")`})

	// Or walked...
	// specs = specs.Walk(func(spec *extensiontests.ExtensionTestSpec) {
	//	if strings.Contains(e.Name, "scale up") {
	//		e.Labels.Insert("SLOW")
	//	}
	//
	// Specs can also be selected...
	// specs = specs.Select(et.NameContains("slow test")).AddLabel("SLOW")
	//
	// Or with "any" (or) matching selections
	// specs = specs.SelectAny(et.NameContains("slow test"), et.HasLabel("SLOW"))
	//
	// Or with "all" (and) matching selections
	// specs = specs.SelectAll(et.NameContains("slow test"), et.HasTagWithValue("speed", "slow"))
	//
	// There are also Must* functions for any of the above flavors of selection
	// which will return an error if nothing is found
	// specs, err = specs.MustSelect(et.NameContains("slow test")).AddLabel("SLOW")
	// if err != nil {
	//    logrus.Warn("no specs found: %w", err)
	// }
	// Test renames
	//	if spec.Name == "[sig-testing] openshift-tests-extension has a test with a typo" {
	//		spec.OriginalName = `[sig-testing] openshift-tests-extension has a test with a tpyo`
	//	}
	//
	// Filter by environment flags
	// if spec.Name == "[sig-testing] openshift-tests-extension should support defining the platform for tests" {
	//		spec.Include(et.PlatformEquals("aws"))
	//		spec.Exclude(et.And(et.NetworkEquals("ovn"), et.TopologyEquals("ha")))
	//	}
	// })

	registerEV2RetryCatcher(specs)

	ext.AddSpecs(specs)
	registry.Register(ext)

	// AddSpecs copies specs into a new backing array (ext.specs), so the
	// scheduler must be wired against ext.GetSpecs() rather than the local
	// specs slice above — otherwise configureMIScheduler mutates a slice
	// run-suite/run-test never reads.
	initMIScheduler(ext.GetSpecs(), miSchedulerConfig{
		pooledIdentitiesEnabled: pooledIdentitiesEnabled,
		containerCount:          containerCount,
		containerCountSource:    containerCountSource,
	})

	root := &cobra.Command{
		Long: "ARO-HCP E2E Tests",
	}

	extensionCmds := cmd.DefaultExtensionCommands(registry)
	for _, c := range extensionCmds {
		switch c.Name() {
		case "run-suite", "run-test":
			wrapMISchedulerPreRun(c)
		}
	}
	root.AddCommand(extensionCmds...)
	root.AddCommand(cleanup.NewCommand())
	root.AddCommand(metadataapi.Must(mergegate.NewCommand()))
	root.AddCommand(metadataapi.Must(visualize.NewCommand()))
	root.AddCommand(metadataapi.Must(customlinktools.NewCommand()))
	root.AddCommand(metadataapi.Must(gatherobservability.NewCommand()))
	root.AddCommand(metadataapi.Must(gathersnapshot.NewCommand()))
	root.AddCommand(metadataapi.Must(slotmanager.NewCommand()))
	return root
}

func main() {
	root := setupCli()
	if err := func() error {
		return root.Execute()
	}(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
