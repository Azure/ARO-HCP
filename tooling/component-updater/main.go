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

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/component-updater/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rootCmd := &cobra.Command{
		Use:   "component-updater",
		Short: "Discovers compatible component versions and updates local ARO-HCP configuration",
		Long: `component-updater is a shared tool for configured ARO-HCP component version
bumps (providers registered per component; Istio mesh revision is wired today).
It proposes at most one safe increment per selected component. Check is
read-only. Update edits local configuration and, by default, runs make
materialize to regenerate rendered configs and Helm fixtures (use
--skip-materialize to edit source only). Neither command deploys resources.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	rootCmd.AddCommand(cmd.NewCheckCommand())
	rootCmd.AddCommand(cmd.NewUpdateCommand())

	rootCmd.SetContext(ctx)
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
