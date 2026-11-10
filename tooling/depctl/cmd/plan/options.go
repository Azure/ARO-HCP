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

package plan

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/model"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
)

// RawOptions are the unvalidated plan flags.
type RawOptions struct {
	Input   string
	Output  string
	Profile string
	Targets cli.StringMapFlag

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw plan options.
func DefaultOptions() *RawOptions {
	return &RawOptions{Profile: "tools", Targets: cli.StringMapFlag{}}
}

// BindOptions registers the plan flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.Input, "input", options.Input, "inventory record")
	command.Flags().StringVar(&options.Output, "output", options.Output, "write the versioned plan record here")
	command.Flags().StringVar(&options.Profile, "profile", options.Profile, "policy profile")
	command.Flags().Var(options.Targets, "target", "explicit FAMILY=VERSION target (repeatable)")
	return nil
}

// ValidatedOptions are the plan options that passed validation.
type ValidatedOptions struct {
	*RawOptions
}

// Validate checks the plan inputs.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.Input == "" {
		return nil, cli.Invalid(errInventoryRequired)
	}
	if o.Profile == "" {
		return nil, cli.Invalid(errProfileRequired)
	}
	return &ValidatedOptions{RawOptions: o}, nil
}

// CompletedOptions are the plan options with the loaded inventory.
type CompletedOptions struct {
	*ValidatedOptions
	inventory model.Inventory
}

// Complete loads the inventory the plan is built from.
func (v *ValidatedOptions) Complete(_ context.Context) (*CompletedOptions, error) {
	var inventory model.Inventory
	if err := cli.ReadRecord(v.Input, model.KindInventory, &inventory); err != nil {
		return nil, cli.Invalid(err)
	}
	if v.Profile != inventory.Profile {
		return nil, cli.Invalid(fmt.Errorf(
			"requested profile %s does not match the inventory profile %s", v.Profile, inventory.Profile))
	}
	return &CompletedOptions{ValidatedOptions: v, inventory: inventory}, nil
}

// Run builds and emits the plan.
func (c *CompletedOptions) Run(ctx context.Context) error {
	_ = ctx
	record, err := orchestration.New(orchestration.Deps{}).Plan(c.inventory, c.Targets)
	if err != nil {
		return cli.Invalid(err)
	}
	if err := cli.EmitRecord(c.out, c.Output, record); err != nil {
		return err
	}
	return planOutcome(record, c.err)
}

// Reports blockers before accepting a successful outcome.
func planOutcome(record model.Plan, errOut io.Writer) error {
	if len(record.Blockers) > 0 {
		return cli.Incomplete("plan is blocked: %v", record.Blockers)
	}
	if record.Outcome == model.OutcomeNoChange {
		fmt.Fprintln(errOut, "no change: no requested target differs from the pinned version")
	}
	return nil
}

// Errors reported by the plan command.
var (
	errInventoryRequired = errors.New("--input is required")
	errProfileRequired   = errors.New("--profile is required")
)
