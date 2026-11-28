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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/tools/grafanactl/internal/grafana"
)

const foldersGroupID = "folders"

func NewFolderCommand(group string) (*cobra.Command, error) {
	folderCmd := &cobra.Command{
		Use:     "folder",
		Short:   "Manage Grafana folders",
		Long:    "Create, inspect, list, and delete folders in an Azure Managed Grafana instance.",
		GroupID: group,
	}

	folderCmd.AddGroup(&cobra.Group{
		ID:    foldersGroupID,
		Title: "Folder Commands:",
	})

	createOpts := BuildDefaultFolderOptions()
	createCmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a folder",
		Long:    "Create a folder with the given UID and title. Succeeds without changing anything when the folder already exists.",
		GroupID: foldersGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return createOpts.RunCreate(cmd.Context())
		},
	}
	if err := BindFolderCreateOptions(createOpts, createCmd); err != nil {
		return nil, err
	}

	getOpts := BuildDefaultFolderOptions()
	getCmd := &cobra.Command{
		Use:     "get",
		Short:   "Show one folder",
		Long:    "Show the title, creation time, and link of the folder with the given UID.",
		GroupID: foldersGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return getOpts.RunGet(cmd.Context())
		},
	}
	if err := BindFolderUIDOption(getOpts, getCmd); err != nil {
		return nil, err
	}

	listOpts := BuildDefaultFolderOptions()
	listCmd := &cobra.Command{
		Use:     "list",
		Short:   "List folders",
		Long:    "List the folders in the Grafana instance, optionally restricted to a UID prefix. Creation times are not reported; use `folder get` for one folder.",
		GroupID: foldersGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listOpts.RunGetList(cmd.Context())
		},
	}
	if err := BindFolderListOptions(listOpts, listCmd); err != nil {
		return nil, err
	}

	deleteOpts := BuildDefaultFolderOptions()
	deleteCmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete a folder",
		Long:    "Delete the folder with the given UID, along with the dashboards it holds. Succeeds when the folder does not exist.",
		GroupID: foldersGroupID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return deleteOpts.RunDelete(cmd.Context())
		},
	}
	if err := BindFolderUIDOption(deleteOpts, deleteCmd); err != nil {
		return nil, err
	}

	folderCmd.AddCommand(createCmd, getCmd, listCmd, deleteCmd)

	return folderCmd, nil
}

// complete runs the given Validate* method followed by Complete, and applies
// the --timeout flag to the returned context. Each RunX below passes the
// Validate* variant that matches what it is about to do, so a malformed or
// missing flag is rejected before Complete spends a network round trip and a
// credential exchange on it.
func (opts *RawFolderOptions) complete(ctx context.Context, validate func(context.Context) (*ValidatedFolderOptions, error)) (*CompletedFolderOptions, context.Context, context.CancelFunc, error) {
	validated, err := validate(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("validation failed: %w", err)
	}

	completed, err := validated.Complete(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("completion failed: %w", err)
	}

	cancel := context.CancelFunc(func() {})
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}

	return completed, ctx, cancel, nil
}

func (opts *RawFolderOptions) RunCreate(ctx context.Context) error {
	completed, ctx, cancel, err := opts.complete(ctx, opts.ValidateCreate)
	if err != nil {
		return err
	}
	defer cancel()

	folder, err := completed.Create(ctx)
	if err != nil {
		return err
	}

	return printFolder(completed.OutputFormat, folder)
}

func (opts *RawFolderOptions) RunGet(ctx context.Context) error {
	completed, ctx, cancel, err := opts.complete(ctx, opts.ValidateGet)
	if err != nil {
		return err
	}
	defer cancel()

	folder, err := completed.Get(ctx)
	if err != nil {
		return err
	}

	return printFolder(completed.OutputFormat, folder)
}

func (opts *RawFolderOptions) RunGetList(ctx context.Context) error {
	completed, ctx, cancel, err := opts.complete(ctx, opts.ValidateGetList)
	if err != nil {
		return err
	}
	defer cancel()

	folders, err := completed.GetList(ctx)
	if err != nil {
		return err
	}

	return printFolderList(ctx, completed.OutputFormat, folders)
}

func (opts *RawFolderOptions) RunDelete(ctx context.Context) error {
	completed, ctx, cancel, err := opts.complete(ctx, opts.ValidateDelete)
	if err != nil {
		return err
	}
	defer cancel()

	return completed.Delete(ctx)
}

