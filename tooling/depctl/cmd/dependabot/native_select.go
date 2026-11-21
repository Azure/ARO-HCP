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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/dependabot"
)

type nativeSelectRawOptions struct {
	repository, inventory, group, target string
}

type nativeSelectValidatedOptions struct {
	raw nativeSelectRawOptions
}

type nativeSelectOptions struct {
	repository, group, target string
	inventory                 io.ReadCloser
	output                    io.Writer
}

// Builds the offline inventory selector with validated command inputs.
func newNativeSelectCommand() *cobra.Command {
	var options nativeSelectRawOptions
	command := &cobra.Command{
		Use:   "select-group",
		Short: "Select one eligible native family and exact target without network access",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			validated, err := options.Validate()
			if err != nil {
				return err
			}
			completed, err := validated.Complete(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			return completed.Run()
		},
	}
	command.Flags().StringVar(&options.repository, "repository", "", "OWNER/REPO")
	command.Flags().StringVar(&options.inventory, "inventory", "", "native inventory JSON file")
	command.Flags().StringVar(&options.group, "group", "", "24-character native family group ID")
	command.Flags().StringVar(&options.target, "target", "", "exact inventory target")
	return command
}

// Validate checks selection arguments before opening the inventory.
func (o nativeSelectRawOptions) Validate() (nativeSelectValidatedOptions, error) {
	if o.repository == "" || o.inventory == "" || o.group == "" || o.target == "" {
		return nativeSelectValidatedOptions{}, cli.Invalid(errors.New("--repository, --inventory, --group and --target are required"))
	}
	if !nativeRequestRepositoryPattern.MatchString(o.repository) {
		return nativeSelectValidatedOptions{}, cli.Invalid(errors.New("repository must be OWNER/REPO"))
	}
	if !nativeRequestGroupPattern.MatchString(o.group) {
		return nativeSelectValidatedOptions{}, cli.Invalid(errors.New("invalid inventory group or target"))
	}
	return nativeSelectValidatedOptions{raw: o}, nil
}

// Complete opens the read-only inventory and binds the selection output.
func (o nativeSelectValidatedOptions) Complete(output io.Writer) (nativeSelectOptions, error) {
	inventory, err := os.Open(o.raw.inventory)
	if err != nil {
		return nativeSelectOptions{}, cli.Invalid(fmt.Errorf("open native inventory: %w", err))
	}
	return nativeSelectOptions{
		repository: o.raw.repository, group: o.raw.group, target: o.raw.target,
		inventory: inventory, output: output,
	}, nil
}

// Run selects one exact inventory group without network access or file writes.
func (o nativeSelectOptions) Run() error {
	defer o.inventory.Close()
	selection, err := dependabot.SelectNativeInventoryGroup(o.inventory, o.repository, o.group, o.target)
	if err != nil {
		return cli.Invalid(fmt.Errorf("select native inventory group: %w", err))
	}
	if err := json.NewEncoder(o.output).Encode(selection); err != nil {
		return fmt.Errorf("encode native selection: %w", err)
	}
	return nil
}
