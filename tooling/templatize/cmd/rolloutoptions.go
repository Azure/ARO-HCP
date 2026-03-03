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

package options

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/ev2config"
	"github.com/Azure/ARO-Tools/config/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/settings"
)

func DefaultRolloutOptions() *RawRolloutOptions {
	return &RawRolloutOptions{
		BaseOptions:  DefaultOptions(),
		StepCacheDir: ".step-cache",
	}
}

func NewRolloutOptions(config types.Configuration) *RolloutOptions {
	return &RolloutOptions{
		completedRolloutOptions: &completedRolloutOptions{
			Config: config,
		},
	}
}

func BindRolloutOptions(opts *RawRolloutOptions, cmd *cobra.Command) error {
	err := BindOptions(opts.BaseOptions, cmd)
	if err != nil {
		return fmt.Errorf("failed to bind options: %w", err)
	}
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "resources location")
	cmd.Flags().StringVar(&opts.RegionShortSuffix, "region-short-suffix", opts.RegionShortSuffix, "suffix to add to short region name, if any")
	cmd.Flags().StringVar(&opts.Stamp, "stamp", opts.Stamp, "stamp")
	cmd.Flags().StringToStringVar(&opts.ExtraVars, "extra-args", opts.ExtraVars, "Extra arguments to be used config templating")
	cmd.Flags().StringVar(&opts.DevSettingsFile, "dev-settings-file", opts.DevSettingsFile, "File to load environment details from.")
	cmd.Flags().StringVar(&opts.DevEnvironment, "dev-environment", opts.DevEnvironment, "Name of the developer environment to use.")
	cmd.Flags().StringVar(&opts.StepCacheDir, "step-cache-dir", opts.StepCacheDir, "Directory where cached step outputs will be stored.")
	cmd.Flags().IntVar(&opts.Concurrency, "concurrency", opts.Concurrency, "Number of concurrent routines to use when running the pipeline. If unset/set to 0, unbounded concurrency is used.")

	for _, flag := range []string{
		"dev-settings-file",
	} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

// RawRolloutOptions holds input values.
type RawRolloutOptions struct {
	Region              string
	RegionShortOverride string
	RegionShortSuffix   string
	Stamp               string
	ExtraVars           map[string]string
	BaseOptions         *RawOptions

	DevSettingsFile string
	DevEnvironment  string

	StepCacheDir string

	Concurrency int
}

// validatedRolloutOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedRolloutOptions struct {
	*RawRolloutOptions
	*ValidatedOptions

	Subscriptions map[string]string
}

type ValidatedRolloutOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedRolloutOptions
}

type completedRolloutOptions struct {
	*ValidatedRolloutOptions
	Options       *Options
	Config        types.Configuration
	Subscriptions map[string]string
	StepCacheDir  string

	BicepClient *bicep.LSPClient
}

type RolloutOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedRolloutOptions
}

func (o *RawRolloutOptions) Validate(ctx context.Context) (*ValidatedRolloutOptions, error) {
	if o.DevEnvironment != "" && o.DevSettingsFile == "" {
		return nil, fmt.Errorf("developer environment %s chosen, but not --dev-settings-file provided", o.DevEnvironment)
	}
	var subscriptions map[string]string
	if o.DevEnvironment != "" && o.DevSettingsFile != "" {
		for name, value := range map[string]string{
			"environment":       o.BaseOptions.DeployEnv,
			"region short-name": o.RegionShortSuffix,
			"stamp":             o.Stamp,
		} {
			if value != "" {
				return nil, fmt.Errorf("%s cannot be provided explicitly when using settings file", name)
			}
		}

		if o.BaseOptions.Cloud == "" {
			return nil, fmt.Errorf("provide the cloud for dev environment %s with --cloud", o.DevEnvironment)
		}

		if o.RegionShortOverride != "" && o.RegionShortSuffix != "" {
			return nil, fmt.Errorf("regionShortOverride and regionShortSuffix cannot be provided together")
		}

		devSettings, err := settings.Load(o.DevSettingsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load developer settings: %w", err)
		}

		env, err := devSettings.Resolve(ctx, o.BaseOptions.Cloud, o.DevEnvironment)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve developer environment %s: %w", o.DevEnvironment, err)
		}
		region := env.Region
		if o.Region != "" {
			region = o.Region
		}
		o.BaseOptions.Cloud = env.Cloud
		o.BaseOptions.Ev2Cloud = env.Ev2Cloud
		o.BaseOptions.DeployEnv = env.Environment
		o.Region = region
		o.RegionShortSuffix = env.RegionShortSuffix
		o.RegionShortOverride = env.RegionShortOverride
		o.Stamp = strconv.Itoa(env.Stamp)
		subscriptions = devSettings.Subscriptions
	}

	validatedBaseOptions, err := o.BaseOptions.Validate()
	if err != nil {
		return nil, err
	}

	return &ValidatedRolloutOptions{
		validatedRolloutOptions: &validatedRolloutOptions{
			RawRolloutOptions: o,
			ValidatedOptions:  validatedBaseOptions,
			Subscriptions:     subscriptions,
		},
	}, nil
}

func (o *ValidatedRolloutOptions) Complete(ctx context.Context) (*RolloutOptions, error) {
	completed, err := o.ValidatedOptions.Complete()
	if err != nil {
		return nil, err
	}

	if o.Ev2Cloud == "" {
		o.Ev2Cloud = o.Cloud
	}
	ev2Cfg, err := ev2config.ResolveConfig(o.Ev2Cloud, o.Region)
	if err != nil {
		return nil, fmt.Errorf("error loading embedded ev2 config: %v", err)
	}

	rawRegionShort, err := ev2Cfg.GetByPath("regionShortName")
	if err != nil {
		return nil, fmt.Errorf("regionShortName not found for ev2Config[%s][%s]: %w", o.Ev2Cloud, o.Region, err)
	}
	regionShort, isString := rawRegionShort.(string)
	if !isString {
		return nil, fmt.Errorf("regionShortName is %T, not string for ev2Config[%s][%s]", rawRegionShort, o.Ev2Cloud, o.Region)
	}
	if o.RegionShortOverride != "" {
		regionShort = o.RegionShortOverride
	}

	resolver, err := completed.ConfigProvider.GetResolver(&config.ConfigReplacements{
		RegionReplacement:      o.Region,
		RegionShortReplacement: regionShort + o.RegionShortSuffix,
		StampReplacement:       o.Stamp,
		CloudReplacement:       o.Cloud,
		EnvironmentReplacement: o.DeployEnv,
		Ev2Config:              ev2Cfg,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get config resolver: %w", err)
	}
	variables, err := resolver.GetRegionConfiguration(o.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to get variables: %w", err)
	}
	if err := resolver.ValidateSchema(variables); err != nil {
		return nil, fmt.Errorf("failed to validate region configuration: %w", err)
	}
	extraVars := make(map[string]interface{})
	for k, v := range o.ExtraVars {
		extraVars[k] = v
	}
	variables["extraVars"] = extraVars

	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, err
	}

	bicepClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		return nil, err
	}

	return &RolloutOptions{
		completedRolloutOptions: &completedRolloutOptions{
			ValidatedRolloutOptions: o,
			Options:                 completed,
			Config:                  variables,
			Subscriptions:           o.Subscriptions,
			StepCacheDir:            o.StepCacheDir,
			BicepClient:             bicepClient,
		},
	}, nil
}
