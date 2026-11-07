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

// Package cmd wires thin Cobra adapters over the workflow engine.
package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/pkg/depctl"
)

// root command group identifiers.
const (
	groupWorkflow = "workflow"
	groupEvidence = "evidence"
	groupHelper   = "helper"
)

// Execute runs the CLI and returns any error for main to exit with.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := &cobra.Command{
		Use:   "depctl",
		Short: "ARO-HCP dependency automation lifecycle",
		Long: `depctl runs the ARO-HCP dependency automation lifecycle locally.

Only commands whose guards exist are registered. Publication, hold recovery and
scheduling require an authorized execution integration and are not reachable
from this build.`,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}
	root.AddGroup(
		&cobra.Group{ID: groupWorkflow, Title: "Workflow commands:"},
		&cobra.Group{ID: groupEvidence, Title: "Evidence commands:"},
		&cobra.Group{ID: groupHelper, Title: "Helper commands:"},
	)
	commands := []*cobra.Command{
		newInventoryCommand(),
		newPlanCommand(),
		newApplyCommand(),
		newVerifyCommand(),
		newProposalCommand(),
		newRunCommand(),
		newMaintenanceCommand(),
		newEvidenceCommand(),
		newVersionCommand(),
	}
	for _, command := range commands {
		root.AddCommand(command)
	}
	return root.ExecuteContext(ctx)
}

// Wraps an input error with the documented exit code.
func invalid(err error) error {
	if err == nil {
		return nil
	}
	var exit *depctl.ExitError
	if errors.As(err, &exit) {
		return err
	}
	return depctl.Errorf(depctl.ExitInvalid, "%v", err)
}

// Wraps an error with the incomplete-evidence exit code.
func incomplete(format string, args ...any) error {
	return depctl.Errorf(depctl.ExitIncomplete, format, args...)
}
