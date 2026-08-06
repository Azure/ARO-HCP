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
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	// Import the e2e package to register Ginkgo specs via var _ = Describe(...).
	_ "github.com/Azure/ARO-HCP/test/e2e"

	"github.com/go-logr/logr"
	"github.com/onsi/gomega/format"
	"github.com/spf13/cobra"

	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-qe-cluster/deploy"
	"github.com/Azure/ARO-HCP/test/pkg/logger"
)

// Initialize ginkgo extension registry in the same way as the test runner
// does it, but without parallelism, test suites and other functionality not
// needed here.
// TODO: consider unification of the core part of ginkgo initialization to
// avoid duplication here
func buildRegistry() (*e.Registry, et.ExtensionTestSpecs, error) {
	format.MaxLength = 0
	format.MaxDepth = 0

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		return nil, nil, fmt.Errorf("building extension specs from ginkgo suite: %w", err)
	}

	ext := e.NewExtension("aro-hcp", "payload", "cuj-e2e-tests")
	ext.AddSpecs(specs)

	registry := e.NewRegistry()
	registry.Register(ext)
	return registry, specs, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	registry, specs, err := buildRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var verbosity int

	root := &cobra.Command{
		Use:              "aro-hcp-qe-cluster",
		Short:            "ARO HCP QE Cluster Deployment Tool",
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx = logr.NewContext(ctx, logger.NewWithVerbosity(verbosity))
			cmd.SetContext(ctx)
		},
	}

	root.PersistentFlags().IntVarP(&verbosity, "verbosity", "v", 0, "log verbosity level")

	root.AddCommand(deploy.NewCommand(registry, specs))

	// Start the cobra based cli, with ability to stop the run via SIGTERM
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
