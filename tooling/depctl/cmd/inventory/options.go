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

package inventory

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/consumers"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/discovery"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/orchestration"
)

// RawOptions are the unvalidated inventory flags.
type RawOptions struct {
	RepositoryPath string
	Profile        string
	Revision       string
	Offline        bool
	Output         string

	out io.Writer
	err io.Writer
}

// DefaultOptions returns the raw inventory options.
func DefaultOptions() *RawOptions {
	return &RawOptions{
		RepositoryPath: ".",
		Profile:        discovery.ProfileTools,
		Offline:        true,
	}
}

// BindOptions registers the inventory flags.
func BindOptions(options *RawOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.RepositoryPath, "repo", options.RepositoryPath, "repository working tree")
	command.Flags().StringVar(&options.Profile, "profile", options.Profile, "policy profile")
	command.Flags().StringVar(&options.Revision, "revision", options.Revision, "revision to inventory (defaults to HEAD)")
	command.Flags().BoolVar(&options.Offline, "offline", options.Offline, "restrict discovery to tracked pins")
	command.Flags().StringVar(&options.Output, "output", options.Output, "write the versioned inventory record here")
	return nil
}

// ValidatedOptions are the inventory options that passed validation.
type ValidatedOptions struct {
	*RawOptions
}

// Validate checks the inventory inputs.
func (o *RawOptions) Validate(_ context.Context) (*ValidatedOptions, error) {
	if o.Profile != discovery.ProfileTools {
		return nil, cli.Invalid(fmt.Errorf("unsupported profile %q", o.Profile))
	}
	if !o.Offline && o.RepositoryPath == "" {
		return nil, cli.Invalid(ErrOnlineRepositoryRequired)
	}
	return &ValidatedOptions{RawOptions: o}, nil
}

// CompletedOptions are the inventory options with their engine.
type CompletedOptions struct {
	*ValidatedOptions
	engine *orchestration.Engine
}

// Complete builds the engine the inventory command uses.
func (v *ValidatedOptions) Complete(_ context.Context) (*CompletedOptions, error) {
	deps := orchestration.Deps{Clock: time.Now, Registry: consumers.Default()}
	if !v.Offline {
		deps.Resolver = discovery.NewCLIResolver(v.RepositoryPath)
	}
	return &CompletedOptions{ValidatedOptions: v, engine: orchestration.New(deps)}, nil
}

// Run executes the inventory.
func (c *CompletedOptions) Run(ctx context.Context) error {
	record, err := c.engine.Inventory(ctx, c.RepositoryPath, c.Revision, c.Profile, c.Offline)
	if err != nil {
		return cli.Invalid(err)
	}
	if err := cli.EmitRecord(c.out, c.Output, record); err != nil {
		return err
	}
	if !record.Complete {
		return cli.Incomplete("inventory is incomplete: %v", record.Errors)
	}
	return nil
}
