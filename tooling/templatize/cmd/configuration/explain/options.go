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

package explain

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/config/ev2config"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		Stamp: 1,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ServiceConfigFile, "service-config-file", opts.ServiceConfigFile, "Path to the service configuration file.")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "The name of the cloud to explain in.")
	cmd.Flags().StringVar(&opts.Environment, "environment", opts.Environment, "The name of the environment to explain in.")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "The name of the region to explain in.")
	cmd.Flags().StringVar(&opts.Ev2Cloud, "ev2-cloud", opts.Ev2Cloud, "Cloud to use for Ev2 configuration, useful for dev mode explanations.")
	cmd.Flags().StringVar(&opts.RegionShortSuffix, "region-short-suffix", opts.RegionShortSuffix, "Suffix to use for region short-name, useful for dev mode explanations.")
	cmd.Flags().IntVar(&opts.Stamp, "stamp", opts.Stamp, "Stamp value to use, useful for dev mode explanations.")

	cmd.Flags().StringVar(&opts.Path, "path", opts.Path, "Path to the value needing explanation.")

	for _, flag := range []string{
		"service-config-file",
	} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

// RawOptions holds input values.
type RawOptions struct {
	ServiceConfigFile string
	Cloud             string
	Environment       string
	Region            string
	Ev2Cloud          string
	RegionShortSuffix string
	Stamp             int

	Path string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	Config            config.ConfigProvider
	Cloud             string
	Environment       string
	Region            string
	Ev2Cloud          string
	RegionShortSuffix string
	Stamp             int
	Path              string
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "service-config-file", name: "service configuration file", value: &o.ServiceConfigFile},
		{flag: "cloud", name: "cloud", value: &o.Cloud},
		{flag: "environment", name: "environment", value: &o.Environment},
		{flag: "region", name: "region", value: &o.Environment},
		{flag: "path", name: "path", value: &o.Path},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	c, err := config.NewConfigProvider(o.ServiceConfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config file: %w", err)
	}

	return &Options{
		completedOptions: &completedOptions{
			Config:            c,
			Cloud:             o.Cloud,
			Environment:       o.Environment,
			Region:            o.Region,
			Ev2Cloud:          o.Ev2Cloud,
			RegionShortSuffix: o.RegionShortSuffix,
			Stamp:             o.Stamp,
			Path:              o.Path,
		},
	}, nil
}

func (opts *Options) ExplainConfiguration(ctx context.Context) error {
	ev2Cloud := opts.Cloud
	if opts.Ev2Cloud != "" {
		ev2Cloud = opts.Ev2Cloud
	}
	ev2Cfg, err := ev2config.ResolveConfig(ev2Cloud, opts.Region)
	if err != nil {
		return fmt.Errorf("failed to get ev2 config: %w", err)
	}
	replacements := &config.ConfigReplacements{
		RegionReplacement:      opts.Region,
		CloudReplacement:       opts.Cloud,
		EnvironmentReplacement: opts.Environment,
		StampReplacement:       strconv.Itoa(opts.Stamp),
		Ev2Config:              ev2Cfg,
	}
	for key, into := range map[string]*string{
		"regionShortName": &replacements.RegionShortReplacement,
	} {
		value, err := ev2Cfg.GetByPath(key)
		if err != nil {
			return fmt.Errorf("%q not found in ev2 config: %w", key, err)
		}
		str, ok := value.(string)
		if !ok {
			return fmt.Errorf("%q is not a string", key)
		}
		*into = str
	}
	if opts.RegionShortSuffix != "" {
		replacements.RegionShortReplacement += opts.RegionShortSuffix
	}

	resolver, err := opts.Config.GetResolver(replacements)
	if err != nil {
		return fmt.Errorf("failed to get resolver: %w", err)
	}

	provenance, err := resolver.ValueProvenance(opts.Region, opts.Path)
	if err != nil {
		return fmt.Errorf("failed to get value provenance: %w", err)
	}

	if provenance.ResultSet {
		fmt.Println("Resulting Value:")
		fmt.Printf("%s: %#v\n", opts.Path, provenance.Result)
		fmt.Println()
	}
	fmt.Println("Defaults and Overrides:")
	if provenance.DefaultSet {
		fmt.Printf("cfg.defaults.%s: %#v\n", opts.Path, provenance.Default)
	}
	if provenance.CloudSet {
		fmt.Printf("cfg.clouds[%s].defaults.%s: %#v\n", opts.Cloud, opts.Path, provenance.Cloud)
	}
	if provenance.EnvironmentSet {
		fmt.Printf("cfg.clouds[%s].environments[%s].defaults.%s: %#v\n", opts.Cloud, opts.Environment, opts.Path, provenance.Environment)
	}
	if provenance.RegionSet {
		fmt.Printf("cfg.clouds[%s].environments[%s].regions[%s].%s: %#v\n", opts.Cloud, opts.Environment, opts.Region, opts.Path, provenance.Region)
	}
	return nil
}
