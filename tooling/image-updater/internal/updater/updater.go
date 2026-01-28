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

// PromotionMode defines how image digests are obtained
type PromotionMode int

const (
	// FetchLatest fetches the latest digest from the registry
	FetchLatest PromotionMode = iota
	// PromoteToStage copies digests from int environment to stage
	PromoteToStage
	// PromoteToProduction copies digests from stage environment to prod
	PromoteToProduction
)

// Updater contains all pre-created resources needed for execution
type Updater struct {
	Config          *config.Config
	DryRun          bool
	ForceUpdate     bool
	RegistryClients map[string]clients.RegistryClient
	YAMLEditors     map[string]yaml.EditorInterface
	Updates         map[string][]yaml.Update
	PromotionMode   PromotionMode
	Environments    []string
	OutputFile      string
	OutputFormat    string
}

// New creates a new Updater with all necessary resources pre-initialized
func New(cfg *config.Config, dryRun bool, forceUpdate bool, registryClients map[string]clients.RegistryClient, yamlEditors map[string]yaml.EditorInterface, promotionMode PromotionMode, environments []string, outputFile, outputFormat string) *Updater {
	return &Updater{
		Config:          cfg,
		DryRun:          dryRun,
		ForceUpdate:     forceUpdate,
		RegistryClients: registryClients,
		YAMLEditors:     yamlEditors,
		Updates:         make(map[string][]yaml.Update),
		PromotionMode:   promotionMode,
		Environments:    environments,
		OutputFile:      outputFile,
		OutputFormat:    outputFormat,
	}
}

// UpdateImages processes all images in the configuration
func (u *Updater) UpdateImages(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}

	logger.V(1).Info("starting image updates",
		"totalImages", len(u.Config.Images),
		"promotionMode", u.getPromotionModeString(),
		"environments", u.Environments)

	// Handle promotion mode (copy from lower environment)
	if u.PromotionMode != FetchLatest {
		return u.promoteImages(ctx)
	}

	// Regular fetch mode
	imageNum := 0
	updatedCount := 0
	for name, imageConfig := range u.Config.Images {
		imageNum++
		tagInfo := imageConfig.Source.TagPattern
		if imageConfig.Source.Tag != "" {
			tagInfo = imageConfig.Source.Tag
		}
		logger.V(2).Info("processing image", "name", name, "source", imageConfig.Source.Image, "tag", tagInfo)

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
	}

	// Generate and output results
	if err := u.outputResults(ctx); err != nil {
		return fmt.Errorf("failed to output results: %w", err)
	}

	return nil
}

// outputResults formats and writes the update results
func (u *Updater) outputResults(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}

	// Check if there were any updates to report
	if len(u.Updates) == 0 {
		logger.V(1).Info("No updates to report")
		if u.OutputFile != "" {
			logger.V(1).Info("Skipping output file creation - no updates", "file", u.OutputFile)
		}
		return nil
	}

	// Format the results
	logger.V(2).Info("Formatting results", "format", u.OutputFormat, "updateCount", len(u.Updates))
	formattedOutput, err := output.FormatResults(u.Updates, u.OutputFormat, u.DryRun)
	if err != nil {
		return fmt.Errorf("failed to format results as %s: %w", u.OutputFormat, err)
	}

	if formattedOutput == "" {
		logger.V(1).Info("Formatted output is empty, skipping write")
		return nil
	}

	// Write to file or stdout
	if u.OutputFile != "" {
		logger.V(1).Info("Writing results to file", "file", u.OutputFile, "format", u.OutputFormat, "size", len(formattedOutput))
		if err := os.WriteFile(u.OutputFile, []byte(formattedOutput), 0644); err != nil {
			return fmt.Errorf("failed to write output file %s: %w", u.OutputFile, err)
		}
		logger.Info("Results written successfully", "file", u.OutputFile, "format", u.OutputFormat)
		fmt.Printf("Results written to %s\n", u.OutputFile)
	} else {
		logger.V(2).Info("Writing results to stdout", "format", u.OutputFormat)
		fmt.Print(formattedOutput)
	}

	return nil
}

