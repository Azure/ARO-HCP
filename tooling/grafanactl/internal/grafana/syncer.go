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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"github.com/grafana-tools/sdk"

	"github.com/Azure/ARO-HCP/tooling/grafanactl/internal/config"
)

// DashboardSyncer handles syncing dashboards from the filesystem to Grafana.
type DashboardSyncer struct {
	client             *Client
	config             *config.ObservabilityConfig
	configDir          string
	dryRun             bool
	existingFolders    []sdk.Folder
	existingDashboards []sdk.FoundBoard
	dashboardsVisited  map[string]bool
	validationErrors   []ValidationIssue
	validationWarnings []ValidationIssue
}

// ValidationIssue represents a validation error or warning for a dashboard.
type ValidationIssue struct {
	Folder  string
	Title   string
	Message string
}

// NewDashboardSyncer creates a new DashboardSyncer.
func NewDashboardSyncer(client *Client, cfg *config.ObservabilityConfig, configFilePath string, dryRun bool) *DashboardSyncer {
	return &DashboardSyncer{
		client:            client,
		config:            cfg,
		configDir:         filepath.Dir(configFilePath),
		dryRun:            dryRun,
		dashboardsVisited: make(map[string]bool),
	}
}

// Sync performs the full sync operation.
func (s *DashboardSyncer) Sync(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	var err error

	s.existingFolders, err = s.client.ListFolders(ctx)
	if err != nil {
		return fmt.Errorf("failed to list existing folders: %w", err)
	}
	logger.Info("Fetched existing folders", "count", len(s.existingFolders))

	s.existingDashboards, err = s.client.ListDashboards(ctx)
	if err != nil {
		return fmt.Errorf("failed to list existing dashboards: %w", err)
	}
	logger.Info("Fetched existing dashboards", "count", len(s.existingDashboards))

	// Process each folder from config
	for _, folder := range s.config.GrafanaDashboards.DashboardFolders {
		if err := s.syncFolder(ctx, folder); err != nil {
			return fmt.Errorf("failed to sync folder %q: %w", folder.Name, err)
		}
	}

	// Delete stale dashboards
	if err := s.deleteStale(ctx); err != nil {
		return fmt.Errorf("failed to delete stale dashboards: %w", err)
	}

	// Report validation issues
	s.reportValidationIssues(ctx)

	if len(s.validationErrors) > 0 {
		return fmt.Errorf("validation errors found in %d dashboards", len(s.validationErrors))
	}

	return nil
}

func (s *DashboardSyncer) syncFolder(ctx context.Context, folder config.DashboardFolder) error {
	logger := logr.FromContextOrDiscard(ctx)
	logger.Info("Syncing folder", "name", folder.Name, "path", folder.Path)

	grafanaFolder, err := s.getOrCreateFolder(ctx, folder.Name)
	if err != nil {
		return fmt.Errorf("failed to get or create folder %q: %w", folder.Name, err)
	}

	// Read dashboards from filesystem
	dashboards, err := s.readDashboardsFromPath(ctx, folder.Path)
	if err != nil {
		return fmt.Errorf("failed to read dashboards from %q: %w", folder.Path, err)
	}

	// Sync each dashboard
	for _, dashboard := range dashboards {
		if err := s.syncDashboard(ctx, dashboard, grafanaFolder, folder.Path); err != nil {
			logger.Error(err, "Failed to sync dashboard", "title", dashboard.Title)
		}
	}

	return nil
}

func (s *DashboardSyncer) getOrCreateFolder(ctx context.Context, name string) (sdk.Folder, error) {
	logger := logr.FromContextOrDiscard(ctx)

	for _, f := range s.existingFolders {
		if f.Title == name {
			logger.V(1).Info("Folder already exists", "name", name, "uid", f.UID, "id", f.ID)
			return f, nil
		}
	}

	if s.dryRun {
		logger.Info("DRY_RUN: Would create folder", "name", name)
		// Return a placeholder folder for dry-run mode with Title set for logging
		return sdk.Folder{Title: name, UID: "dry-run-" + name}, nil
	}

	folder, err := s.client.CreateFolder(ctx, name)
	if err != nil {
		return sdk.Folder{}, fmt.Errorf("failed to create folder %q: %w", name, err)
	}

	logger.Info("Created folder", "name", name, "uid", folder.UID, "id", folder.ID)
	s.existingFolders = append(s.existingFolders, folder)
	return folder, nil
}

