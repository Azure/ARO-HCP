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

// Package toolchain implements read-only Go toolchain readiness commands.
package toolchain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/depctl/internal/cli"
	"github.com/Azure/ARO-HCP/tooling/depctl/internal/toolchain"
)

// RawOptions holds the plan command flags.
type RawOptions struct {
	root, repo string
}

type validatedOptions struct {
	root, repo string
}

type completedOptions struct {
	validatedOptions
	client *http.Client
	output io.Writer
}

// NewCommand builds the toolchain command and its readiness plan subcommand.
func NewCommand(group string) (*cobra.Command, error) {
	command := &cobra.Command{
		Use: "toolchain", Short: "Check Go toolchain upgrade readiness", GroupID: group,
		Args: cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	raw := RawOptions{}
	plan := &cobra.Command{
		Use:   "plan",
		Short: "Emit read-only Go toolchain readiness evidence as JSON",
		Args:  cli.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			validated, err := raw.Validate()
			if err != nil {
				return cli.Invalid(err)
			}
			completed, err := validated.Complete(cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("complete toolchain plan options: %w", err)
			}
			return completed.Run(cmd.Context())
		},
	}
	plan.Flags().StringVar(&raw.root, "root", ".", "repository checkout root")
	plan.Flags().StringVar(&raw.repo, "repo", toolchain.Repository, "GitHub repository")
	command.AddCommand(plan)
	RegisterPreparation(command)
	return command, nil
}

// Validate checks local input syntax before any I/O.
func (r RawOptions) Validate() (validatedOptions, error) {
	if strings.TrimSpace(r.root) == "" || strings.ContainsAny(r.root, "\x00\r\n") {
		return validatedOptions{}, errors.New("--root must be a nonempty checkout path on one line")
	}
	if r.repo != toolchain.Repository {
		return validatedOptions{}, fmt.Errorf("--repo must be %s", toolchain.Repository)
	}
	return validatedOptions(r), nil
}

// Complete resolves the checkout and binds the release client and JSON stream.
func (v validatedOptions) Complete(output io.Writer) (completedOptions, error) {
	root, err := filepath.Abs(v.root)
	if err != nil {
		return completedOptions{}, fmt.Errorf("resolve checkout root: %w", err)
	}
	v.root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return completedOptions{}, fmt.Errorf("resolve checkout symlinks: %w", err)
	}
	return completedOptions{validatedOptions: v, client: &http.Client{Timeout: time.Minute}, output: output}, nil
}

// Run emits the plan without creating an artifact in the checkout.
func (o completedOptions) Run(ctx context.Context) error {
	plan, err := toolchain.MakePlan(ctx, o.root, o.repo, o.client)
	if err != nil {
		return fmt.Errorf("plan Go toolchain upgrade: %w", err)
	}
	if err := cli.WriteJSON(o.output, plan); err != nil {
		return fmt.Errorf("emit toolchain plan: %w", err)
	}
	return nil
}
