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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-Tools/tools/grafanactl/config"
)

// DashboardSyncer handles syncing dashboards from the filesystem to Grafana.
type DashboardSyncer struct {
	client    *Client
	config    *config.ObservabilityConfig
	configDir string
	dryRun    bool
	now       func() time.Time
}

// ValidationIssue represents a validation error or warning for a dashboard.
type ValidationIssue struct {
	Folder  string
	Title   string
	Message string
}

type dashboardDocument struct {
	raw  json.RawMessage
	meta dashboardMetadata
}

// fetchExistingState fetches existing folders and dashboards from Grafana.
func (s *DashboardSyncer) fetchExistingState(ctx context.Context) ([]Folder, []FoundBoard, error) {
	folders, err := s.client.ListFolders(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list existing folders: %w", err)
	}

	dashboards, err := s.client.ListDashboards(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list existing dashboards: %w", err)
	}

	return folders, dashboards, nil
}

// NewDashboardSyncer creates a new DashboardSyncer.
func NewDashboardSyncer(client *Client, cfg *config.ObservabilityConfig, configFilePath string, dryRun bool) *DashboardSyncer {
	return &DashboardSyncer{
		client:    client,
		config:    cfg,
		configDir: filepath.Dir(configFilePath),
		dryRun:    dryRun,
		now:       time.Now,
	}
}

// Sync performs the full sync operation.
func (s *DashboardSyncer) Sync(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	existingFolders, existingDashboards, err := s.fetchExistingState(ctx)
	if err != nil {
		return err
	}
	logger.Info("Fetched existing folders", "count", len(existingFolders))
	logger.Info("Fetched existing dashboards", "count", len(existingDashboards))

	dashboardsVisited := make(map[string]bool)
	var validationErrors, validationWarnings []ValidationIssue

	// Process each folder from config
	for _, folder := range s.config.GrafanaDashboards.DashboardFolders {
		folderErrors, folderWarnings, err := s.syncFolder(ctx, folder, existingFolders, existingDashboards, dashboardsVisited)
		if err != nil {
			return fmt.Errorf("failed to sync folder %q: %w", folder.Name, err)
		}
		validationErrors = append(validationErrors, folderErrors...)
		validationWarnings = append(validationWarnings, folderWarnings...)
	}

	// Delete stale dashboards
	if err := s.deleteStale(ctx, existingFolders, existingDashboards, dashboardsVisited); err != nil {
		return fmt.Errorf("failed to delete stale dashboards: %w", err)
	}

	if err := s.syncScratchFolders(ctx); err != nil {
		return fmt.Errorf("failed to sync scratch folders: %w", err)
	}

	// Report validation issues
	reportValidationIssues(ctx, validationErrors, validationWarnings)

	if len(validationErrors) > 0 {
		return fmt.Errorf("validation errors found in %d dashboards", len(validationErrors))
	}

	return nil
}

func (s *DashboardSyncer) syncFolder(ctx context.Context, folder config.DashboardFolder, existingFolders []Folder, existingDashboards []FoundBoard, dashboardsVisited map[string]bool) ([]ValidationIssue, []ValidationIssue, error) {
	logger := logr.FromContextOrDiscard(ctx)
	logger.Info("Syncing folder", "name", folder.Name, "path", folder.Path)

	var validationErrors, validationWarnings []ValidationIssue

	grafanaFolder, err := s.getOrCreateFolder(ctx, folder.Name, existingFolders)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get or create folder %q: %w", folder.Name, err)
	}

	// Read dashboards from filesystem
	dashboards, err := s.readDashboardsFromPath(ctx, folder.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read dashboards from %q: %w", folder.Path, err)
	}

	// Sync each dashboard
	for _, dashboard := range dashboards {
		errors, warnings, err := s.syncDashboard(ctx, dashboard, grafanaFolder, folder.Path, existingDashboards, dashboardsVisited)
		if err != nil {
			logger.Error(err, "Failed to sync dashboard", "title", dashboard.meta.Title)
		}
		validationErrors = append(validationErrors, errors...)
		validationWarnings = append(validationWarnings, warnings...)
	}

	return validationErrors, validationWarnings, nil
}

