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

package graph

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-Tools/pkg/graph"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/entrypoint/entrypointutils"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		RawOptions: entrypointutils.DefaultOptions(),
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	if err := entrypointutils.BindOptions(opts.RawOptions, cmd); err != nil {
		return err
	}

	return nil
}

type RawOptions struct {
	*entrypointutils.RawOptions
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
	*entrypointutils.ValidatedOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	*entrypointutils.Options
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate(ctx context.Context) (*ValidatedOptions, error) {
	validated, err := o.RawOptions.Validate(ctx)
	if err != nil {
		return nil, err
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions:       o,
			ValidatedOptions: validated,
		},
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	completed, err := o.ValidatedOptions.Complete(ctx)
	if err != nil {
		return nil, err
	}

	return &Options{
		completedOptions: &completedOptions{
			Options: completed,
		},
	}, nil
}

func (o *Options) Run(ctx context.Context) error {
	var executionGraph *graph.Graph
	var err error
	if o.Entrypoint != nil {
		executionGraph, err = graph.ForEntrypoint(o.Topo, o.Entrypoint, o.Pipelines)
	} else {
		executionGraph, err = graph.ForPipeline(o.Service, o.Pipelines[o.Service.ServiceGroup])
	}
	if err != nil {
		return err
	}

	raw, err := graph.MarshalDOT(executionGraph.Nodes, executionGraph.ServiceValidationSteps)
	if err != nil {
		return fmt.Errorf("unable to marshal graph to DOT: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}
