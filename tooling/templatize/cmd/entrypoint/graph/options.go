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
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-Tools/pipelines/graph"

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

	cmd.Flags().StringVar(&opts.OutputDotFile, "output-dot", opts.OutputDotFile, "Where the output .dot file should be written.")
	cmd.Flags().StringVar(&opts.OutputHtmlFile, "output-html", opts.OutputHtmlFile, "Where the output .html file should be written.")

	return nil
}

type RawOptions struct {
	*entrypointutils.RawOptions
	OutputDotFile  string
	OutputHtmlFile string
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

	OutputDotFile  string
	OutputHtmlFile string
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

	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "output-dot", name: "output .dot file", value: &o.OutputDotFile},
		{flag: "output-html", name: "output .html file", value: &o.OutputHtmlFile},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
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
			Options:        completed,
			OutputDotFile:  o.OutputDotFile,
			OutputHtmlFile: o.OutputHtmlFile,
		},
	}, nil
}

func (o *Options) Run(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	var title string
	var executionGraph *graph.Graph
	if o.Entrypoint != nil {
		parts := strings.Split(o.Entrypoint.Identifier, ".")
		if len(parts) < 5 {
			return fmt.Errorf("service group only had %d parts: %s", len(parts), o.Entrypoint.Identifier)
		}
		title = fmt.Sprintf("entrypoint/%s", strings.Join(parts[4:], "."))
		executionGraph, err = graph.ForEntrypoint(o.Topo, o.Entrypoint, o.Pipelines)
	} else {
		parts := strings.Split(o.Service.ServiceGroup, ".")
		if len(parts) < 5 {
			return fmt.Errorf("service group only had %d parts: %s", len(parts), o.Service.ServiceGroup)
		}
		title = fmt.Sprintf("pipeline/%s", strings.Join(parts[4:], "."))
		executionGraph, err = graph.ForPipeline(o.Service, o.Pipelines[o.Service.ServiceGroup])
	}
	if err != nil {
		return err
	}

	rawDot, err := graph.MarshalDOT(executionGraph.Nodes, executionGraph.ServiceValidationSteps)
	if err != nil {
		return fmt.Errorf("unable to marshal graph to DOT: %w", err)
	}
	if err := os.WriteFile(o.OutputDotFile, rawDot, 0644); err != nil {
		return fmt.Errorf("unable to write graph to %s: %w", o.OutputDotFile, err)
	}
	logger.Info("Created DOT visualization.", "output", o.OutputDotFile)

	if err := o.marshalHtmlGraph(logger, title, executionGraph); err != nil {
		return fmt.Errorf("unable to write graph to %s: %w", o.OutputHtmlFile, err)
	}
	return nil
}

func (o *Options) marshalHtmlGraph(logger logr.Logger, title string, executionGraph *graph.Graph) error {
	var serviceGroups []string
	for serviceGroup := range executionGraph.Services {
		serviceGroups = append(serviceGroups, serviceGroup)
	}
	slices.Sort(serviceGroups)
	shortServiceGroups := map[string]string{}
	for _, serviceGroup := range serviceGroups {
		parts := strings.Split(serviceGroup, ".")
		if len(parts) < 5 {
			return fmt.Errorf("service group only had %d parts: %s", len(parts), serviceGroup)
		}
		shortServiceGroups[serviceGroup] = strings.Join(parts[4:], ".")
	}

	g := charts.NewGraph()
	g.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle: "Step Graph",
			Renderer:  "svg",
			Height:    "1024px",
		}),
		charts.WithTitleOpts(opts.Title{
			Title:      fmt.Sprintf("%s Step Graph", title),
			TitleStyle: &opts.TextStyle{Align: "left"},
			TextAlign:  "left",
			Left:       "center",
		}),
		charts.WithGridOpts(opts.Grid{
			Top: "80",
		}),
		charts.WithLegendOpts(opts.Legend{
			Top: "30",
		}),
	)
	var nodes []opts.GraphNode
	var edges []opts.GraphLink
	var categories []*opts.GraphCategory
	for _, serviceGroup := range serviceGroups {
		categories = append(categories, &opts.GraphCategory{
			Name: shortServiceGroups[serviceGroup],
		})
		for _, node := range executionGraph.Nodes {
			if node.ServiceGroup == serviceGroup {
				id := fmt.Sprintf("%s/%s/%s", shortServiceGroups[serviceGroup], node.ResourceGroup, node.Step)
				nodes = append(nodes, opts.GraphNode{
					Name:     id,
					Category: shortServiceGroups[serviceGroup],
				})

				for _, child := range node.Children {
					var attraction float32
					if node.ServiceGroup != child.ServiceGroup {
						attraction = 1
					} else if node.ResourceGroup != child.ResourceGroup {
						attraction = 8
					} else {
						attraction = 10
					}
					edges = append(edges, opts.GraphLink{
						Source: id,
						Target: fmt.Sprintf("%s/%s/%s", shortServiceGroups[child.ServiceGroup], child.ResourceGroup, child.Step),
						Value:  attraction,
					})
				}
			}
		}

	}
	g.AddSeries("Nodes", nodes, edges, charts.WithGraphChartOpts(opts.GraphChart{
		Layout: "force",
		Force: &opts.GraphForce{
			Repulsion:  100,
			Gravity:    0.05,
			EdgeLength: []float32{10, 100},
		},
		Categories: categories,
		LineStyle: &opts.LineStyle{
			Curveness: 0.2,
		},
		EdgeSymbol:         []string{"none", "arrow"},
		EdgeSymbolSize:     4,
		FocusNodeAdjacency: ptr.To(true),
		Roam:               ptr.To(false),
		Draggable:          ptr.To(false),
	}))

	if err := os.MkdirAll(filepath.Dir(o.OutputHtmlFile), 0755); err != nil {
		return fmt.Errorf("unable to create output directory: %w", err)
	}
	output, err := os.Create(o.OutputHtmlFile)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() {
		if err := output.Close(); err != nil {
			logger.Error(err, "failed to close output file")
		}
	}()
	if err := g.Render(output); err != nil {
		return fmt.Errorf("failed to render output: %w", err)
	}

	logger.Info("Created HTML visualization.", "output", o.OutputHtmlFile)
	return nil
}
