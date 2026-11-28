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
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/base"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/azure"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
)

// DashboardInfo describes one uploaded Grafana dashboard. It is the result type
// the dashboard subcommands return to an in-process caller, which cannot name
// the internal Grafana domain types.
type DashboardInfo struct {
	UID   string `json:"uid"`
	Title string `json:"title"`
	// URL is the browser link to the dashboard.
	URL string `json:"url"`
}

// RawUploadOptions represents the initial, unvalidated configuration for dashboard upload.
type RawUploadOptions struct {
	*base.BaseOptions
	// File is the path to the dashboard JSON document to upload.
	File string
	// FolderUID identifies the folder to upload into. An empty value uploads
	// into the General folder.
	FolderUID string
	// UID replaces the dashboard's own UID when set.
	UID string
	// Title replaces the dashboard's own title when set.
	Title string
}

// validatedUploadOptions is a private struct that enforces the options validation pattern.
type validatedUploadOptions struct {
	*RawUploadOptions
	*base.CompletedBaseOptions
}

// ValidatedUploadOptions represents upload configuration that has passed validation.
type ValidatedUploadOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedUploadOptions
}

// CompletedUploadOptions represents the final, fully validated and initialized
// configuration for dashboard upload.
type CompletedUploadOptions struct {
	*validatedUploadOptions
	GrafanaClient *grafana.Client
}

// BuildDefaultUploadOptions returns a new RawUploadOptions with default values
func BuildDefaultUploadOptions() *RawUploadOptions {
	return &RawUploadOptions{
		BaseOptions: base.DefaultBaseOptions(),
	}
}

// BindUploadOptions binds command-line flags to the options
func BindUploadOptions(opts *RawUploadOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.File, "file", opts.File, "Path to the dashboard JSON file (required)")
	flags.StringVar(&opts.FolderUID, "folder-uid", opts.FolderUID, "UID of the folder to upload into. Uploads into the General folder when empty")
	flags.StringVar(&opts.UID, "uid", opts.UID, "UID to store the dashboard under. Keeps the UID in the file when empty")
	flags.StringVar(&opts.Title, "title", opts.Title, "Title to store the dashboard under. Keeps the title in the file when empty")

	_ = cmd.MarkFlagRequired("file")
	return nil
}

// Validate performs validation on the raw options
func (o *RawUploadOptions) Validate(ctx context.Context) (*ValidatedUploadOptions, error) {
	completedBase, err := base.ValidateBaseOptions(o.BaseOptions)
	if err != nil {
		return nil, err
	}

	if o.File == "" {
		return nil, fmt.Errorf("dashboard file is required")
	}

	absPath, err := filepath.Abs(o.File)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve dashboard file path: %w", err)
	}

	if _, err := os.Stat(absPath); err != nil {
		return nil, fmt.Errorf("dashboard file not found: %w", err)
	}

	o.File = absPath

	if err := grafana.ValidateUID(o.UID); err != nil {
		return nil, err
	}
	if err := grafana.ValidateUID(o.FolderUID); err != nil {
		return nil, err
	}

	return &ValidatedUploadOptions{
		validatedUploadOptions: &validatedUploadOptions{
			RawUploadOptions:     o,
			CompletedBaseOptions: completedBase,
		},
	}, nil
}

// Complete performs final initialization to create fully usable upload options.
func (o *ValidatedUploadOptions) Complete(ctx context.Context) (*CompletedUploadOptions, error) {
	cred, err := cmdutils.GetAzureTokenCredentialsForCloud(o.CloudConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	clientOpts := o.ARMClientOptions()

	managedGrafanaClient, err := azure.NewManagedGrafanaClient(o.SubscriptionID, cred, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create Managed Grafana client: %w", err)
	}

	grafanaClient, err := grafana.NewClient(ctx, cred, managedGrafanaClient, o.SubscriptionID, o.ResourceGroup, o.GrafanaName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Grafana client: %w", err)
	}

	return &CompletedUploadOptions{
		validatedUploadOptions: o.validatedUploadOptions,
		GrafanaClient:          grafanaClient,
	}, nil
}

// BuildDashboardURL builds the browser link to a dashboard.
func BuildDashboardURL(grafanaBaseURL, uid string) string {
	return fmt.Sprintf("%s/d/%s", grafanaBaseURL, uid)
}
