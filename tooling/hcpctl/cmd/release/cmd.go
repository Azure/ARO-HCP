// Copyright 2025 Microsoft Corporation
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

package release

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/release"
)

func NewCommand(group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     "release",
		Short:   "Helm release operations and reporting",
		GroupID: group,
		Long: `release provides operations for release management and reporting.

This command group includes subcommands for generating image promotion reports
from Helm releases deployed on Kubernetes clusters.`,
		Example: `  hcpctl release status
  hcpctl release status --release backend --namespace aro-hcp`,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// Add status subcommand
	statusCmd, err := newStatusCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(statusCmd)

	return cmd, nil
}

func newStatusCommand() (*cobra.Command, error) {
	opts := DefaultReleaseStatusOptions()

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Generate image promotion report for Helm releases",
		Long: `Generate image promotion report for Helm releases.

This command extracts container images from deployed Helm releases and generates
a structured report showing image promotion data. If no release or namespace is
specified, it will discover and report on all Helm releases in the cluster.

The report includes metadata about the release and lists all container images
with their workload context.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleaseStatus(cmd.Context(), opts)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := BindReleaseStatusOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runReleaseStatus(ctx context.Context, opts *RawReleaseStatusOptions) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	// Get releases to process
	releases, err := release.DiscoverReleases(ctx, completed.HelmClient, completed.ReleaseName, completed.Namespace)
	if err != nil {
		return fmt.Errorf("failed to discover Helm releases: %w", err)
	}

	if len(releases) == 0 {
		return fmt.Errorf("no Helm releases found")
	}

	// Generate reports for all discovered releases
	reports, err := release.GenerateReports(ctx, completed.HelmClient, completed.KubeClient, releases, completed.AroHcpCommit, completed.SdpPipelinesCommit)
	if err != nil {
		return fmt.Errorf("failed to generate reports: %w", err)
	}

	// Output reports to stdout
	return release.OutputReports(reports, completed.OutputFormat, completed.AroHcpCommit, completed.SdpPipelinesCommit)
}
