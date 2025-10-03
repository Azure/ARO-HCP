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

package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/Azure/ARO-Tools/pkg/types"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	rollout "github.com/Azure/ARO-HCP/tooling/templatize/cmd"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawRolloutOptions:        rollout.DefaultRolloutOptions(),
		DeploymentTimeoutSeconds: pipeline.DefaultDeploymentTimeoutSeconds,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	if err := rollout.BindRolloutOptions(opts.RawRolloutOptions, cmd); err != nil {
		return err
	}
	cmd.Flags().StringVar(&opts.TopologyFile, "topology-config", opts.TopologyFile, "Path to the topology configuration file. The directory holding this file will become the root of the Ev2 content archive.")
	cmd.Flags().StringVar(&opts.Entrypoint, "entrypoint", opts.Entrypoint, "Name of the entrypoint to create Ev2 manifests for. Exclusive with --service-group.")
	cmd.Flags().StringVar(&opts.ServiceGroup, "service-group", opts.ServiceGroup, "Name of the service group to create Ev2 manifests for. Exclusive with --entrypoint.")

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "validate the pipeline without executing it")
	cmd.Flags().BoolVar(&opts.Persist, "persist-tag", opts.Persist, "toggle if persist tag should be set")
	cmd.Flags().IntVar(&opts.DeploymentTimeoutSeconds, "deployment-timeout-seconds", opts.DeploymentTimeoutSeconds, "Timeout in Seconds to wait for previous deployments of the pipeline to finish")

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

	DryRun                   bool
	Persist                  bool
	DeploymentTimeoutSeconds int
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

	DryRun                   bool
	NoPersist                bool
	DeploymentTimeoutSeconds int
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

func (o *ValidatedOptions) Complete() (*Options, error) {
	completed, err := o.ValidatedRolloutOptions.Complete()
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

			DryRun:                   o.DryRun,
			NoPersist:                o.Persist,
			DeploymentTimeoutSeconds: o.DeploymentTimeoutSeconds,
		},
	}, nil
}

func LoadPipelines(
	root *topology.Service, topologyDir string, pipelines map[string]*types.Pipeline,
	cfg config.Configuration,
) error {
	pipelineConfigFilePath := filepath.Join(topologyDir, root.PipelinePath)
	pipe, err := types.NewPipelineFromFile(pipelineConfigFilePath, cfg)
	if err != nil {
		return fmt.Errorf("failed to precompile pipeline: %w", err)
	}
	if pipe.ServiceGroup != root.ServiceGroup {
		return fmt.Errorf("pipeline loaded from %s is for %s, not %s", pipelineConfigFilePath, pipe.ServiceGroup, root.ServiceGroup)
	}

	pipelines[root.ServiceGroup] = pipe

	for _, child := range root.Children {
		if err := LoadPipelines(&child, topologyDir, pipelines, cfg); err != nil {
			return err
		}
	}
	return nil
}

func (o *Options) Run(ctx context.Context) error {
	runOpts := &pipeline.PipelineRunOptions{
		BaseRunOptions: pipeline.BaseRunOptions{
			DryRun:                   o.DryRun,
			Cloud:                    o.Cloud,
			Configuration:            o.Config,
			NoPersist:                o.NoPersist,
			DeploymentTimeoutSeconds: o.DeploymentTimeoutSeconds,
		},
		TopologyDir:           o.TopoDir,
		Region:                o.Region,
		SubsciptionLookupFunc: pipeline.LookupSubscriptionID(o.Subscriptions),
		Concurrency:           o.Concurrency,
	}

	if o.Entrypoint != nil {
		_, err := pipeline.RunEntrypoint(o.Topo, o.Entrypoint, o.Pipelines, ctx, runOpts, pipeline.RunStep)
		return err
	}

	_, err := pipeline.RunPipeline(o.Service, o.Pipelines[o.Service.ServiceGroup], ctx, runOpts, pipeline.RunStep)
	return err
}
