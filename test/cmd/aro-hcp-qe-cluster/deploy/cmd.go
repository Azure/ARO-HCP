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

package deploy

import (
	"fmt"
	"os"
	"os/signal"
	"slices"

	"github.com/spf13/cobra"

	extcmd "github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
)

func NewCommand(registry *e.Registry, specs et.ExtensionTestSpecs) *cobra.Command {
	var kubeconfigPath string

	cmd := &cobra.Command{
		Use:   "deploy TEST_NAME",
		Short: "Deploy ARO HCP cluster by running given E2E test without teardown",
		Args:  cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer cancel()

			testName := args[0]
			if !slices.Contains(specs.Names(), testName) {
				return fmt.Errorf("test case %q not found", testName)
			}

			if err := os.Setenv("ARO_E2E_SKIP_CLEANUP", "true"); err != nil {
				return fmt.Errorf("setting ARO_E2E_SKIP_CLEANUP: %w", err)
			}

			if kubeconfigPath != "" {
				// store the given file path so that we can save the admin
				// kubeconfig for the deployed cluster to the given file later
				panic("implement kubeconfig fetching")
			}

			runner := &cobra.Command{SilenceUsage: true, SilenceErrors: true}
			for _, c := range extcmd.DefaultExtensionCommands(registry) {
				runner.AddCommand(c)
			}
			runner.SetArgs([]string{"run-test", testName})
			return runner.ExecuteContext(ctx)
		},
	}

	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "", "file to write the deployed cluster's admin kubeconfig to")
	return cmd
}