func (s *DashboardSyncer) readDashboardsFromPath(ctx context.Context, path string) ([]sdk.Board, error) {
	logger := logr.FromContextOrDiscard(ctx)
	fullPath := filepath.Join(s.configDir, path)
	logger.V(1).Info("Reading dashboards", "path", fullPath)

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var dashboards []sdk.Board
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(fullPath, entry.Name())
		dashboard, err := s.readDashboardFile(filePath)
		if err != nil {
			logger.Error(err, "Failed to read dashboard file", "file", filePath)
			continue
		}

		dashboards = append(dashboards, dashboard)
	}

	return dashboards, nil
}

func (s *DashboardSyncer) readDashboardFile(path string) (sdk.Board, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sdk.Board{}, fmt.Errorf("failed to read file %q: %w", path, err)
	}

	// Try parsing as wrapped format {"dashboard": {...}}
	var wrapped struct {
		Dashboard sdk.Board `json:"dashboard"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Dashboard.Title != "" {
		return wrapped.Dashboard, nil
	}

	// Try parsing as raw dashboard
	var board sdk.Board
	if err := json.Unmarshal(data, &board); err != nil {
		return sdk.Board{}, fmt.Errorf("failed to parse dashboard JSON: %w", err)
	}

	return board, nil
}

func (s *DashboardSyncer) syncDashboard(ctx context.Context, localDashboard sdk.Board, folder sdk.Folder, folderPath string) error {
	logger := logr.FromContextOrDiscard(ctx)

	s.validateDashboard(localDashboard, folderPath)

	// Mark dashboard UID as visited
	s.dashboardsVisited[localDashboard.UID] = true

	// Check if dashboard already exists in Grafana
	existingBoard := s.findExistingDashboard(localDashboard.UID)

	// If dashboard exists in the correct folder, check if it matches
	if existingBoard != nil && existingBoard.FolderUID == folder.UID {
		remoteDashboard, _, err := s.client.GetDashboardByUID(ctx, localDashboard.UID)
		if err != nil {
			return fmt.Errorf("failed to fetch remote dashboard %q: %w", localDashboard.Title, err)
		}
		if areDashboardsEqual(remoteDashboard, localDashboard) {
			logger.V(1).Info("Dashboard matches, no update needed", "title", localDashboard.Title)
			return nil
		}
	}

	// Dashboard needs to be created or updated
	action := "Creating"
	if existingBoard != nil {
		action = "Updating"
	}
	logger.Info(action+" dashboard", "title", localDashboard.Title, "folder", folder.Title)

	if s.dryRun {
		logger.Info("DRY_RUN: Would "+strings.ToLower(action)+" dashboard", "title", localDashboard.Title, "folder", folder.Title)
		return nil
	}

	// Clear ID and Version so Grafana uses UID for lookup (values in JSON files may be stale)
	localDashboard.ID = 0
	localDashboard.Version = 0
	return s.client.SetDashboard(ctx, localDashboard, folder.ID, true)
}

func (s *DashboardSyncer) findExistingDashboard(uid string) *sdk.FoundBoard {
	for i, d := range s.existingDashboards {
		if d.UID == uid {
			return &s.existingDashboards[i]
		}
	}
	return nil
}

func areDashboardsEqual(remote, local sdk.Board) bool {
	// Clear fields that change on save
	remote.ID = 0
	local.ID = 0
	remote.Version = 0
	local.Version = 0

	// Compare JSON representations to avoid type mismatches
	// (e.g., string "1" vs float64 1 in interface{} fields)
	remoteJSON, err := json.Marshal(remote)
	if err != nil {
		return false
	}
	localJSON, err := json.Marshal(local)
	if err != nil {
		return false
	}

	return string(remoteJSON) == string(localJSON)
}

func (s *DashboardSyncer) validateDashboard(localDashboard sdk.Board, folderPath string) {
	// Check for required fields
	if localDashboard.Title == "" {
		s.validationErrors = append(s.validationErrors, ValidationIssue{
			Folder:  folderPath,
			Title:   "(unknown)",
			Message: "Invalid dashboard, missing 'title' key",
		})
		return
	}

	if localDashboard.UID == "" {
		s.validationErrors = append(s.validationErrors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: "Invalid dashboard, missing 'uid' key",
		})
	}

	if len(localDashboard.UID) > 40 {
		s.validationErrors = append(s.validationErrors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: fmt.Sprintf("Dashboard uid '%s' is too long, must be less than 40 characters", localDashboard.UID),
		})
	}

	// Check for templating
	if len(localDashboard.Templating.List) == 0 {
		s.validationErrors = append(s.validationErrors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: "Dashboard does not have any variables set",
		})
	}

	// Check for prometheus datasource variable
	hasPrometheusDatasource := false
	var datasourceVar *sdk.TemplateVar
	for i, v := range localDashboard.Templating.List {
		if !hasPrometheusDatasource && v.Query != nil {
			if query, ok := v.Query.(string); ok && query == "prometheus" {
				hasPrometheusDatasource = true
			}
		}
		if datasourceVar == nil && v.Type == "datasource" {
			datasourceVar = &localDashboard.Templating.List[i]
		}
		if hasPrometheusDatasource && datasourceVar != nil {
			break
		}
	}

	if !hasPrometheusDatasource {
		s.validationErrors = append(s.validationErrors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: "Dashboard does not have a datasource of type prometheus",
		})
	}

	// Warning: check for regex on datasource variable
	if datasourceVar != nil && datasourceVar.Regex == "" {
		s.validationWarnings = append(s.validationWarnings, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: "Dashboard does not have a regex set for the datasource variable",
		})
	}
}

func (s *DashboardSyncer) deleteStale(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	azureManagedFolderUIDs := make(map[string]bool)
	for _, name := range s.config.GrafanaDashboards.AzureManagedFolders {
		for _, f := range s.existingFolders {
			if f.Title == name {
				azureManagedFolderUIDs[f.UID] = true
				break
			}
		}
	}

	for _, d := range s.existingDashboards {
		// Check if dashboard was visited by its UID
		if s.dashboardsVisited[d.UID] {
			continue
		}

		// Skip Azure managed folders
		if azureManagedFolderUIDs[d.FolderUID] {
			logger.V(1).Info("Skipping deletion, dashboard is in Azure managed folder", "title", d.Title)
			continue
		}

		logger.Info("Deleting stale dashboard", "title", d.Title, "uid", d.UID)

		if s.dryRun {
			logger.Info("DRY_RUN: Would delete dashboard", "title", d.Title)
			continue
		}

		if err := s.client.DeleteDashboardByUID(ctx, d.UID); err != nil {
			logger.Error(err, "Failed to delete stale dashboard", "title", d.Title)
		}
	}

	return nil
}

func (s *DashboardSyncer) reportValidationIssues(ctx context.Context) {
	logger := logr.FromContextOrDiscard(ctx)

	if len(s.validationWarnings) > 0 {
		logger.Info("Dashboards with warnings", "count", len(s.validationWarnings))
		for _, w := range s.validationWarnings {
			logger.Info("Warning", "folder", w.Folder, "title", w.Title, "message", w.Message)
		}
	}

	if len(s.validationErrors) > 0 {
		logger.Info("Dashboards with errors that need to be fixed", "count", len(s.validationErrors))
		for _, e := range s.validationErrors {
			logger.Error(nil, "Validation error", "folder", e.Folder, "title", e.Title, "message", e.Message)
		}
	}
}
