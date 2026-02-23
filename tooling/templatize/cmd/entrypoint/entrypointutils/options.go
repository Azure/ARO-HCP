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
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"sigs.k8s.io/yaml"

<<<<<<< HEAD
	configtypes "github.com/Azure/ARO-Tools/pkg/config/types"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/Azure/ARO-Tools/pkg/types"
=======
	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"
>>>>>>> 9d6ea99b5 (*: update to ARO-Tools modules)

	rollout "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
)

// RegionalResourceGroupNames extracts the regional resource group names from the configuration.
// These are the RGs that correspond to region-scoped infrastructure (as opposed to global resources).
func RegionalResourceGroupNames(cfg configtypes.Configuration) []string {
	rgPaths := []string{"regionRG", "svc.rg", "mgmt.rg"}
	var names []string
	for _, path := range rgPaths {
		if rg, err := cfg.GetByPath(path); err == nil {
			if rgStr, ok := rg.(string); ok {
				names = append(names, rgStr)
			}
		}
	}
	return names
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
	cmd.Flags().StringVar(&opts.TopologyFile, "topology-config", opts.TopologyFile, "Path to the topology configuration file. The directory holding this file will become the root of the Ev2 content archive.")
	cmd.Flags().StringVar(&opts.Entrypoint, "entrypoint", opts.Entrypoint, "Name of the entrypoint to create Ev2 manifests for. Exclusive with --service-group.")
	cmd.Flags().StringVar(&opts.ServiceGroup, "service-group", opts.ServiceGroup, "Name of the service group to create Ev2 manifests for. Exclusive with --entrypoint.")

	for _, flag := range []string{"topology-config"} {
		if err := cmd.MarkFlagFilename(flag); err != nil {
			return fmt.Errorf("failed to mark flag %q as a file: %w", flag, err)
		}
	}
	return nil
}

type RawOptions struct {
	*rollout.RawRolloutOptions

	TopologyFile string
	Entrypoint   string
	ServiceGroup string
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
	Topo       *topology.Topology
	TopoDir    string
	Service    *topology.Service
	Entrypoint *topology.Entrypoint
	Pipelines  map[string]*types.Pipeline

	*rollout.RolloutOptions
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate(ctx context.Context) (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "topology-config", name: "topology configuration file", value: &o.TopologyFile},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	if o.ServiceGroup == "" && o.Entrypoint == "" {
		return nil, fmt.Errorf("either --service-group or --entrypoint must be provided")
	}

	if o.ServiceGroup != "" && o.Entrypoint != "" {
		return nil, fmt.Errorf("invalid to provide both --service-group and --entrypoint")
	}

	validated, err := o.RawRolloutOptions.Validate(ctx)
	if err != nil {
		return nil, err
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

	rawTopology, err := os.ReadFile(o.TopologyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read input file %s: %w", o.TopologyFile, err)
	}

	var t topology.Topology
	if err := yaml.Unmarshal(rawTopology, &t); err != nil {
		return nil, fmt.Errorf("failed to unmarshal topology: %w", err)
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
	if err := LoadPipelines(service, filepath.Dir(o.TopologyFile), pipelines, completed.Config); err != nil {
		return nil, fmt.Errorf("failed to load pipelines from %s: %w", o.TopologyFile, err)
	}

	return &Options{
		completedOptions: &completedOptions{
			Topo:       &t,
			TopoDir:    filepath.Dir(o.TopologyFile),
			Entrypoint: e,
			Service:    service,
			Pipelines:  pipelines,

			RolloutOptions: completed,
		},
	}, nil
}
