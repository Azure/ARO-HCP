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
	"strconv"
	"strings"

	_ "github.com/Azure/ARO-HCP/test/e2e"

	"github.com/go-logr/stdr"
	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	// If using ginkgo, import your tests here
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/cleanup"
	customlinktools "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/custom-link-tools"
	gatherobservability "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/gather-observability"
	gathersnapshot "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/gather-snapshot"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/infra"
	mergegate "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/merge-gate"
	slotmanager "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/visualize"
	"github.com/Azure/ARO-HCP/test/pkg/leaseenv"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

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

// Build the CLI with registration-time concurrency defaults and lazy extension specs.
func setupCli() (*cobra.Command, error) {
	containerCount, containerCountSource := parseMIContainerCount()
	acquired := 0
	if containerCountSource == leaseenv.MSIContainers {
		acquired = containerCount
	}
	workers, err := parseSuiteConcurrency(acquired)
	if err != nil {
		return nil, err
	}

	// Extension registry
	registry := e.NewRegistry()

	initializeSuites := func(*cobra.Command, []string) error {
		configureGomegaFormatting()
		pooledIdentitiesRaw := strings.TrimSpace(os.Getenv(framework.UsePooledIdentitiesEnvvar))
		pooledIdentitiesEnabled, err := strconv.ParseBool(pooledIdentitiesRaw)
		if err != nil && pooledIdentitiesRaw != "" {
			fmt.Fprintf(os.Stderr, "WARNING: %s=%q is not a valid boolean, treating as false\n", framework.UsePooledIdentitiesEnvvar, pooledIdentitiesRaw)
		}
		var miPools map[string]int
		if pooledIdentitiesEnabled {
			miPools = map[string]int{"mi-containers": containerCount}
		}

		// You can declare multiple extensions, but most people will probably only need to create one.
		ext := e.NewExtension("aro-hcp", "payload", "cuj-e2e-tests")

		// Build extension specs once when a registry-consuming command executes.
		// This reads the Ginkgo spec tree populated at import time before adding suites.
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

		registerSuites(ext, miPools, upgradeInPlaceCount, workers)

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
		// specs slice above — otherwise the demand-based sort in
		// configureMIScheduler reorders a slice run-suite/run-test never reads.
		initMIScheduler(ext.GetSpecs(), miSchedulerConfig{
			pooledIdentitiesEnabled: pooledIdentitiesEnabled,
			containerCount:          containerCount,
			containerCountSource:    containerCountSource,
		})
		return nil
	}
	configureScheduler := func(*cobra.Command, []string) error {
		ensureMISchedulerConfigured()
		return nil
	}

	root := &cobra.Command{
		Long: "ARO-HCP E2E Tests",
	}

	extensionCmds := cmd.DefaultExtensionCommands(registry)
	for _, c := range extensionCmds {
		hooks := commandHooks{}
		switch c.Name() {
		case "run-suite":
			hooks.BeforePersistent = []commandHook{initializeSuites}
			hooks.BeforePersistentLocal = []commandHook{configureScheduler}
			hooks.AfterPreRun = []commandHook{configureSuiteConcurrency(registry)}
		case "run-test":
			hooks.BeforePersistent = []commandHook{initializeSuites}
			hooks.BeforePersistentLocal = []commandHook{configureScheduler}
		default:
			hooks.BeforePersistent = []commandHook{initializeSuites}
		}
		wrapCommandHooks(c, hooks)
	}
	root.AddCommand(extensionCmds...)
	root.AddCommand(cleanup.NewCommand())
	root.AddCommand(metadataapi.Must(mergegate.NewCommand()))
	root.AddCommand(metadataapi.Must(visualize.NewCommand()))
	root.AddCommand(metadataapi.Must(customlinktools.NewCommand()))
	root.AddCommand(metadataapi.Must(gatherobservability.NewCommand()))
	root.AddCommand(metadataapi.Must(gathersnapshot.NewCommand()))
	root.AddCommand(metadataapi.Must(slotmanager.NewCommand()))
	root.AddCommand(infra.NewCommand())
	return root, nil
}
