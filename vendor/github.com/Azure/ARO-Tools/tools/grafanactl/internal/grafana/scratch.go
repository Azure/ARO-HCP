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

package grafana

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/tools/grafanactl/config"
)

type scratchGrafanaClient interface {
	ListFolders(ctx context.Context) ([]Folder, error)
	CreateFolder(ctx context.Context, title string) (Folder, error)
	UpdateFolderPermissions(ctx context.Context, folderUID string, permissions ...FolderPermission) error
	ListDashboards(ctx context.Context) ([]FoundBoard, error)
	GetDashboardByUID(ctx context.Context, uid string) (BoardProperties, error)
	DeleteDashboardByUID(ctx context.Context, uid string) error
	SearchFolders(ctx context.Context) ([]FoundBoard, error)
	DeleteFolderByUID(ctx context.Context, uid string) error
}

var scratchFolderPermissions = []FolderPermission{
	{Role: "Viewer", Permission: PermissionEdit},
	{Role: "Editor", Permission: PermissionEdit},
	{Role: "Admin", Permission: PermissionAdmin},
}

func (s *DashboardSyncer) syncScratchFolders(ctx context.Context) error {
	if len(s.config.GrafanaDashboards.ScratchFolders) == 0 {
		return nil
	}
	return syncScratchFolders(ctx, s.client, s.config.GrafanaDashboards.ScratchFolders, s.dryRun, s.now())
}

func syncScratchFolders(ctx context.Context, client scratchGrafanaClient, folders []config.ScratchFolder, dryRun bool, now time.Time) error {
	logger := logr.FromContextOrDiscard(ctx)

	existingFolders, err := client.ListFolders(ctx)
	if err != nil {
		return fmt.Errorf("failed to list folders for scratch sync: %w", err)
	}

	allDashboards, err := client.ListDashboards(ctx)
	if err != nil {
		return fmt.Errorf("failed to list dashboards for scratch sync: %w", err)
	}

	allSearchFolders, err := client.SearchFolders(ctx)
	if err != nil {
		return fmt.Errorf("failed to search folders for scratch sync: %w", err)
	}

	for _, sf := range folders {
		maxAge, err := sf.MaxAge()
		if err != nil {
			return err
		}

		if err := syncOneScratchFolder(ctx, client, sf.Name, maxAge, existingFolders, allDashboards, allSearchFolders, dryRun, now); err != nil {
			return fmt.Errorf("failed to sync scratch folder %q: %w", sf.Name, err)
		}
		logger.Info("Synced scratch folder", "name", sf.Name, "maxAge", maxAge)
	}

	return nil
}

// collectScratchFolderUIDs returns a set of UIDs that belong to the scratch folder tree:
// the root folder itself plus all nested subfolders (recursively).
func collectScratchFolderUIDs(rootUID string, allSearchFolders []FoundBoard) map[string]bool {
	uids := map[string]bool{rootUID: true}
	changed := true
	for changed {
		changed = false
		for _, f := range allSearchFolders {
			if f.Type != "dash-folder" {
				continue
			}
			if uids[f.FolderUID] && !uids[f.UID] {
				uids[f.UID] = true
				changed = true
			}
		}
	}
	return uids
}

