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

package mustgather

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	_ "embed"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/openshift/must-gather-clean/pkg/schema"
)

//go:embed default_config.json
var defaultConfig string

type replacement struct {
	Regex              *regexp.Regexp
	ReplacementPattern string
}

var replacementPatterns = []*replacement{
	{
		Regex:              regexp.MustCompile("([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})"),
		ReplacementPattern: "x-uid-%010d",
	},
}

func newCleanCommand() (*cobra.Command, error) {
	opts := DefaultCleanOptions()

	cmd := &cobra.Command{
		Use:              "clean",
		Short:            "Clean must-gather data",
		Long:             `Create must-gather-clean config file from config and possibly run must-gather-clean.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := BindCleanOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func (opts *CleanOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	generatedConfigPath, err := generateMustGatherCleanConfig(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to generate must-gather-clean config: %w", err)
	}

	args := []string{
		"-i", opts.PathToClean,
		"-o", opts.CleanedOutputPath,
		"-c", generatedConfigPath,
	}

	cmd := exec.Command(opts.MustGatherCleanBinary, args...)

	output, err := cmd.CombinedOutput()
	logger.V(4).Info("must-gather-clean output", "output", string(output))
	if err != nil {
		return fmt.Errorf("failed to run must-gather-clean: %w", err)
	}
	return nil
}

func generateMustGatherCleanConfig(ctx context.Context, opts *CleanOptions) (string, error) {
	logger := logr.FromContextOrDiscard(ctx)

	logger.V(4).Info("generating must-gather-clean config", "service-config-path", opts.ServiceConfigPath)

	var cleanConfigBase *schema.SchemaJson
	var err error
	if opts.CleanConfigPath != "" {
		cleanConfigBase, err = schema.ReadConfigFromPath(opts.CleanConfigPath)
		if err != nil {
			return "", fmt.Errorf("failed to read clean config base: %w", err)
		}
	} else {
		logger.Info("no clean config path provided, using default config")

		err = os.WriteFile(filepath.Join(opts.WorkingDir, "default_config.json"), []byte(defaultConfig), 0644)
		if err != nil {
			return "", fmt.Errorf("failed to write default clean config: %w", err)
		}

		cleanConfigBase, err = schema.ReadConfigFromPath(filepath.Join(opts.WorkingDir, "default_config.json"))
		if err != nil {
			return "", fmt.Errorf("failed to read default clean config: %w", err)
		}
	}

	allMatches, err := walkAndMatchRegexPatterns(ctx, opts.ServiceConfigPath, replacementPatterns)
	if err != nil {
		return "", fmt.Errorf("failed to walk and match regex patterns: %w", err)
	}

	for match, replacement := range allMatches {
		cleanConfigBase.Config.Obfuscate = append(cleanConfigBase.Config.Obfuscate, schema.Obfuscate{
			Type: schema.ObfuscateTypeExact,
			ExactReplacements: []schema.ObfuscateExactReplacementsElem{
				{
					Original:    match,
					Replacement: replacement,
				},
			},
		})
	}

	json, err := json.MarshalIndent(cleanConfigBase, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal clean config base: %w", err)
	}
	filename := filepath.Join(opts.WorkingDir, "must-gather-clean-config.json")
	logger.V(4).Info("persisting config to file", "filename", filename)
	err = os.WriteFile(filename, json, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write clean config to file: %w", err)
	}
	return filename, nil
}

func walkAndMatchRegexPatterns(ctx context.Context, configPath string, patterns []*replacement) (map[string]string, error) {
	logger := logr.FromContextOrDiscard(ctx)

	matchedReplacements := make(map[string]string)

	replacementIndex := 0

	err := filepath.Walk(configPath, func(path string, info fs.FileInfo, err error) error {
		logger.V(8).Info("walking path", "path", path)
		if err != nil {
			return fmt.Errorf("failed to walk path: %w", err)
		}
		if info.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			logger.Error(err, "failed to read file", "path", path)
			return nil
		}
		for _, replacement := range patterns {
			matches := replacement.Regex.FindAllString(string(content), -1)
			if len(matches) > 0 {
				if _, exists := matchedReplacements[matches[0]]; exists {
					continue
				}
				matchedReplacements[matches[0]] = fmt.Sprintf(replacement.ReplacementPattern, replacementIndex)
				replacementIndex++
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk path: %w", err)
	}

	return matchedReplacements, nil
}
