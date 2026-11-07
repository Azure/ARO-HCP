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

package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
)

// osReadFile is a thin seam used by record readers.
var osReadFile = os.ReadFile

// Builds the `depctl plan` command.
func newPlanCommand() *cobra.Command {
	var (
		input   string
		output  string
		profile string
		targets = stringMapFlag{}
	)
	command := &cobra.Command{
		Use:     "plan",
		Short:   "Pin exact targets, base, scope and required consumers",
		GroupID: groupWorkflow,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var inventory model.Inventory
			if err := readRecord(input, model.KindInventory, &inventory); err != nil {
				return invalid(err)
			}
			if profile != inventory.Profile {
				return invalid(errProfileMismatch(profile, inventory.Profile))
			}
			plan, err := orchestration.New(orchestration.Deps{}).Plan(inventory, targets)
			if err != nil {
				return invalid(err)
			}
			if err := orchestration.WriteRecord(output, plan); err != nil {
				return err
			}
			switch plan.Outcome {
			case model.OutcomeBlocked:
				return incomplete("plan is blocked: %v", plan.Blockers)
			case model.OutcomeNoChange:
				cmd.Println("no change: no requested target differs from the pinned version")
			}
			return nil
		},
	}
	command.Flags().StringVar(&input, "input", "", "inventory record")
	command.Flags().StringVar(&output, "output", "", "write the versioned plan record here")
	command.Flags().StringVar(&profile, "profile", "tools", "policy profile")
	command.Flags().Var(targets, "target", "explicit FAMILY=VERSION target (repeatable)")
	return command
}

// Reports a requested profile that differs from the inventory profile.
func errProfileMismatch(requested, actual string) error {
	return &profileMismatch{requested: requested, actual: actual}
}

type profileMismatch struct {
	requested string
	actual    string
}

// Error reports the profile mismatch.
func (p *profileMismatch) Error() string {
	return "requested profile " + p.requested + " does not match the inventory profile " + p.actual
}
