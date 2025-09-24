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
	"html/template"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	_ "embed"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/config/ev2config"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

//go:embed config.tmpl
var mustGatherTemplate []byte

type redactionConfig map[string][]any

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
	if os.Getenv("DEBUG") == "true" {
		logrus.SetLevel(logrus.DebugLevel)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := &cobra.Command{
		Use:              "log-redaction",
		Short:            "Extract sensitive values from config for log redaction",
		Long:             "Reads redaction keys from config.redaction.yaml and extracts their actual values from the main config file",
		SilenceUsage:     true,
		TraverseChildren: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cmd.SetContext(ctx)
		},
	}

	opts := &Options{
		Stamp: 1,
	}

	cmd.Flags().StringVar(&opts.ServiceConfigFile, "service-config-file", "config/config.yaml", "Path to the service configuration file.")
	cmd.Flags().StringVar(&opts.RedactionConfigFile, "redaction-config-file", "config/config.redaction.yaml", "Path to the redaction configuration file.")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", "dev", "The name of the cloud.")
	cmd.Flags().StringVar(&opts.Environment, "environment", "dev", "The name of the environment.")
	cmd.Flags().StringVar(&opts.Region, "region", "eastus2", "The name of the region.")
	cmd.Flags().IntVar(&opts.Stamp, "stamp", opts.Stamp, "Stamp value to use.")
	cmd.Flags().StringVar(&opts.Ev2Cloud, "ev2-cloud", "public", "Cloud to use for Ev2 configuration.")
	cmd.Flags().StringVar(&opts.RegionShortSuffix, "region-short-suffix", "", "Suffix to use for region short-name.")
	cmd.Flags().BoolVar(&opts.Debug, "debug", false, "Enable debug output to see the resolved config structure.")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runLogRedaction(cmd.Context(), opts)
	}

	if err := cmd.Execute(); err != nil {
		logrus.Error("command failed: %w", err)
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

	ev2Config, err := ev2config.ResolveConfig(opts.Ev2Cloud, opts.Region)
	if err != nil {
		return fmt.Errorf("failed to resolve ev2 config: %w", err)
	}

	replacements := &config.ConfigReplacements{
		RegionReplacement:      opts.Region,
		CloudReplacement:       opts.Cloud,
		EnvironmentReplacement: opts.Environment,
		StampReplacement:       strconv.Itoa(opts.Stamp),
		RegionShortReplacement: opts.Region,
		Ev2Config:              ev2Config,
	}

	resolver, err := c.GetResolver(replacements)

	if err != nil {
		return fmt.Errorf("failed to get resolver, %w", err)
	}

	cfg, err := resolver.GetConfiguration()
	if err != nil {
		return fmt.Errorf("Error resolving config, %w", err)
	}

	redactionKeys, err := resolveRedactionKeys(cfg, redactionConfig)
	if err != nil {
		return fmt.Errorf("failed to resolve redaction keys: %w", err)
	}

	redactionConfigTemplate, err := template.New("redactionConfig").Parse(string(mustGatherTemplate))
	if err != nil {
		return fmt.Errorf("failed to parse redaction config template: %w", err)
	}

	redactionConfigTemplate.Execute(os.Stdout, map[string]interface{}{
		"redactionKeys": redactionKeys,
	})

	return nil
}

func resolveRedactionKeys(cfg config.Configuration, redactionConfig redactionConfig) ([]string, error) {
	redactionKeys := make([]string, 0)
	for _, redactionKey := range redactionConfig["keys"] {
		var err error
		var configuredValue any

		switch v := redactionKey.(type) {
		case string:
			configuredValue, err = cfg.GetByPath(v)
		case map[any]any:
			for key := range v {
				configuredValue, err = cfg.GetByPath(key.(string))
			}
		}
		if err != nil {
			logrus.Warnf("failed to get redaction key: %w", err)
			continue
		}

		switch v := configuredValue.(type) {
		case string:
			if v != "" {
				redactionKeys = append(redactionKeys, v)
			}
		case []map[string]any:
			for _, item := range v {
				for _, nestedKeysToRedact := range redactionKey.(map[any]any) {
					for _, x := range nestedKeysToRedact.([]any) {
						for key := range x.(map[any]any) {
							redactedKey := item[key.(string)].(string)
							if redactedKey != "" {
								redactionKeys = append(redactionKeys, redactedKey)
							}
						}
					}
				}
			}
		case []string:
			for _, item := range v {
				if item != "" {
					redactionKeys = append(redactionKeys, item)
				}
			}
		}

	}
	return redactionKeys, nil
}

func loadRedactionConfig(filepath string) (redactionConfig, error) {
	redactionCfg := make(redactionConfig)

	contentBytes, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read redaction config file: %w", err)
	}

	if err := yaml.Unmarshal(contentBytes, &redactionCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal redaction config file: %w", err)
	}

	return redactionCfg, nil
}
