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

// Package preview deploys the Grafana dashboards that a pull request changes to
// a folder of its own, so that a reviewer can see them before they merge.
package preview

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

const previewGroupID = "previews"

// NewPreviewCommand builds the preview command and its subcommands.
func NewPreviewCommand(group string) (*cobra.Command, error) {
	previewCmd := &cobra.Command{
		Use:     "preview",
		Short:   "Manage pull request dashboard previews",
		Long:    "Deploy the Grafana dashboards a pull request changes to a preview folder of its own.",
		GroupID: group,
	}

	previewCmd.AddGroup(&cobra.Group{
		ID:    previewGroupID,
		Title: "Preview Commands:",
	})

	createOpts := BuildDefaultCreateOptions()
	createCmd := &cobra.Command{
		Use:     "create",
		Short:   "Create or update the preview of a pull request",
		Long:    "Upload the dashboards a pull request changes into a folder of its own, and report the links in the job log and in a comment on the pull request.",
		GroupID: previewGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return createOpts.RunCreate(cmd.Context())
		},
	}
	if err := BindCreateOptions(createOpts, createCmd); err != nil {
		return nil, err
	}

	previewCmd.AddCommand(createCmd)

	return previewCmd, nil
}

// RunCreate validates and completes the options, then creates the preview.
func (o *RawCreateOptions) RunCreate(ctx context.Context) error {
	validated, err := o.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	return completed.Run(ctx)
}