func (s *DashboardSyncer) getOrCreateFolder(ctx context.Context, name string, existingFolders []Folder) (Folder, error) {
	logger := logr.FromContextOrDiscard(ctx)

	for _, f := range existingFolders {
		if f.Title == name {
			logger.V(1).Info("Folder already exists", "name", name, "uid", f.UID)
			return f, nil
		}
	}

	if s.dryRun {
		logger.Info("DRY_RUN: Would create folder", "name", name)
		// Return a placeholder folder for dry-run mode with Title set for logging
		return Folder{Title: name, UID: "dry-run-" + name}, nil
	}

	folder, err := s.client.CreateFolder(ctx, name)
	if err != nil {
		return Folder{}, fmt.Errorf("failed to create folder %q: %w", name, err)
	}

	logger.Info("Created folder", "name", name, "uid", folder.UID)
	return folder, nil
}

func (s *DashboardSyncer) readDashboardsFromPath(ctx context.Context, path string) ([]dashboardDocument, error) {
	logger := logr.FromContextOrDiscard(ctx)
	fullPath := filepath.Join(s.configDir, path)
	logger.V(1).Info("Reading dashboards", "path", fullPath)

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var dashboards []dashboardDocument
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(fullPath, entry.Name())
		dashboard, err := readDashboardFile(filePath)
		if err != nil {
			logger.Error(err, "Failed to read dashboard file", "file", filePath)
			continue
		}

		dashboards = append(dashboards, dashboard)
	}

	return dashboards, nil
}

