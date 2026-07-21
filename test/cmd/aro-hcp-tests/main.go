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
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	// If using ginkgo, import your tests here
	_ "github.com/Azure/ARO-HCP/test/e2e"

	"github.com/go-logr/stdr"
	"github.com/onsi/gomega/format"
	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/cleanup"
	customlinktools "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/custom-link-tools"
	gatherobservability "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/gather-observability"
	gathersnapshot "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/gather-snapshot"
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

const DefaultMIContainerCount = 15

func parseMIContainersLabel(spec *et.ExtensionTestSpec) (int, bool) {
	for label := range spec.Labels {
		if v, ok := strings.CutPrefix(label, "MIContainers:"); ok {
			n, err := strconv.Atoi(v)
			if err == nil {
				if n < 0 {
					fmt.Fprintf(os.Stderr, "FATAL: test %q has MIContainers:%d but N must be >= 0\n", spec.Name, n)
					os.Exit(1)
				}
				return n, true
			}
		}
	}
	return 0, false
}

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

func miDemandPriority(spec *et.ExtensionTestSpec) int {
	demand, _ := parseMIContainersLabel(spec)
	return demand
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
	miPools := map[string]int{"mi-containers": containerCount}

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
	integrationQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.DevelopmentOnly[0], labels.StageAndProdOnly[0])
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

	stageQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.IntegrationOnly[0], labels.DevelopmentOnly[0])
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

	prodQuery := fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.IntegrationOnly[0], labels.DevelopmentOnly[0])
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
		if demand > 0 {
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
	fmt.Fprintf(os.Stderr, "[scheduler] pool mi-containers=%d (source: %s), %d specs (%d×0, %d×1, %d×2+)\n",
		containerCount, containerCountSource, total, demand0, demand1, demandN)

	if os.Getenv("ARO_HCP_DISABLE_MI_SORT") != "true" {
		sort.SliceStable(specs, func(i, j int) bool {
			return miDemandPriority(specs[i]) > miDemandPriority(specs[j])
		})
	}

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "ARO-HCP E2E Tests",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)
	root.AddCommand(cleanup.NewCommand())
	root.AddCommand(api.Must(visualize.NewCommand()))
	root.AddCommand(api.Must(customlinktools.NewCommand()))
	root.AddCommand(api.Must(gatherobservability.NewCommand()))
	root.AddCommand(api.Must(gathersnapshot.NewCommand()))
	root.AddCommand(api.Must(slotmanager.NewCommand()))
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