// promoteImages handles copying digests from a lower environment to a higher environment
func (u *Updater) promoteImages(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}

	sourceEnv, targetEnv := u.getPromotionSourceAndTarget()
	logger = logger.WithValues("from", sourceEnv, "to", targetEnv)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("promoting images")

	var promotions []Promotion
	for name, imageConfig := range u.Config.Images {
		logger.V(1).Info("processing image for promotion", "name", name, "source", imageConfig.Source.Image)

		promotion := Promotion{ImageName: name}
		for _, target := range imageConfig.Targets {
			// Find all the targets thar target the source env
			if target.Env == sourceEnv {
				promotion.SourceImageDeclaration = append(promotion.SourceImageDeclaration, &ImageDeclaration{
					JSONPath: target.JsonPath,
					FilePath: target.FilePath,
				})
			}

			// Find all the targets thar target the target env
			if target.Env == targetEnv {
				promotion.TargetImageDeclaration = append(promotion.TargetImageDeclaration, &ImageDeclaration{
					JSONPath: target.JsonPath,
					FilePath: target.FilePath,
				})
			}
		}

		getEditor := func(filePath string) (yaml.EditorInterface, error) {
			editor, exists := u.YAMLEditors[filePath]
			if !exists {
				return nil, fmt.Errorf("no YAML editor available for %s", filePath)
			}
			return editor, nil
		}

		if err := promotion.Build(getEditor); err != nil {
			if IsSkippablePromotionBuildError(err) {
				logger.Error(err, "skipping image promotion due to invalid/unclear source", "name", name)
				continue
			}
			return fmt.Errorf("failed to build promotion for image %s: %w", name, err)
		}

		promotions = append(promotions, promotion)
	}

	updatedCount := 0
	for _, promotion := range promotions {
		updates, err := promotion.Execute(ctx, u.ForceUpdate)
		if err != nil {
			return fmt.Errorf("failed to execute promotion for image %s: %w", promotion.ImageName, err)
		}
		updatedCount += len(updates)
		for _, update := range updates {
			u.Updates[update.FilePath] = append(u.Updates[update.FilePath], update)
		}
	}

	// Always show summary
	if u.DryRun {
		fmt.Fprintf(os.Stderr, "Promoting from %s to %s: %d images would be updated (dry-run mode)\n",
			sourceEnv, targetEnv, updatedCount)
	} else {
		fmt.Fprintf(os.Stderr, "Promoting from %s to %s: %d images updated\n",
			sourceEnv, targetEnv, updatedCount)
	}

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

		commitMsg := fmt.Sprintf("Promoted images from %s to %s", sourceEnv, targetEnv)
		fmt.Println(commitMsg)
	}

	// Generate and output results
	if err := u.outputResults(ctx); err != nil {
		return fmt.Errorf("failed to output results: %w", err)
	}

	return nil
}

// getPromotionSourceAndTarget returns the source and target environments for promotion
func (u *Updater) getPromotionSourceAndTarget() (source, target string) {
	switch u.PromotionMode {
	case PromoteToStage:
		return "int", "stg"
	case PromoteToProduction:
		return "stg", "prod"
	default:
		return "", ""
	}
}

// getPromotionModeString returns a string representation of the promotion mode
func (u *Updater) getPromotionModeString() string {
	switch u.PromotionMode {
	case FetchLatest:
		return "fetch-latest"
	case PromoteToStage:
		return "promote-to-stage"
	case PromoteToProduction:
		return "promote-to-production"
	default:
		return "unknown"
	}
}

// fetchLatestDigest retrieves the latest digest from the appropriate registry
func (u *Updater) fetchLatestDigest(ctx context.Context, source config.Source) (*clients.Tag, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

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

	// Get the effective version label to use for this source
	versionLabel := source.GetEffectiveVersionLabel()

	// If a specific tag is provided, use the more efficient GetDigestForTag method
	// Otherwise, use GetArchSpecificDigest which requires pagination
	if source.Tag != "" {
		logger.V(2).Info("fetching digest for specific tag (no pagination)", "tag", source.Tag, "versionLabel", versionLabel)
		return client.GetDigestForTag(ctx, repository, source.Tag, arch, source.MultiArch, versionLabel)
	}

	logger.V(2).Info("fetching latest digest using pattern (requires pagination)", "tagPattern", source.TagPattern, "versionLabel", versionLabel)
	return client.GetArchSpecificDigest(ctx, repository, source.GetEffectiveTagPattern(), arch, source.MultiArch, versionLabel)
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

	// Format the date as YYYY-MM-DD HH:MM if available
	dateStr := ""
	if !tag.LastModified.IsZero() {
		dateStr = tag.LastModified.Format("2006-01-02 15:04")
	}

	// Record the update for reporting purposes (both dry-run and real runs)
	u.Updates[target.FilePath] = append(u.Updates[target.FilePath], yaml.Update{
		Name:      name,
		NewDigest: newDigest,
		OldDigest: currentDigest,
		Tag:       tag.Name,
		Version:   tag.Version,
		Date:      dateStr,
		JsonPath:  target.JsonPath,
		FilePath:  target.FilePath,
		Line:      line,
	})

	if u.DryRun {
		logger.V(2).Info("DRY RUN: Would update image",
			"name", name,
			"jsonPath", target.JsonPath,
			"filePath", target.FilePath,
			"line", line,
			"from", currentDigest,
			"to", newDigest,
			"tag", tag.Name)
	}

	return true, nil
}
