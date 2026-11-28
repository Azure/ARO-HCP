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

package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
)

const dashboardsGroupID = "dashboards"

func NewDashboardCommand(group string) (*cobra.Command, error) {
	dashboardCmd := &cobra.Command{
		Use:     "dashboard",
		Short:   "Manage Grafana dashboards",
		Long:    "Operate on individual dashboards in an Azure Managed Grafana instance.",
		GroupID: group,
	}

	dashboardCmd.AddGroup(&cobra.Group{
		ID:    dashboardsGroupID,
		Title: "Dashboard Commands:",
	})

	uploadOpts := BuildDefaultUploadOptions()
	uploadCmd := &cobra.Command{
		Use:     "upload",
		Short:   "Upload a dashboard from a file",
		Long:    "Upload one dashboard JSON file into a folder, optionally storing it under a different UID and title. An existing dashboard with the same UID is replaced.",
		GroupID: dashboardsGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return uploadOpts.RunUpload(cmd.Context())
		},
	}
	if err := BindUploadOptions(uploadOpts, uploadCmd); err != nil {
		return nil, err
	}

	dashboardCmd.AddCommand(uploadCmd)

	return dashboardCmd, nil
}

func (opts *RawUploadOptions) RunUpload(ctx context.Context) error {
	validated, err := opts.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return fmt.Errorf("completion failed: %w", err)
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	dashboard, err := completed.Upload(ctx)
	if err != nil {
		return err
	}

	return printDashboard(completed.OutputFormat, dashboard)
}

// Upload reads the dashboard file, applies the UID and Title overrides, and
// stores the result in the folder named by the FolderUID option, replacing any
// dashboard already held under the same UID.
func (o *CompletedUploadOptions) Upload(ctx context.Context) (DashboardInfo, error) {
	logger := logr.FromContextOrDiscard(ctx)

	raw, err := os.ReadFile(o.File)
	if err != nil {
		return DashboardInfo{}, fmt.Errorf("failed to read dashboard file %q: %w", o.File, err)
	}

	prepared, err := grafana.PrepareDashboardForUpload(raw, o.UID, o.Title)
	if err != nil {
		return DashboardInfo{}, fmt.Errorf("failed to prepare dashboard %q for upload: %w", o.File, err)
	}

	info, err := o.buildDashboardInfo(prepared)
	if err != nil {
		return DashboardInfo{}, fmt.Errorf("failed to read the uid and title of dashboard %q: %w", o.File, err)
	}
	if info.UID == "" {
		return DashboardInfo{}, fmt.Errorf("dashboard %q has no uid; pass --uid", o.File)
	}
	if err := grafana.ValidateUID(info.UID); err != nil {
		return DashboardInfo{}, fmt.Errorf("dashboard %q has an invalid uid: %w", o.File, err)
	}
	if info.Title == "" {
		return DashboardInfo{}, fmt.Errorf("dashboard %q has no title; pass --title", o.File)
	}

	if o.DryRun {
		logger.Info("Dry run - would upload dashboard", "file", o.File, "uid", info.UID, "title", info.Title, "folder-uid", o.FolderUID)
		return info, nil
	}

	if err := o.GrafanaClient.SetRawDashboard(ctx, prepared, o.FolderUID, true); err != nil {
		return DashboardInfo{}, err
	}

	logger.Info("Uploaded dashboard", "file", o.File, "uid", info.UID, "title", info.Title, "folder-uid", o.FolderUID)
	return info, nil
}

// buildDashboardInfo reads the effective UID and title back out of the prepared
// document, which is where the overrides and the file's own values have been reconciled.
func (o *CompletedUploadOptions) buildDashboardInfo(prepared []byte) (DashboardInfo, error) {
	uid, err := grafana.GetDashboardStringField(prepared, "uid")
	if err != nil {
		return DashboardInfo{}, err
	}

	title, err := grafana.GetDashboardStringField(prepared, "title")
	if err != nil {
		return DashboardInfo{}, err
	}

	return DashboardInfo{
		UID:   uid,
		Title: title,
		URL:   BuildDashboardURL(o.GrafanaClient.GetBaseURL(), uid),
	}, nil
}

func printDashboard(outputFormat string, dashboard DashboardInfo) error {
	if outputFormat == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(dashboard); err != nil {
			return fmt.Errorf("failed to encode JSON output: %w", err)
		}
		return nil
	}

	fmt.Printf("%s\t%s\t%s\n", dashboard.UID, dashboard.Title, dashboard.URL)
	return nil
}
