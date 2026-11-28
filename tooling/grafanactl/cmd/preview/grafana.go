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

package preview

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/dashboard"
	"github.com/Azure/ARO-Tools/tools/grafanactl/cmd/folder"
)

// This file holds everything that drives the grafanactl folder and dashboard
// commands. The rest of the package works through the two seams below, which a
// test fills in to exercise the preview without Azure.

// folderCreator creates the folder a preview is held in. The folder command
// satisfies it.
type folderCreator interface {
	Create(ctx context.Context) (folder.FolderInfo, error)
}

// uploadDashboard stores one dashboard file under the given UID, and reports
// where it landed. uploadToPreviewFolder is the implementation.
type uploadDashboard func(ctx context.Context, file, uid string) (dashboard.DashboardInfo, error)

// buildCompletedFolderOptions completes the folder command this preview drives.
// It shares the base options, so one set of flags configures both commands.
func (o *CompletedCreateOptions) buildCompletedFolderOptions(ctx context.Context) (*folder.CompletedFolderOptions, error) {
	raw := folder.BuildDefaultFolderOptions()
	raw.BaseOptions = o.BaseOptions
	raw.UID = buildFolderUID(o.PullNumber)
	raw.Title = buildFolderTitle(o.PullNumber)

	validated, err := raw.ValidateCreate(ctx)
	if err != nil {
		return nil, fmt.Errorf("folder validation failed: %w", err)
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return nil, fmt.Errorf("folder completion failed: %w", err)
	}
	return completed, nil
}

// uploadToPreviewFolder stores one dashboard in the folder of this pull request.
// The title is left to the one the document carries.
func (o *CompletedCreateOptions) uploadToPreviewFolder(ctx context.Context, file, uid string) (dashboard.DashboardInfo, error) {
	raw := dashboard.BuildDefaultUploadOptions()
	raw.BaseOptions = o.BaseOptions
	raw.FolderUID = buildFolderUID(o.PullNumber)
	raw.File = file
	raw.UID = uid

	validated, err := raw.Validate(ctx)
	if err != nil {
		return dashboard.DashboardInfo{}, fmt.Errorf("dashboard validation failed: %w", err)
	}
	completed, err := validated.Complete(ctx)
	if err != nil {
		return dashboard.DashboardInfo{}, fmt.Errorf("dashboard completion failed: %w", err)
	}
	return completed.Upload(ctx)
}
