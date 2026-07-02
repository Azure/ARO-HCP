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

package entrypointutils

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"

	rollout "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
)

// RegionalResourceGroupNames extracts the regional resource group names from all
// per-stamp configurations. This ensures stamped RG names (which vary per stamp)
// are included.
func RegionalResourceGroupNames(stampConfigs map[string]configtypes.Configuration) []string {
	rgPaths := []string{"regionRG", "svc.rg", "mgmt.rg"}
	names := sets.New[string]()
	for _, cfg := range stampConfigs {
		for _, path := range rgPaths {
			if rg, err := cfg.GetByPath(path); err == nil {
				if rgStr, ok := rg.(string); ok {
					names.Insert(rgStr)
				}
			}
		}
	}
	return sets.List(names)
}

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawRolloutOptions: rollout.DefaultRolloutOptions(),
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	if err := rollout.BindRolloutOptions(opts.RawRolloutOptions, cmd); err != nil {
		return err
	}
	cmd.Flags().StringArrayVar(&opts.TopologyFiles, "topology-config", opts.TopologyFiles, "Path to a topology configuration file. Can be specified multiple times.")
	cmd.Flags().StringVar(&opts.Entrypoint, "entrypoint", opts.Entrypoint, "Name of the entrypoint to create Ev2 manifests for. Exclusive with --service-group.")
	cmd.Flags().StringVar(&opts.ServiceGroup, "service-group", opts.ServiceGroup, "Name of the service group to create Ev2 manifests for. Exclusive with --entrypoint.")
	cmd.Flags().StringVar(&opts.StampCountConfigRef, "stamp-count-config-ref", opts.StampCountConfigRef, "Configuration path where the stamp count is stored (e.g. mgmt.stamps.count). Only supported with --entrypoint. When provided, stamped service groups are executed once per stamp in parallel.")

	for _, flag := range []string{"topology-config"} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

type RawOptions struct {
	*rollout.RawRolloutOptions

	TopologyFiles       []string
	Entrypoint          string
	ServiceGroup        string
	StampCountConfigRef string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
	*rollout.ValidatedRolloutOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	Topo       *topology.CombinedTopology
	Service    *topology.Service
	Entrypoint *topology.Entrypoint
	Pipelines  map[string]*types.Pipeline

	Stamps         []string
	StampConfigs   map[string]configtypes.Configuration
	StampPipelines map[string]map[string]*types.Pipeline

	*rollout.RolloutOptions
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate(ctx context.Context) (*ValidatedOptions, error) {
	if len(o.TopologyFiles) == 0 {
		return nil, fmt.Errorf("the topology configuration file must be provided with --topology-config")
	}

	if o.ServiceGroup == "" && o.Entrypoint == "" {
		return nil, fmt.Errorf("either --service-group or --entrypoint must be provided")
	}

	if o.ServiceGroup != "" && o.Entrypoint != "" {
		return nil, fmt.Errorf("invalid to provide both --service-group and --entrypoint")
	}

	if o.ServiceGroup != "" && o.StampCountConfigRef != "" {
		return nil, fmt.Errorf("--stamp-count-config-ref is only supported with --entrypoint, not --service-group")
	}

	validated, err := o.RawRolloutOptions.Validate(ctx)
	if err != nil {
		return nil, err
	}

	if validated.Stamp == "" && o.StampCountConfigRef == "" {
		return nil, fmt.Errorf("either --stamp or --stamp-count-config-ref must be provided")
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions:              o,
			ValidatedRolloutOptions: validated,
		},
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	completed, err := o.ValidatedRolloutOptions.Complete(ctx)
	if err != nil {
		return nil, err
	}

	t, err := topology.LoadCombined(o.TopologyFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to load topology: %w", err)
	}

	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate topology: %w", err)
	}

	var service *topology.Service
	if o.Entrypoint != "" {
		root, err := t.Lookup(o.Entrypoint)
		if err != nil {
			return nil, fmt.Errorf("failed to look up entrypoint %s in topology: %w", o.ServiceGroup, err)
		}
		service = root
	} else {
		svc, err := t.Lookup(o.ServiceGroup)
		if err != nil {
			return nil, fmt.Errorf("failed to look up service group %s in topology: %w", o.ServiceGroup, err)
		}
		service = svc
	}

	var e *topology.Entrypoint
	for _, option := range t.Entrypoints {
		if option.Identifier == o.Entrypoint {
			e = &option
		}
	}

	if o.Entrypoint != "" && e == nil {
		return nil, fmt.Errorf("entrypoint %s not found in topology", o.Entrypoint)
	}

	pipelines := map[string]*types.Pipeline{}
	if err := LoadPipelines(service, t, pipelines, completed.Config); err != nil {
		return nil, fmt.Errorf("failed to load pipelines: %w", err)
	}

	stamps, err := BuildStampList(completed.Stamp, o.StampCountConfigRef, completed.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to build stamp list: %w", err)
	}

	stampConfigs, err := ResolveStampConfigs(stamps, completed.Options.ConfigProvider, completed.ConfigReplacements, completed.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve stamp configs: %w", err)
	}

	stampPipelines := map[string]map[string]*types.Pipeline{}
	for stamp, cfg := range stampConfigs {
		sp := map[string]*types.Pipeline{}
		if err := LoadPipelines(service, t, sp, cfg); err != nil {
			return nil, fmt.Errorf("failed to load pipelines for stamp %s: %w", stamp, err)
		}
		stampPipelines[stamp] = sp
	}

	return &Options{
		completedOptions: &completedOptions{
			Topo:           t,
			Entrypoint:     e,
			Service:        service,
			Pipelines:      pipelines,
			Stamps:         stamps,
			StampConfigs:   stampConfigs,
			StampPipelines: stampPipelines,
			RolloutOptions: completed,
		},
	}, nil
}
