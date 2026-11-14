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

package dependabot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	dependabotpolicy "github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/forge/ghapi"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/policy"
)

// readerFactory builds the read-only proposal reader.
var readerFactory = func(repository string) (forge.Reader, error) {
	return ghapi.New(repository)
}

// InventoryOptions are the unvalidated inventory flags.
type InventoryOptions struct {
	Repository string
	Output     string

	out io.Writer
	err io.Writer
}

// DefaultInventoryOptions returns the raw inventory options.
func DefaultInventoryOptions() *InventoryOptions {
	return &InventoryOptions{}
}

// BindInventoryOptions registers the inventory flags.
func BindInventoryOptions(options *InventoryOptions, command *cobra.Command) error {
	command.Flags().StringVar(&options.Repository, "repository", options.Repository, "OWNER/REPO")
	command.Flags().StringVar(&options.Output, "output", options.Output,
		"write the inventory array here instead of stdout")
	return nil
}

// ValidatedInventory are the inventory options that passed validation.
type ValidatedInventory struct {
	*InventoryOptions
}

// Validate checks the inventory inputs.
func (o *InventoryOptions) Validate(_ context.Context) (*ValidatedInventory, error) {
	if o.Repository == "" {
		return nil, cli.Invalid(errors.New("--repository OWNER/REPO is required"))
	}
	return &ValidatedInventory{InventoryOptions: o}, nil
}

// CompletedInventory are the inventory options with their reader.
type CompletedInventory struct {
	*ValidatedInventory
	reader forge.Reader
}

// Complete builds the read-only proposal reader.
func (v *ValidatedInventory) Complete(_ context.Context) (*CompletedInventory, error) {
	reader, err := readerFactory(v.Repository)
	if err != nil {
		if errors.Is(err, forge.ErrAuthentication) {
			return nil, cli.Incomplete("read owned proposals: %v", err)
		}
		return nil, cli.Invalid(err)
	}
	return &CompletedInventory{ValidatedInventory: v, reader: reader}, nil
}

// Run lists the owned security proposals and emits the inventory array.
func (c *CompletedInventory) Run(ctx context.Context) error {
	pulls, err := c.reader.OpenPullRequests(ctx, policy.Robot)
	if err != nil {
		return cli.MapEngineError(err)
	}
	records, err := dependabotpolicy.OwnedSecurityProposals(pulls, c.Repository)
	if err != nil {
		return cli.Incomplete("%v", err)
	}
	encoded, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return cli.Internal(err)
	}
	encoded = append(encoded, '\n')
	if c.Output == "" {
		_, err := c.out.Write(encoded)
		return err
	}
	if err := os.WriteFile(c.Output, encoded, 0o600); err != nil {
		return cli.Internal(fmt.Errorf("write inventory: %w", err))
	}
	fmt.Fprintf(c.err, "found %d owned security proposals\n", len(records))
	return nil
}
