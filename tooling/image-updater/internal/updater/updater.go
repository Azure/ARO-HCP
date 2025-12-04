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

package updater

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/clients"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/output"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

const (
	DefaultArchitecture = "amd64"
)

// Updater contains all pre-created resources needed for execution
type Updater struct {
	Config          *config.Config
	DryRun          bool
	ForceUpdate     bool
	RegistryClients map[string]clients.RegistryClient
	YAMLEditors     map[string]*yaml.Editor
	Updates         map[string][]yaml.Update
}

// New creates a new Updater with all necessary resources pre-initialized
func New(cfg *config.Config, dryRun bool, forceUpdate bool, registryClients map[string]clients.RegistryClient, yamlEditors map[string]*yaml.Editor) *Updater {
	return &Updater{
		Config:          cfg,
		DryRun:          dryRun,
		ForceUpdate:     forceUpdate,
		RegistryClients: registryClients,
		YAMLEditors:     yamlEditors,
		Updates:         make(map[string][]yaml.Update),
	}
}

// UpdateImages processes all images in the configuration
func (u *Updater) UpdateImages(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}

	totalImages := len(u.Config.Images)
	logger.V(2).Info("starting image updates", "totalImages", totalImages)

	imageNum := 0
	updatedCount := 0
	for name, imageConfig := range u.Config.Images {
		imageNum++
		logger.V(2).Info("processing image", "name", name, "source", imageConfig.Source.Image, "tagPattern", imageConfig.Source.TagPattern)

		imageInfo, err := u.fetchLatestDigest(ctx, imageConfig.Source)
		if err != nil {
			return fmt.Errorf("failed to fetch latest digest for %s: %w", name, err)
		}

		logger.V(2).Info("found latest tag", "name", name, "tag", imageInfo.Name, "digest", imageInfo.Digest)

		for _, target := range imageConfig.Targets {
			updated, err := u.ProcessImageUpdates(ctx, name, imageInfo, target)
			if err != nil {
				return fmt.Errorf("failed to update image %s: %w", name, err)
			}
			if updated {
				updatedCount++
			}
		}
	}

	// Always show summary
	output.PrintSummary(os.Stderr, totalImages, updatedCount, u.DryRun)

	if !u.DryRun && len(u.Updates) > 0 {
		for filePath, updates := range u.Updates {
			editor, exists := u.YAMLEditors[filePath]
			if !exists {
				return fmt.Errorf("no YAML editor available for %s", filePath)
			}

			if err := editor.ApplyUpdates(updates); err != nil {
				return fmt.Errorf("failed to apply updates to %s: %w", filePath, err)
			}
		}

		commitMsg := output.GenerateCommitMessage(u.Updates)
		if commitMsg != "" {
			fmt.Println(commitMsg)
		}
	}

	return nil
}

// fetchLatestDigest retrieves the latest digest from the appropriate registry
func (u *Updater) fetchLatestDigest(ctx context.Context, source config.Source) (*clients.Tag, error) {
	registry, repository, err := source.ParseImageReference()
	if err != nil {
		return nil, fmt.Errorf("failed to parse registry from image reference: %w", err)
	}

	// Determine useAuth for this specific image - default to false if not specified
	useAuth := false
	if source.UseAuth != nil {
		useAuth = *source.UseAuth
	}

	// Use the same key format as in options.go: "registry:useAuth"
	clientKey := fmt.Sprintf("%s:%t", registry, useAuth)
	client, exists := u.RegistryClients[clientKey]
	if !exists {
		return nil, fmt.Errorf("no registry client available for %s (useAuth=%t)", registry, useAuth)
	}

	arch := source.Architecture
	if arch == "" {
		arch = DefaultArchitecture
	}

	return client.GetArchSpecificDigest(ctx, repository, source.TagPattern, arch, source.MultiArch)
}

// ProcessImageUpdates sets up the updates needed for a specific image and target
// Returns true if an update was needed/applied, false otherwise
func (u *Updater) ProcessImageUpdates(ctx context.Context, name string, tag *clients.Tag, target config.Target) (bool, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return false, fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(2).Info("Processing image", "name", name, "latestDigest", tag.Digest, "tag", tag.Name)

	editor, exists := u.YAMLEditors[target.FilePath]
	if !exists {
		return false, fmt.Errorf("no YAML editor available for %s", target.FilePath)
	}

	line, currentDigest, err := editor.GetUpdate(target.JsonPath)
	if err != nil {
		return false, fmt.Errorf("failed to get current digest at path %s: %w", target.JsonPath, err)
	}

	logger.V(2).Info("Current digest", "name", name, "currentDigest", currentDigest)

	// If the target path ends with .sha, we need to strip the sha256: prefix
	// from the digest since sha fields only contain the hash value
	newDigest := tag.Digest
	if strings.HasSuffix(target.JsonPath, ".sha") {
		newDigest = strings.TrimPrefix(tag.Digest, "sha256:")
	}

	if currentDigest == newDigest && !u.ForceUpdate {
		logger.V(2).Info("No update needed - digests match", "name", name)
		return false, nil
	}

	if currentDigest == newDigest && u.ForceUpdate {
		logger.V(2).Info("Force update - regenerating version tag comment", "name", name)
	} else {
		logger.V(2).Info("Update needed", "name", name, "from", currentDigest, "to", newDigest)
	}

	if u.DryRun {
		logger.V(2).Info("DRY RUN: Would update image",
			"name", name,
			"jsonPath", target.JsonPath,
			"filePath", target.FilePath,
			"line", line,
			"from", currentDigest,
			"to", newDigest,
			"tag", tag.Name)
		return true, nil
	}

	// Format the date as YYYY-MM-DD HH:MM if available
	dateStr := ""
	if !tag.LastModified.IsZero() {
		dateStr = tag.LastModified.Format("2006-01-02 15:04")
	}

	u.Updates[target.FilePath] = append(u.Updates[target.FilePath], yaml.Update{
		Name:      name,
		NewDigest: newDigest,
		OldDigest: currentDigest,
		Tag:       tag.Name,
		Date:      dateStr,
		JsonPath:  target.JsonPath,
		FilePath:  target.FilePath,
		Line:      line,
	})

	return true, nil
}
