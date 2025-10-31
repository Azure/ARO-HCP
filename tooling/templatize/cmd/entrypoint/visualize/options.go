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

package visualize

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/go-echarts/v2/render"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/utils/ptr"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.TimingInputFile, "timing-input", opts.TimingInputFile, "Path to the file holding timing outputs from an entrypoint run.")
	cmd.Flags().StringVar(&opts.OutputFile, "output", opts.OutputFile, "Path to the file where visualizations will be written.")

	return nil
}

type RawOptions struct {
	TimingInputFile string
	OutputFile      string
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
	Times      []NodeInfo
	OutputFile string
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
		{flag: "timing-input", name: "timing input file", value: &o.TimingInputFile},
		{flag: "output", name: "output file", value: &o.OutputFile},
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
	rawTiming, err := os.ReadFile(o.TimingInputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read timing input file: %w", err)
	}

	var rawTimes []pipeline.NodeInfo
	if err := yaml.Unmarshal(rawTiming, &rawTimes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal timing input file: %w", err)
	}

	var times []NodeInfo
	for _, item := range rawTimes {
		q, err := time.Parse(time.RFC3339Nano, item.Info.QueuedAt)
		if err != nil {
			return nil, fmt.Errorf("%s: failed to parse queue date: %w", item.Identifier, err)
		}
		s, err := time.Parse(time.RFC3339Nano, item.Info.StartedAt)
		if err != nil {
			return nil, fmt.Errorf("%s: failed to parse start date: %w", item.Identifier, err)
		}
		f, err := time.Parse(time.RFC3339Nano, item.Info.FinishedAt)
		if err != nil {
			return nil, fmt.Errorf("%s: failed to parse finish date: %w", item.Identifier, err)
		}
		times = append(times, NodeInfo{
			Identifier: item.Identifier,
			Info: ExecutionInfo{
				QueuedAt:   q,
				StartedAt:  s,
				FinishedAt: f,
			},
		})
	}

	return &Options{
		completedOptions: &completedOptions{
			Times:      times,
			OutputFile: o.OutputFile,
		},
	}, nil
}

type NodeInfo struct {
	Identifier pipeline.Identifier
	Info       ExecutionInfo
}

type ExecutionInfo struct {
	QueuedAt   time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

func (o *Options) Visualize(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	slices.SortFunc(o.Times, func(a, b NodeInfo) int {
		t := a.Info.QueuedAt.Compare(b.Info.QueuedAt)
		if t != 0 {
			return t
		}
		return strings.Compare(a.Identifier.String(), b.Identifier.String())
	})

	startTime := o.Times[0].Info.QueuedAt

	var names []string
	var highWaterMark []opts.BarData
	var bars []opts.BarData
	for _, item := range o.Times {
		if item.Info.FinishedAt.Sub(item.Info.StartedAt) < time.Second {
			continue
		}

		names = append(names, item.Identifier.String())
		highWaterMark = append(highWaterMark, opts.BarData{
			Value: item.Info.StartedAt.Sub(startTime).Seconds(),
			Tooltip: &opts.Tooltip{
				Show: ptr.To(false),
			},
		})
		bars = append(bars, opts.BarData{
			Value: item.Info.FinishedAt.Sub(item.Info.StartedAt).Seconds(),
		})
	}

	waterfall := charts.NewBar()
	waterfall.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle: "Timing Analysis",
			Renderer:  "svg",
			Height:    "1024px",
		}),
		charts.WithTitleOpts(opts.Title{
			Title:      "ARO HCP entrypoint/Region Deployment Timing",
			TitleStyle: &opts.TextStyle{Align: "left"},
			TextAlign:  "left",
			Left:       "center",
		}),
		charts.WithTooltipOpts(opts.Tooltip{
			Trigger: "axis",
			AxisPointer: &opts.AxisPointer{
				Type: "shadow",
			},
		}),
		charts.WithLegendOpts(opts.Legend{Show: ptr.To(false)}),
		charts.WithYAxisOpts(opts.YAxis{Show: ptr.To(false)}),
		charts.WithXAxisOpts(opts.XAxis{AxisLabel: &opts.AxisLabel{Rotate: -22.5}}),
	)
	waterfall.AddJSFuncStrs(
		opts.FuncOpts(render.EchartsInstancePlaceholder+`.setOption({"xAxis": {"axisLabel": {"formatter": `+axisFormatter(startTime)+`}}});`),
		opts.FuncOpts(render.EchartsInstancePlaceholder+`.setOption({"tooltip": {"formatter": `+valueFormatter+`}});`),
	)
	waterfall.SetXAxis(names).
		AddSeries("Placeholder", highWaterMark,
			charts.WithBarChartOpts(opts.BarChart{
				Stack: "total",
			}),
			charts.WithItemStyleOpts(opts.ItemStyle{
				BorderColor: "transparent",
				Color:       "transparent",
			})).
		AddSeries("Step Timing", bars,
			charts.WithBarChartOpts(opts.BarChart{
				Stack: "total",
			})).
		XYReversal()

	output, err := os.Create(o.OutputFile)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() {
		if err := output.Close(); err != nil {
			logger.Error(err, "failed to close output file")
		}
	}()
	if err := waterfall.Render(output); err != nil {
		return fmt.Errorf("failed to render output: %w", err)
	}

	logger.Info("Created visualization.", "output", o.OutputFile)
	return nil
}

const valueFormatter = `(params) => {
	const totalSeconds = params[1].value;
    
	const hours = Math.floor(totalSeconds / (60 * 60));
    const minutes = Math.floor((totalSeconds % (60 * 60)) / (60));
    const seconds = Math.floor(totalSeconds % (60));

    let result = '';
    if (hours > 0) {
        result += ` + "`${hours}h`" + `;
    }
    if (minutes > 0 || hours > 0) {
        result += ` + "`${minutes}m`" + `;
    }
    result += ` + "`${seconds}s`" + `;
    return params[1].name + ": " + result;
}`

func axisFormatter(base time.Time) string {
	return `(value, index) => {
    const baseDate = new Date('` + base.Format(time.RFC3339) + `');
    const resultDate = new Date(baseDate.getTime() + value * 1000);
	const options = {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        timeZoneName: 'short'
    };
    return resultDate.toLocaleTimeString('en-US', options);
}`
}
