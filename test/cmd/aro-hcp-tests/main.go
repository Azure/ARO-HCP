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
	"fmt"
	"os"

	// If using ginkgo, import your tests here
	_ "github.com/Azure/ARO-HCP/test/e2e"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/cleanup"
	customlinktools "github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/custom-link-tools"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/visualize"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

func main() {
	// Extension registry
	registry := e.NewRegistry()

	// You can declare multiple extensions, but most people will probably only need to create one.
	ext := e.NewExtension("aro-hcp", "payload", "cuj-e2e-tests")

	// The tests that a suite is composed of can be filtered by CEL expressions. By
	// default, the qualifiers only apply to tests from this extension.
	ext.AddSuite(e.Suite{
		Name: "integration/parallel",
		Qualifiers: []string{
			// Remember that the label constants are (currently) slices, not items.
			// TODO we will need per-env markers eventually, but it's ok to start here
			fmt.Sprintf(`labels.exists(l, l=="%s")`, labels.RequireNothing[0]),
		},
		Parallelism: 20,
	})

	ext.AddSuite(e.Suite{
		Name: "stage/parallel",
		Qualifiers: []string{
			// Remember that the label constants are (currently) slices, not items.
			// TODO we will need per-env markers eventually, but it's ok to start here
			fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.IntegrationOnly[0]),
		},
		Parallelism: 10,
	})

	ext.AddSuite(e.Suite{
		Name: "prod/parallel",
		Qualifiers: []string{
			// Remember that the label constants are (currently) slices, not items.
			// TODO we will need per-env markers eventually, but it's ok to start here
			fmt.Sprintf(`labels.exists(l, l=="%s") && !labels.exists(l, l=="%s")`, labels.RequireNothing[0], labels.IntegrationOnly[0]),
		},
		Parallelism: 10,
	})

	ext.AddSuite(e.Suite{
		Name: "dev-cd-check/parallel",
		Qualifiers: []string{
			// Subset of E2E tests to be executed as a final step during ARO
			// HCP Continous Deployment GitHub Action Workflow.
			// TODO: revisit labels to tweak which tests to select here
			fmt.Sprintf(`labels.exists(l, l=="%s" ) && labels.exists(l, l=="%s")`, labels.AroRpApiCompatible[0], labels.Positive[0]),
		},
		Parallelism: 20,
	})

	ext.AddSuite(e.Suite{
		Name: "rp-api-compat-all/parallel",
		Qualifiers: []string{
			// This suite contains all E2E tests which don't use ARM APIs to
			// communicate with ARO HCP RP (so that it's possible to run
			// them against ARO HCP dev instance via RP API endpoint).
			fmt.Sprintf(`labels.exists(l, l=="%s")`, labels.AroRpApiCompatible[0]),
		},
		Parallelism: 20,
	})

	// If using Ginkgo, build test specs automatically
	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

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

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "ARO-HCP E2E Tests",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)
	root.AddCommand(cleanup.NewCommand())
	root.AddCommand(api.Must(visualize.NewCommand()))
	root.AddCommand(api.Must(customlinktools.NewCommand()))

	if err := func() error {
		return root.Execute()
	}(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