func readDashboardFile(path string) (dashboardDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return dashboardDocument{}, fmt.Errorf("failed to read file %q: %w", path, err)
	}

	// Try parsing as wrapped format {"dashboard": {...}}
	var wrapped struct {
		Dashboard json.RawMessage `json:"dashboard"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Dashboard) > 0 {
		dashboard, err := parseDashboard(wrapped.Dashboard)
		if err != nil {
			return dashboardDocument{}, fmt.Errorf("failed to parse wrapped dashboard JSON: %w", err)
		}
		if dashboard.meta.Title != "" {
			return dashboard, nil
		}
	}

	return parseDashboard(data)
}

func parseDashboard(data []byte) (dashboardDocument, error) {
	var meta dashboardMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return dashboardDocument{}, fmt.Errorf("failed to parse dashboard JSON: %w", err)
	}

	return dashboardDocument{
		raw:  append(json.RawMessage(nil), data...),
		meta: meta,
	}, nil
}

func (s *DashboardSyncer) syncDashboard(ctx context.Context, localDashboard dashboardDocument, folder Folder, folderPath string, existingDashboards []FoundBoard, dashboardsVisited map[string]bool) ([]ValidationIssue, []ValidationIssue, error) {
	logger := logr.FromContextOrDiscard(ctx)

	errors, warnings := validateDashboard(localDashboard.meta, folderPath)

	if len(errors) > 0 {
		logger.Info("Skipping dashboard due to validation errors", "title", localDashboard.meta.Title)
		return errors, warnings, nil
	}

	// Mark dashboard UID as visited
	dashboardsVisited[localDashboard.meta.UID] = true

	// Check if dashboard already exists in Grafana
	existingBoard := findExistingDashboard(localDashboard.meta.UID, existingDashboards)

	// If dashboard exists in the correct folder, check if it matches
	if existingBoard != nil && existingBoard.FolderUID == folder.UID {
		remoteDashboard, _, err := s.client.GetRawDashboardByUID(ctx, localDashboard.meta.UID)
		if err != nil {
			return errors, warnings, fmt.Errorf("failed to fetch remote dashboard %q: %w", localDashboard.meta.Title, err)
		}
		if areDashboardsEqual(remoteDashboard, localDashboard.raw) {
			logger.V(1).Info("Dashboard matches, no update needed", "title", localDashboard.meta.Title)
			return errors, warnings, nil
		}
	}

	// Dashboard needs to be created or updated
	action := "Creating"
	if existingBoard != nil {
		action = "Updating"
	}
	logger.Info(action+" dashboard", "title", localDashboard.meta.Title, "folder", folder.Title)

	if s.dryRun {
		logger.Info("DRY_RUN: Would "+strings.ToLower(action)+" dashboard", "title", localDashboard.meta.Title, "folder", folder.Title)
		return errors, warnings, nil
	}

	dashboardToUpload, err := normalizeDashboard(localDashboard.raw)
	if err != nil {
		return errors, warnings, fmt.Errorf("failed to prepare dashboard %q for upload: %w", localDashboard.meta.Title, err)
	}

	return errors, warnings, s.client.SetRawDashboard(ctx, dashboardToUpload, folder.UID, true)
}

func findExistingDashboard(uid string, existingDashboards []FoundBoard) *FoundBoard {
	for i, d := range existingDashboards {
		if d.UID == uid {
			return &existingDashboards[i]
		}
	}
	return nil
}

func areDashboardsEqual(remote, local []byte) bool {
	remoteJSON, err := normalizeDashboard(remote)
	if err != nil {
		return false
	}
	localJSON, err := normalizeDashboard(local)
	if err != nil {
		return false
	}

	return bytes.Equal(remoteJSON, localJSON)
}

func normalizeDashboard(raw []byte) ([]byte, error) {
	var dashboard map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&dashboard); err != nil {
		return nil, err
	}
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard is not a JSON object")
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("dashboard contains multiple JSON values")
		}
		return nil, fmt.Errorf("dashboard contains trailing data: %w", err)
	}

	dashboard["id"] = json.Number("0")
	dashboard["version"] = json.Number("0")

	return json.Marshal(dashboard)
}

// validateDashboard validates a dashboard and returns validation errors and warnings.
// If errors are returned, the dashboard should not be synced.
func validateDashboard(localDashboard dashboardMetadata, folderPath string) (errors []ValidationIssue, warnings []ValidationIssue) {
	// Check for required fields
	if localDashboard.Title == "" {
		errors = append(errors, ValidationIssue{
			Folder:  folderPath,
			Title:   "(unknown)",
			Message: "Invalid dashboard, missing 'title' key",
		})
		return errors, warnings // Return early since Title is needed for following validations
	}

	if localDashboard.UID == "" {
		errors = append(errors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: "Invalid dashboard, missing 'uid' key",
		})
	}

	if len(localDashboard.UID) > 40 {
		errors = append(errors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: fmt.Sprintf("Dashboard uid '%s' is too long, must be less than 40 characters", localDashboard.UID),
		})
	}

	// Check for templating
	if len(localDashboard.Templating.List) == 0 {
		errors = append(errors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: "Dashboard does not have any variables set",
		})
	}

	// Check for prometheus datasource variable
	hasPrometheusDatasource := false
	var datasourceVar *templateVar
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
		errors = append(errors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: "Dashboard does not have a datasource of type prometheus",
		})
	}

	// Require a regex on the datasource variable so dashboards cannot list
	// every datasource in their picker.
	if datasourceVar != nil && datasourceVar.Regex == "" {
		errors = append(errors, ValidationIssue{
			Folder:  folderPath,
			Title:   localDashboard.Title,
			Message: "Dashboard does not have a regex set for the datasource variable",
		})
	}

	return errors, warnings
}

func (s *DashboardSyncer) deleteStale(ctx context.Context, existingFolders []Folder, existingDashboards []FoundBoard, dashboardsVisited map[string]bool) error {
	logger := logr.FromContextOrDiscard(ctx)

	azureManagedFolderUIDs := make(map[string]bool)
	for _, name := range s.config.GrafanaDashboards.AzureManagedFolders {
		for _, f := range existingFolders {
			if f.Title == name {
				azureManagedFolderUIDs[f.UID] = true
				break
			}
		}
	}

	currentConfigDashboardFolders := make(map[string]bool)
	for _, folder := range s.config.GrafanaDashboards.DashboardFolders {
		for _, f := range existingFolders {
			if f.Title == folder.Name {
				currentConfigDashboardFolders[f.UID] = true
				break
			}
		}
	}

	for _, d := range existingDashboards {

		// Skip Azure managed folders
		if azureManagedFolderUIDs[d.FolderUID] {
			logger.V(1).Info("Skipping deletion, dashboard is in Azure managed folder", "title", d.Title)
			continue
		}

		// skip folders not in current config to avoid deleting dashboards that are outside of our management scope
		if !currentConfigDashboardFolders[d.FolderUID] {
			logger.V(1).Info("Skipping deletion, dashboard is in a folder not managed by current config", "title", d.Title)
			continue
		}

		// Check if dashboard was visited by its UID
		if dashboardsVisited[d.UID] {
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

func reportValidationIssues(ctx context.Context, validationErrors, validationWarnings []ValidationIssue) {
	logger := logr.FromContextOrDiscard(ctx)

	if len(validationWarnings) > 0 {
		logger.Info("Dashboards with warnings", "count", len(validationWarnings))
		for _, w := range validationWarnings {
			logger.Info("Warning", "folder", w.Folder, "title", w.Title, "message", w.Message)
		}
	}

	if len(validationErrors) > 0 {
		logger.Info("Dashboards with errors that need to be fixed", "count", len(validationErrors))
		for _, e := range validationErrors {
			logger.Error(nil, "Validation error", "folder", e.Folder, "title", e.Title, "message", e.Message)
		}
	}
}
