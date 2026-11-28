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

package folder

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/base"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/azure"
	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
)

// ErrNotFound reports that Grafana holds no folder with the requested UID.
// Get returns an error that matches it. It is re-exported here because
// the Grafana client it comes from is internal to this module, so a caller
// outside the module cannot name the original.
var ErrNotFound = grafana.ErrFolderNotFound

// FolderInfo describes one Grafana folder. It is the result type the folder
// subcommands return to an in-process caller, which cannot name the internal
// Grafana domain types.
type FolderInfo struct {
	UID   string `json:"uid"`
	Title string `json:"title"`
	// Created is the folder's creation time. It is zero in List results, and
	// absent from their JSON output, because Grafana omits the timestamp from
	// its folder list endpoint; call Get for a folder whose creation time
	// matters.
	Created time.Time `json:"created,omitzero"`
	// URL is the browser link to the folder.
	URL string `json:"url"`
}

// RawFolderOptions represents the initial, unvalidated configuration for folder operations.
type RawFolderOptions struct {
	*base.BaseOptions
	UID    string
	Title  string
	Prefix string
}

// validatedFolderOptions is a private struct that enforces the options validation pattern.
type validatedFolderOptions struct {
	*RawFolderOptions
	*base.CompletedBaseOptions
}

// ValidatedFolderOptions represents folder configuration that has passed validation.
type ValidatedFolderOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package
	*validatedFolderOptions
}

// CompletedFolderOptions represents the final, fully validated and initialized
// configuration for folder operations.
type CompletedFolderOptions struct {
	*validatedFolderOptions
	GrafanaClient *grafana.Client
}

// BuildDefaultFolderOptions returns a new RawFolderOptions with default values
func BuildDefaultFolderOptions() *RawFolderOptions {
	return &RawFolderOptions{
		BaseOptions: base.DefaultBaseOptions(),
	}
}

// BindFolderUIDOption binds the flags shared by the subcommands that address a
// single folder by UID.
func BindFolderUIDOption(opts *RawFolderOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	cmd.Flags().StringVar(&opts.UID, "uid", opts.UID, "UID of the folder (required)")
	_ = cmd.MarkFlagRequired("uid")
	return nil
}

// BindFolderCreateOptions binds command-line flags to the options for folder create.
func BindFolderCreateOptions(opts *RawFolderOptions, cmd *cobra.Command) error {
	if err := BindFolderUIDOption(opts, cmd); err != nil {
		return err
	}

	cmd.Flags().StringVar(&opts.Title, "title", opts.Title, "Display title of the folder (required)")
	_ = cmd.MarkFlagRequired("title")
	return nil
}

// BindFolderListOptions binds command-line flags to the options for folder list.
func BindFolderListOptions(opts *RawFolderOptions, cmd *cobra.Command) error {
	if err := base.BindBaseOptions(opts.BaseOptions, cmd); err != nil {
		return err
	}

	cmd.Flags().StringVar(&opts.Prefix, "prefix", opts.Prefix, "List only folders whose UID starts with this prefix")
	return nil
}

// validateBase validates the options every folder subcommand shares: the base
// options common to all grafanactl commands. It does not check UID, Title, or
// Prefix, because which of those are required depends on the subcommand; the
// Validate* methods below layer that check on top, before Complete does any
// network or credential work.
func (o *RawFolderOptions) validateBase(ctx context.Context) (*ValidatedFolderOptions, error) {
	completedBase, err := base.ValidateBaseOptions(o.BaseOptions)
	if err != nil {
		return nil, err
	}

	return &ValidatedFolderOptions{
		validatedFolderOptions: &validatedFolderOptions{
			RawFolderOptions:     o,
			CompletedBaseOptions: completedBase,
		},
	}, nil
}

// ValidateCreate validates the options for folder create: a non-empty UID and
// Title, and a UID that Grafana accepts. The UID checks are shared with
// ValidateGet and ValidateDelete through validateUID; Title is checked here,
// since create is the only operation that needs one.
func (o *RawFolderOptions) ValidateCreate(ctx context.Context) (*ValidatedFolderOptions, error) {
	validated, err := o.validateUID(ctx)
	if err != nil {
		return nil, err
	}

	if o.Title == "" {
		return nil, fmt.Errorf("folder title is required")
	}

	return validated, nil
}

// validateUID validates the options for the folder subcommands that address
// one folder by UID: create, get, and delete all require a non-empty UID that
// Grafana accepts.
func (o *RawFolderOptions) validateUID(ctx context.Context) (*ValidatedFolderOptions, error) {
	validated, err := o.validateBase(ctx)
	if err != nil {
		return nil, err
	}

	if o.UID == "" {
		return nil, fmt.Errorf("folder UID is required")
	}
	if err := grafana.ValidateUID(o.UID); err != nil {
		return nil, err
	}

	return validated, nil
}

// ValidateGet validates the options for folder get.
func (o *RawFolderOptions) ValidateGet(ctx context.Context) (*ValidatedFolderOptions, error) {
	return o.validateUID(ctx)
}

// ValidateDelete validates the options for folder delete.
func (o *RawFolderOptions) ValidateDelete(ctx context.Context) (*ValidatedFolderOptions, error) {
	return o.validateUID(ctx)
}

// ValidateGetList validates the options for folder list, which has no
// required fields: Prefix is optional and matches every folder when empty.
func (o *RawFolderOptions) ValidateGetList(ctx context.Context) (*ValidatedFolderOptions, error) {
	return o.validateBase(ctx)
}

// Complete performs final initialization to create fully usable folder options.
func (o *ValidatedFolderOptions) Complete(ctx context.Context) (*CompletedFolderOptions, error) {
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

	return &CompletedFolderOptions{
		validatedFolderOptions: o.validatedFolderOptions,
		GrafanaClient:          grafanaClient,
	}, nil
}

// buildFolderInfo converts an internal folder into the exported result type.
func (o *CompletedFolderOptions) buildFolderInfo(f grafana.Folder) FolderInfo {
	return FolderInfo{
		UID:     f.UID,
		Title:   f.Title,
		Created: f.Created,
		URL:     BuildFolderURL(o.GrafanaClient.GetBaseURL(), f.UID),
	}
}

// BuildFolderURL builds the browser link to a folder.
func BuildFolderURL(grafanaBaseURL, uid string) string {
	return fmt.Sprintf("%s/dashboards/f/%s", grafanaBaseURL, uid)
}
