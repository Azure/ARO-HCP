// Copyright 2026 Microsoft Corporation
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

package stackscopes

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/ev2config"
	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/topology"
)

func NewCommand() (*cobra.Command, error) {
	opts := &RawOptions{}
	cmd := &cobra.Command{
		Use:   "validate-stack-scopes",
		Short: "Fail when more than one region or stamp of an environment would deploy the same ARMStack deployment stack.",
		Long: "For every ARMStack step, render the subscription and resource group for every region and stamp the step executes in, " +
			"honoring executionConstraints, and fail if two executions of the same cloud and environment land in the same resource group. " +
			"Deployment stack names do not include the region or stamp, so such executions would share one stack and prune each other's resources.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			validated, err := opts.Validate()
			if err != nil {
				return err
			}
			completed, err := validated.Complete()
			if err != nil {
				return err
			}
			if err := completed.Run(cmd.Context()); err != nil {
				lines := strings.Split(err.Error(), "\n")
				for _, line := range lines[1:] {
					fmt.Println(line)
				}
				return errors.New(lines[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.ServiceConfigFile, "service-config-file", opts.ServiceConfigFile, "Path to the service configuration file.")
	cmd.Flags().StringVar(&opts.ConfigFileOverride, "config-file-override", opts.ConfigFileOverride, "Path to a configuration overlay merged on top of the service configuration, e.g. to add the int/stg/prod environments.")
	cmd.Flags().StringArrayVar(&opts.TopologyFiles, "topology-config-file", opts.TopologyFiles, "Path to a topology configuration file. Can be specified multiple times.")
	for _, flag := range []string{"service-config-file", "config-file-override", "topology-config-file"} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return nil, fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return cmd, nil
}

// RawOptions holds input values.
type RawOptions struct {
	ServiceConfigFile  string
	ConfigFileOverride string
	TopologyFiles      []string
}

type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	Topology *topology.CombinedTopology
	Config   config.ConfigProvider
}

type Options struct {
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.ServiceConfigFile == "" {
		return nil, errors.New("the service configuration file must be provided with --service-config-file")
	}
	if len(o.TopologyFiles) == 0 {
		return nil, errors.New("the topology configuration file must be provided with --topology-config-file")
	}
	return &ValidatedOptions{validatedOptions: &validatedOptions{RawOptions: o}}, nil
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	t, err := topology.LoadCombined(o.TopologyFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to load topology: %w", err)
	}
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate topology: %w", err)
	}

	var c config.ConfigProvider
	if o.ConfigFileOverride != "" {
		schemaBaseDir := filepath.Dir(o.ServiceConfigFile)
		merged, err := configtypes.MergeRawConfigurationFiles(schemaBaseDir, []string{o.ServiceConfigFile, o.ConfigFileOverride})
		if err != nil {
			return nil, fmt.Errorf("failed to merge configuration files: %w", err)
		}
		if c, err = config.NewConfigProviderFromData(merged, schemaBaseDir); err != nil {
			return nil, fmt.Errorf("failed to load merged configuration: %w", err)
		}
	} else if c, err = config.NewConfigProvider(o.ServiceConfigFile); err != nil {
		return nil, fmt.Errorf("failed to load config file: %w", err)
	}

	return &Options{completedOptions: &completedOptions{Topology: t, Config: c}}, nil
}

func (o *Options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	pipelines, err := collectPipelines(o.Topology)
	if err != nil {
		return err
	}
	ev2Regions, err := ev2config.AllContexts()
	if err != nil {
		return fmt.Errorf("failed to load ev2 contexts: %w", err)
	}

	var collisions []collision
	for cloud, environments := range o.Config.AllContexts() {
		regions := append([]string(nil), ev2Regions[cloud]...)
		if len(regions) == 0 {
			logger.V(3).Info("Skipping cloud without known regions.", "cloud", cloud)
			continue
		}
		sort.Strings(regions)
		for environment := range environments {
			logger.V(3).Info("Checking deployment stack scopes.", "cloud", cloud, "environment", environment)
			found, err := findCollisions(cloud, environment, regions, pipelines, o.renderer(cloud, environment))
			if err != nil {
				return err
			}
			collisions = append(collisions, found...)
		}
	}
	if len(collisions) == 0 {
		return nil
	}

	sort.Slice(collisions, func(i, j int) bool { return collisions[i].String() < collisions[j].String() })
	errs := make([]error, 0, len(collisions))
	for _, c := range collisions {
		errs = append(errs, errors.New(c.String()))
	}
	return fmt.Errorf("found %d ARMStack deployment stack scope collision(s):\n%w", len(collisions), errors.Join(errs...))
}

// renderer resolves the configuration for one region and stamp, the same way `pipeline validate` does.
func (o *Options) renderer(cloud, environment string) renderFunc {
	return func(region string, stamp int) (configtypes.Configuration, error) {
		ev2Cfg, err := ev2config.ResolveConfig(cloud, region)
		if err != nil {
			return nil, fmt.Errorf("failed to get ev2 config: %w", err)
		}
		shortName, err := ev2Cfg.GetByPath("regionShortName")
		if err != nil {
			return nil, fmt.Errorf("regionShortName not found in ev2 config: %w", err)
		}
		short, ok := shortName.(string)
		if !ok {
			return nil, fmt.Errorf("regionShortName is %T, not a string", shortName)
		}
		resolver, err := o.Config.GetResolver(&config.ConfigReplacements{
			RegionReplacement:      region,
			RegionShortReplacement: short,
			CloudReplacement:       cloud,
			EnvironmentReplacement: environment,
			StampReplacement:       strconv.Itoa(stamp),
			Ev2Config:              ev2Cfg,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get resolver: %w", err)
		}
		return resolver.GetRegionConfiguration(region)
	}
}

func collectPipelines(t *topology.CombinedTopology) ([]servicePipeline, error) {
	var pipelines []servicePipeline
	var walk func(service topology.Service) error
	walk = func(service topology.Service) error {
		dir, err := t.GetTopologyDirForServiceGroup(service.ServiceGroup)
		if err != nil {
			return fmt.Errorf("failed to get topology dir for %s: %w", service.ServiceGroup, err)
		}
		pipelines = append(pipelines, servicePipeline{ServiceGroup: service.ServiceGroup, Path: filepath.Join(dir, service.PipelinePath)})
		for _, child := range service.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, service := range t.Services {
		if err := walk(service); err != nil {
			return nil, err
		}
	}
	return pipelines, nil
}
