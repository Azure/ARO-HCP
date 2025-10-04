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
	"strings"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/clients"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

// ImageUpdate represents a successful image update
type ImageUpdate struct {
	Name      string
	NewDigest string
}

// Updater contains all pre-created resources needed for execution
type Updater struct {
	Config          *config.Config
	DryRun          bool
	RegistryClients map[string]clients.RegistryClient
	YAMLEditors     map[string]*yaml.Editor
	Updates         []ImageUpdate
}

// New creates a new Updater with all necessary resources pre-initialized
func New(cfg *config.Config, dryRun bool, registryClients map[string]clients.RegistryClient, yamlEditors map[string]*yaml.Editor) *Updater {
	return &Updater{
		Config:          cfg,
		DryRun:          dryRun,
		RegistryClients: registryClients,
		YAMLEditors:     yamlEditors,
		Updates:         []ImageUpdate{},
	}
}

// UpdateImages processes all images in the configuration using pre-created resources
func (u *Updater) UpdateImages(ctx context.Context) error {
	for name, imageConfig := range u.Config.Images {
		digest, err := u.fetchLatestDigest(imageConfig.Source)
		if err != nil {
			return fmt.Errorf("failed to fetch latest digest for %s: %w", name, err)
		}

		for _, target := range imageConfig.Targets {
			if err := u.updateImage(ctx, name, digest, target); err != nil {
				return fmt.Errorf("failed to update image %s: %w", name, err)
			}
		}
	}

	if !u.DryRun && len(u.Updates) > 0 {
		commitMsg := u.GenerateCommitMessage()
		if commitMsg != "" {
			fmt.Println(commitMsg)
		}
	}

	return nil
}

// fetchLatestDigest retrieves the latest digest from the appropriate registry
func (u *Updater) fetchLatestDigest(source config.Source) (string, error) {
	registry, repository, err := source.ParseImageReference()
	if err != nil {
		return "", fmt.Errorf("failed to parse registry from image reference: %w", err)
	}

	client, exists := u.RegistryClients[registry]
	if !exists {
		return "", fmt.Errorf("no registry client available for %s", registry)
	}

	return client.GetLatestDigest(repository, source.TagPattern)
}

// updateImage processes a single image update
func (u *Updater) updateImage(ctx context.Context, name string, latestDigest string, target config.Target) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("Processing image", "name", name, "latestDigest", latestDigest)

	editor, exists := u.YAMLEditors[target.FilePath]
	if !exists {
		return fmt.Errorf("no YAML editor available for %s", target.FilePath)
	}

	currentDigest, err := editor.GetValue(target.JsonPath)
	if err != nil {
		return fmt.Errorf("failed to get current digest at path %s: %w", target.JsonPath, err)
	}

	logger.Info("Current digest", "name", name, "currentDigest", currentDigest)

	if currentDigest == latestDigest {
		logger.Info("No update needed - digests match", "name", name)
		return nil
	}

	logger.Info("Update needed", "name", name)

	if u.DryRun {
		logger.Info("DRY RUN: Would update image",
			"name", name,
			"jsonPath", target.JsonPath,
			"filePath", target.FilePath,
			"from", currentDigest,
			"to", latestDigest)
		return nil
	}

	if err := editor.SetValue(target.JsonPath, latestDigest); err != nil {
		return fmt.Errorf("failed to set new digest: %w", err)
	}

	if err := editor.Save(); err != nil {
		return fmt.Errorf("failed to save file: %w", err)
	}

	u.Updates = append(u.Updates, ImageUpdate{
		Name:      name,
		NewDigest: latestDigest,
	})

	logger.Info("Updated file successfully", "name", name, "filePath", target.FilePath)
	return nil
}

// GenerateCommitMessage creates a commit message for the updated images
func (u *Updater) GenerateCommitMessage() string {
	if len(u.Updates) == 0 {
		return ""
	}

	msg := "updated image components for dev/int\n\n"

	seen := make(map[string]string)
	for _, update := range u.Updates {
		seen[update.Name] = update.NewDigest
	}

	for name, digest := range seen {
		msg += fmt.Sprintf("- %s: %s\n", name, digest)
	}

	return strings.TrimSuffix(msg, "\n")
}