// Create creates the folder named by the UID and Title options. A folder that
// already exists is returned unchanged, which makes repeated calls safe.
//
// ValidateCreate is the real gate on UID and Title being present and
// well-formed; the checks below are a cheap backstop for a caller that built
// a CompletedFolderOptions without going through it.
func (o *CompletedFolderOptions) Create(ctx context.Context) (FolderInfo, error) {
	logger := logr.FromContextOrDiscard(ctx)

	if o.UID == "" {
		return FolderInfo{}, fmt.Errorf("folder UID is required")
	}
	if o.Title == "" {
		return FolderInfo{}, fmt.Errorf("folder title is required")
	}

	existing, err := o.GrafanaClient.GetFolderByUID(ctx, o.UID)
	switch {
	case err == nil:
		logger.Info("Folder already exists", "uid", existing.UID, "title", existing.Title)
		return o.buildFolderInfo(existing), nil
	case !errors.Is(err, grafana.ErrFolderNotFound):
		return FolderInfo{}, err
	}

	if o.DryRun {
		logger.Info("Dry run - would create folder", "uid", o.UID, "title", o.Title)
		return o.buildFolderInfo(grafana.Folder{UID: o.UID, Title: o.Title}), nil
	}

	created, err := o.GrafanaClient.CreateFolder(ctx, o.UID, o.Title)
	if err != nil {
		if errors.Is(err, grafana.ErrFolderAlreadyExists) {
			// Another writer created the folder between the lookup above and this
			// create. Read it back, so that concurrent callers both succeed.
			existing, getErr := o.GrafanaClient.GetFolderByUID(ctx, o.UID)
			if getErr != nil {
				return FolderInfo{}, errors.Join(err, getErr)
			}

			logger.Info("Folder already exists", "uid", existing.UID, "title", existing.Title)
			return o.buildFolderInfo(existing), nil
		}

		return FolderInfo{}, err
	}

	logger.Info("Created folder", "uid", created.UID, "title", created.Title)
	return o.buildFolderInfo(created), nil
}

// Get returns the folder named by the UID option, including its creation time.
// The error matches grafana.ErrFolderNotFound when no such folder exists.
//
// ValidateGet is the real gate on UID being present; the check below is a
// cheap backstop for a caller that built a CompletedFolderOptions without
// going through it.
func (o *CompletedFolderOptions) Get(ctx context.Context) (FolderInfo, error) {
	if o.UID == "" {
		return FolderInfo{}, fmt.Errorf("folder UID is required")
	}

	folder, err := o.GrafanaClient.GetFolderByUID(ctx, o.UID)
	if err != nil {
		return FolderInfo{}, err
	}

	return o.buildFolderInfo(folder), nil
}

// GetList returns the folders in the Grafana instance, restricted to those
// whose UID starts with the Prefix option when one is set. The results have a
// zero Created, because Grafana's folder list endpoint omits the timestamp.
func (o *CompletedFolderOptions) GetList(ctx context.Context) ([]FolderInfo, error) {
	folders, err := o.GrafanaClient.ListFolders(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]FolderInfo, 0, len(folders))
	for _, f := range folders {
		if !strings.HasPrefix(f.UID, o.Prefix) {
			continue
		}
		result = append(result, o.buildFolderInfo(f))
	}
	return result, nil
}

// Delete removes the folder named by the UID option, along with the dashboards
// it holds. It succeeds when no such folder exists, so that a caller can repeat
// a delete that something else already did.
//
// ValidateDelete is the real gate on UID being present; the check below is a
// cheap backstop for a caller that built a CompletedFolderOptions without
// going through it.
func (o *CompletedFolderOptions) Delete(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	if o.UID == "" {
		return fmt.Errorf("folder UID is required")
	}

	if o.DryRun {
		logger.Info("Dry run - would delete folder", "uid", o.UID)
		return nil
	}

	if err := o.GrafanaClient.DeleteFolderByUID(ctx, o.UID); err != nil {
		if errors.Is(err, grafana.ErrFolderNotFound) {
			logger.Info("Folder does not exist", "uid", o.UID)
			return nil
		}
		return err
	}

	logger.Info("Deleted folder", "uid", o.UID)
	return nil
}

// printFolder writes the single folder a command acted on. JSON output is one
// object, so that a script reads `.url` rather than `.[0].url`.
func printFolder(outputFormat string, folder FolderInfo) error {
	if outputFormat == "json" {
		return encodeJSON(folder)
	}

	printFolderTable([]FolderInfo{folder})
	return nil
}

func printFolderList(ctx context.Context, outputFormat string, folders []FolderInfo) error {
	logger := logr.FromContextOrDiscard(ctx)

	if outputFormat == "json" {
		return encodeJSON(folders)
	}

	if len(folders) == 0 {
		logger.Info("No folders found")
		return nil
	}

	printFolderTable(folders)
	return nil
}

func printFolderTable(folders []FolderInfo) {
	fmt.Printf("%-40s %-40s %-25s %s\n", "UID", "Title", "Created", "URL")
	fmt.Println(strings.Repeat("-", 140))
	for _, f := range folders {
		created := ""
		if !f.Created.IsZero() {
			created = f.Created.UTC().Format(time.RFC3339)
		}
		fmt.Printf("%-40s %-40s %-25s %s\n", f.UID, f.Title, created, f.URL)
	}
}

func encodeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("failed to encode JSON output: %w", err)
	}
	return nil
}