func syncOneScratchFolder(ctx context.Context, client scratchGrafanaClient, name string, maxAge time.Duration, existingFolders []Folder, allDashboards []FoundBoard, allSearchFolders []FoundBoard, dryRun bool, now time.Time) error {
	logger := logr.FromContextOrDiscard(ctx)

	folder, err := findOrCreateFolder(ctx, client, name, existingFolders, dryRun)
	if err != nil {
		return err
	}

	if dryRun {
		logger.Info("DRY_RUN: Would set permissions on scratch folder", "name", name)
	} else {
		if err := client.UpdateFolderPermissions(ctx, folder.UID, scratchFolderPermissions...); err != nil {
			return fmt.Errorf("failed to set permissions on folder %q: %w", name, err)
		}
		logger.Info("Set permissions on scratch folder", "name", name)
	}

	scratchUIDs := collectScratchFolderUIDs(folder.UID, allSearchFolders)

	deletedDashboards := sets.New[string]()
	cutoff := now.Add(-maxAge)
	for _, db := range allDashboards {
		if !scratchUIDs[db.FolderUID] {
			continue
		}

		props, err := client.GetDashboardByUID(ctx, db.UID)
		if err != nil {
			logger.Error(err, "Failed to get metadata for scratch dashboard, skipping", "title", db.Title, "uid", db.UID)
			continue
		}

		if !props.Created.Before(cutoff) {
			logger.V(1).Info("Scratch dashboard not expired", "title", db.Title, "uid", db.UID, "created", props.Created, "cutoff", cutoff)
			continue
		}

		if dryRun {
			logger.Info("DRY_RUN: Would delete expired scratch dashboard", "title", db.Title, "uid", db.UID, "created", props.Created)
			deletedDashboards.Insert(db.UID)
		} else {
			logger.Info("Deleting expired scratch dashboard", "title", db.Title, "uid", db.UID, "created", props.Created)
			if err := client.DeleteDashboardByUID(ctx, db.UID); err != nil {
				logger.Error(err, "Failed to delete expired scratch dashboard, continuing", "title", db.Title, "uid", db.UID)
			} else {
				deletedDashboards.Insert(db.UID)
			}
		}
	}

	deleteEmptySubfolders(ctx, client, folder.UID, allDashboards, allSearchFolders, scratchUIDs, deletedDashboards, dryRun)

	return nil
}

// deleteEmptySubfolders removes subfolders of the scratch folder that contain no
// dashboards (after expiry deletion). Processes leaf-first so nested empty trees
// are fully removed.
func deleteEmptySubfolders(ctx context.Context, client scratchGrafanaClient, rootUID string, allDashboards []FoundBoard, allSearchFolders []FoundBoard, scratchUIDs map[string]bool, deletedDashboards sets.Set[string], dryRun bool) {
	// Build parent→children map for subfolders only (exclude root).
	children := make(map[string][]FoundBoard)
	for _, f := range allSearchFolders {
		if f.Type != "dash-folder" || !scratchUIDs[f.UID] || f.UID == rootUID {
			continue
		}
		children[f.FolderUID] = append(children[f.FolderUID], f)
	}

	dashCount := make(map[string]int)
	for _, db := range allDashboards {
		if scratchUIDs[db.FolderUID] && !deletedDashboards.Has(db.UID) {
			dashCount[db.FolderUID]++
		}
	}

	// Recursively delete leaf-first, starting from direct children of root.
	for _, child := range children[rootUID] {
		deleteEmptyRecursive(ctx, client, child.UID, children, dashCount, dryRun)
	}
}

// deleteEmptyRecursive walks the subfolder tree depth-first and deletes folders
// that are empty (no dashboards and no remaining children after recursion).
// Returns true if the folder at uid was deleted (or would be in dry-run).
func deleteEmptyRecursive(ctx context.Context, client scratchGrafanaClient, uid string, children map[string][]FoundBoard, dashCount map[string]int, dryRun bool) bool {
	logger := logr.FromContextOrDiscard(ctx).WithValues("uid", uid)

	hasChildren := false
	for _, child := range children[uid] {
		if !deleteEmptyRecursive(ctx, client, child.UID, children, dashCount, dryRun) {
			hasChildren = true
		}
	}

	if hasChildren || dashCount[uid] > 0 {
		return false
	}

	if dryRun {
		logger.Info("DRY_RUN: Would delete empty scratch subfolder")
		return true
	}

	logger.Info("Deleting empty scratch subfolder")
	if err := client.DeleteFolderByUID(ctx, uid); err != nil {
		logger.Error(err, "Failed to delete empty scratch subfolder, continuing")
		return false
	}
	return true
}

func findOrCreateFolder(ctx context.Context, client scratchGrafanaClient, name string, existingFolders []Folder, dryRun bool) (Folder, error) {
	logger := logr.FromContextOrDiscard(ctx)

	for _, f := range existingFolders {
		if f.Title == name {
			logger.V(1).Info("Scratch folder already exists", "name", name, "uid", f.UID)
			return f, nil
		}
	}

	if dryRun {
		logger.Info("DRY_RUN: Would create scratch folder", "name", name)
		return Folder{Title: name, UID: "dry-run-" + name}, nil
	}

	folder, err := client.CreateFolder(ctx, name)
	if err != nil {
		return Folder{}, fmt.Errorf("failed to create scratch folder %q: %w", name, err)
	}

	logger.Info("Created scratch folder", "name", name, "uid", folder.UID)
	return folder, nil
}
