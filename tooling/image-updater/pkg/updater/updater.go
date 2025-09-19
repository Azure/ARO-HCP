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
	"fmt"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/image-updater/pkg/clients"
	"github.com/Azure/ARO-HCP/tooling/image-updater/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/image-updater/pkg/yaml"
)

// ImageUpdate represents a successful image update
type ImageUpdate struct {
	Name      string
	NewDigest string
}

// Updater handles the image update process
type Updater struct {
	dryRun     bool
	quayClient *clients.QuayClient
	acrClient  *clients.ACRClient
	updates    []ImageUpdate
}

// New creates a new image updater
func New(dryRun bool) *Updater {
	acrClient, err := clients.NewACRClient("arohcpsvcdev.azurecr.io")
	if err != nil {
		// For now, we'll handle this gracefully - ACR client creation might fail if not authenticated
		acrClient = nil
	}

	return &Updater{
		dryRun:     dryRun,
		quayClient: clients.NewQuayClient(),
		acrClient:  acrClient,
	}
}

// UpdateImages processes all images in the configuration
func (u *Updater) UpdateImages(cfg *config.Config) error {
	for name, imageConfig := range cfg.Images {
		digest, err := u.fetchLatestDigest(imageConfig.Source)
		if err != nil {
			return fmt.Errorf("failed to fetch latest digest: %w", err)
		}
		for _, target := range imageConfig.Targets {
			if err := u.updateImage(name, digest, target); err != nil {
				return fmt.Errorf("failed to update image %s: %w", name, err)
			}
		}
	}

	// Output commit message if there were updates and not in dry-run mode
	if !u.dryRun && len(u.updates) > 0 {
		commitMsg := u.GenerateCommitMessage()
		if commitMsg != "" {
			fmt.Printf("=== COMMIT MESSAGE ===\n%s\n", commitMsg)
		}
	}

	return nil
}

// updateImage processes a single image update
func (u *Updater) updateImage(name string, latestDigest string, target config.Target) error {
	fmt.Printf("Processing image: %s\n", name)

	fmt.Printf("  Latest digest: %s\n", latestDigest)

	// Load the target file
	editor, err := yaml.NewEditor(target.FilePath)
	if err != nil {
		return fmt.Errorf("failed to load target file %s: %w", target.FilePath, err)
	}

	// Get current digest
	currentDigest, err := editor.GetValue(target.JsonPath)
	if err != nil {
		return fmt.Errorf("failed to get current digest at path %s: %w", target.JsonPath, err)
	}

	fmt.Printf("  Current digest: %s\n", currentDigest)

	// Check if update is needed
	if currentDigest == latestDigest {
		fmt.Printf("  ✅ No update needed - digests match\n\n\n")
		return nil
	}

	fmt.Printf("  📝 Update needed\n")

	if u.dryRun {
		fmt.Printf("  🔍 DRY RUN: Would update %s in %s\n", target.JsonPath, target.FilePath)
		fmt.Printf("    From: %s\n", currentDigest)
		fmt.Printf("    To:   %s\n", latestDigest)
		return nil
	}

	// Update the digest
	if err := editor.SetValue(target.JsonPath, latestDigest); err != nil {
		return fmt.Errorf("failed to set new digest: %w", err)
	}

	// Save the file
	if err := editor.Save(); err != nil {
		return fmt.Errorf("failed to save file: %w", err)
	}

	// Record the successful update
	u.updates = append(u.updates, ImageUpdate{
		Name:      name,
		NewDigest: latestDigest,
	})

	fmt.Printf("  ✅ Updated %s successfully\n\n\n", target.FilePath)
	return nil
}

// getACRDigest handles ACR registry digest retrieval
func (u *Updater) getACRDigest(source config.Source) (string, error) {
	if u.acrClient == nil {
		return "", fmt.Errorf("ACR client not initialized - authentication may have failed")
	}

	repository, err := source.Repository()
	if err != nil {
		return "", fmt.Errorf("failed to parse repository from image reference: %w", err)
	}

	return u.acrClient.GetLatestDigest(repository)
}

// fetchLatestDigest retrieves the latest digest from the appropriate registry
func (u *Updater) fetchLatestDigest(source config.Source) (string, error) {
	registry, err := source.Registry()
	if err != nil {
		return "", fmt.Errorf("failed to parse registry from image reference: %w", err)
	}

	repository, err := source.Repository()
	if err != nil {
		return "", fmt.Errorf("failed to parse repository from image reference: %w", err)
	}

	switch {
	case strings.Contains(registry, "quay.io"):
		return u.quayClient.GetLatestDigest(repository, source.TagPattern)
	case strings.Contains(registry, "azurecr.io"):
		return u.getACRDigest(source)
	default:
		return "", fmt.Errorf("unsupported registry: %s", registry)
	}
}

// GenerateCommitMessage creates a commit message for the updated images
func (u *Updater) GenerateCommitMessage() string {
	if len(u.updates) == 0 {
		return ""
	}

	msg := "updated image components for dev/int\n\n"

	// Track unique updates (in case same image was updated multiple times)
	seen := make(map[string]string)
	for _, update := range u.updates {
		seen[update.Name] = update.NewDigest
	}

	for name, digest := range seen {
		msg += fmt.Sprintf("- %s: %s\n", name, digest)
	}

	return strings.TrimSuffix(msg, "\n")
}
