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

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/config/ev2config"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

type RedactionConfig struct {
	Keys []string `yaml:"keys"`
}

type Options struct {
	ServiceConfigFile   string
	RedactionConfigFile string
	Cloud               string
	Environment         string
	Region              string
	Stamp               int
	Ev2Cloud            string
	RegionShortSuffix   string
	Debug               bool
}

func main() {
	logger := createLogger(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var logVerbosity int

	cmd := &cobra.Command{
		Use:              "log-redaction",
		Short:            "Extract sensitive values from config for log redaction",
		Long:             "Reads redaction keys from config.redaction.yaml and extracts their actual values from the main config file",
		SilenceUsage:     true,
		TraverseChildren: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ctx = logr.NewContext(ctx, createLogger(logVerbosity))
			cmd.SetContext(ctx)
		},
	}

	cmd.PersistentFlags().IntVarP(&logVerbosity, "verbosity", "v", 0, "set the verbosity level")

	opts := &Options{
		Stamp: 1,
	}

	cmd.Flags().StringVar(&opts.ServiceConfigFile, "service-config-file", "config/config.yaml", "Path to the service configuration file.")
	cmd.Flags().StringVar(&opts.RedactionConfigFile, "redaction-config-file", "config/config.redaction.yaml", "Path to the redaction configuration file.")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", "dev", "The name of the cloud.")
	cmd.Flags().StringVar(&opts.Environment, "environment", "pers", "The name of the environment.")
	cmd.Flags().StringVar(&opts.Region, "region", "eastus", "The name of the region.")
	cmd.Flags().IntVar(&opts.Stamp, "stamp", opts.Stamp, "Stamp value to use.")
	cmd.Flags().StringVar(&opts.Ev2Cloud, "ev2-cloud", "", "Cloud to use for Ev2 configuration.")
	cmd.Flags().StringVar(&opts.RegionShortSuffix, "region-short-suffix", "", "Suffix to use for region short-name.")
	cmd.Flags().BoolVar(&opts.Debug, "debug", false, "Enable debug output to see the resolved config structure.")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runLogRedaction(cmd.Context(), opts)
	}

	if err := cmd.Execute(); err != nil {
		logger.Error(err, "command failed")
		os.Exit(1)
	}
}

func runLogRedaction(ctx context.Context, opts *Options) error {
	redactionConfig, err := loadRedactionConfig(opts.RedactionConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load redaction config: %w", err)
	}

	c, err := config.NewConfigProvider(opts.ServiceConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load config file: %w", err)
	}

	replacements := &config.ConfigReplacements{
		RegionReplacement:        opts.Region,
		CloudReplacement:         opts.Cloud,
		EnvironmentReplacement:   opts.Environment,
		StampReplacement:         strconv.Itoa(opts.Stamp),
		RegionShortReplacement:   opts.Region,
	}

	ev2Cloud := opts.Cloud
	if opts.Ev2Cloud != "" {
		ev2Cloud = opts.Ev2Cloud
	}

	ev2Cfg, err := ev2config.ResolveConfig(ev2Cloud, opts.Region)
	if err == nil {
		replacements.Ev2Config = ev2Cfg

		value, err := ev2Cfg.GetByPath("regionShortName")
		if err == nil {
			if regionShort, ok := value.(string); ok {
				replacements.RegionShortReplacement = regionShort
				if opts.RegionShortSuffix != "" {
					replacements.RegionShortReplacement += opts.RegionShortSuffix
				}
			}
		}
	} else {
		// Create a minimal EV2 config for template resolution
		ev2ConfigData := map[string]interface{}{
			"cloudName": opts.Cloud,
			"regionFriendlyName": opts.Region,
			"regionShortName": opts.Region,
			"keyVault": map[string]interface{}{
				"domainNameSuffix": "vault.azure.net",
			},
		}
		replacements.Ev2Config = ev2ConfigData
	}

	resolver, err := c.GetResolver(replacements)
	if err != nil {
		return fmt.Errorf("failed to get resolver: %w", err)
	}

	cfg, err := resolver.GetRegionConfiguration(opts.Region)
	if err != nil {
		return fmt.Errorf("failed to get region config: %w", err)
	}

	if err := resolver.ValidateSchema(cfg); err != nil {
		return fmt.Errorf("resolved region config was invalid: %w", err)
	}

	// Convert to map[string]interface{} for easier traversal
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal configuration: %w", err)
	}

	var configMap map[string]interface{}
	if err := yaml.Unmarshal(encoded, &configMap); err != nil {
		return fmt.Errorf("failed to unmarshal configuration: %w", err)
	}

	if opts.Debug {
		fmt.Printf("DEBUG: Resolved config structure:\n%s\n", string(encoded))
		fmt.Printf("DEBUG: cfg type: %T\n", cfg)
	}

	for _, key := range redactionConfig.Keys {
		value, err := getValueByPath(configMap, key)
		if err != nil {
			fmt.Printf("Warning: could not find key %s: %v\n", key, err)
			continue
		}
		if value != nil && value != "" {
			fmt.Printf("%s=%v\n", key, value)
		}
	}

	return nil
}

func loadRedactionConfig(filename string) (*RedactionConfig, error) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var config RedactionConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return &config, nil
}

func getValueByPath(data interface{}, path string) (interface{}, error) {
	parts := strings.Split(path, ".")
	current := data

	for i, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			if strings.HasSuffix(part, "[]") {
				key := strings.TrimSuffix(part, "[]")
				if arr, ok := v[key].([]interface{}); ok {
					var values []interface{}
					for _, item := range arr {
						remaining := strings.Join(parts[i+1:], ".")
						if remaining == "" {
							values = append(values, item)
						} else {
							val, err := getValueByPath(item, remaining)
							if err == nil && val != nil {
								values = append(values, val)
							}
						}
					}
					return values, nil
				}
				return nil, fmt.Errorf("key %s is not an array", key)
			}
			if val, ok := v[part]; ok {
				current = val
			} else {
				return nil, fmt.Errorf("key %s not found", part)
			}
		case map[interface{}]interface{}:
			if val, ok := v[part]; ok {
				current = val
			} else {
				return nil, fmt.Errorf("key %s not found", part)
			}
		default:
			if i == 0 {
				// If we're at the root and it's not a map, we can't traverse further
				return nil, fmt.Errorf("cannot traverse path %s: root is not a map", path)
			}
			return nil, fmt.Errorf("cannot traverse path %s at part %s: not a map", path, part)
		}
	}

	return current, nil
}

func createLogger(verbosity int) logr.Logger {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     slog.Level(verbosity * -1),
		AddSource: false,
	})
	return logr.FromSlogHandler(handler)
}
