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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-echarts/go-echarts/v2/render"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.TimingInputDir, "timing-input", opts.TimingInputDir, "Path to the directory holding timing outputs from an end-to-end test run.")
	cmd.Flags().StringVar(&opts.OutputDir, "output", opts.OutputDir, "Path to the directory where visualizations will be written.")

	return nil
}

type RawOptions struct {
	TimingInputDir string
	OutputDir      string
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
	Times     []TestInfo
	OutputDir string
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
		{flag: "timing-input", name: "timing input dir", value: &o.TimingInputDir},
		{flag: "output", name: "output dir", value: &o.OutputDir},
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

func (o *ValidatedOptions) Complete(logger logr.Logger) (*Options, error) {
	timingPathMatcher, err := regexp.Compile(`timing-metadata-[a-z0-9]+.yaml`)
	if err != nil {
		return nil, fmt.Errorf("failed to compile timing file regexp: %w", err)
	}

	var rawTimes []timing.SpecTimingMetadata
	if err := fs.WalkDir(os.DirFS(o.TimingInputDir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !timingPathMatcher.MatchString(d.Name()) {
			return nil
		}
		file := filepath.Join(o.TimingInputDir, path)
		logger.Info("Reading input file", "path", file)
		rawTiming, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read timing input file: %w", err)
		}

		var rawTime timing.SpecTimingMetadata
		if err := yaml.Unmarshal(rawTiming, &rawTime); err != nil {
			return fmt.Errorf("failed to unmarshal timing input file: %w", err)
		}
		rawTimes = append(rawTimes, rawTime)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk timing input directory: %w", err)
	}

	var times []TestInfo
	for _, item := range rawTimes {
		s, err := time.Parse(time.RFC3339Nano, item.StartedAt)
		if err != nil {
			logger.Error(err, "failed to parse start date", "identifier", item.Identifier)
			continue
		}
		f, err := time.Parse(time.RFC3339Nano, item.FinishedAt)
		if err != nil {
			logger.Error(err, "failed to parse finish date", "identifier", item.Identifier)
			continue
		}
		var steps []StepTimingMetadata
		for _, step := range item.Steps {
			stepStarted, err := time.Parse(time.RFC3339Nano, step.StartedAt)
			if err != nil {
				logger.Error(err, "failed to parse start date", "identifier", step.Name)
				continue
			}
			stepFinished, err := time.Parse(time.RFC3339Nano, step.FinishedAt)
			if err != nil {
				logger.Error(err, "failed to parse finish date", "identifier", step.Name)
				continue
			}
			steps = append(steps, StepTimingMetadata{
				Name:       step.Name,
				StartedAt:  stepStarted,
				FinishedAt: stepFinished,
			})
		}
		deployments := make(map[string]map[string][]ARMOperation)
		for resourceGroup, deploymentNames := range item.Deployments {
			deployments[resourceGroup] = make(map[string][]ARMOperation)
			for name, operations := range deploymentNames {
				var ops []ARMOperation
				for _, op := range operations {
					parsed, err := operationFor(op)
					if err != nil {
						return nil, err
					}
					ops = append(ops, parsed)
				}
				deployments[resourceGroup][name] = ops
			}
		}
		times = append(times, TestInfo{
			Identifier:  item.Identifier,
			StartedAt:   s,
			FinishedAt:  f,
			Steps:       steps,
			Deployments: deployments,
		})
	}

	return &Options{
		completedOptions: &completedOptions{
			Times:     times,
			OutputDir: o.OutputDir,
		},
	}, nil
}

func operationFor(op timing.Operation) (ARMOperation, error) {
	var children []ARMOperation
	for _, child := range op.Children {
		c, err := operationFor(child)
		if err != nil {
			return c, err
		}
		children = append(children, c)
	}

	startTime, err := time.Parse(time.RFC3339, op.StartTimestamp)
	if err != nil {
		return ARMOperation{}, fmt.Errorf("failed to parse start date: %w", err)
	}

	duration, err := parseISO8601Duration(op.Duration)
	if err != nil {
		return ARMOperation{}, fmt.Errorf("failed to parse duration: %w", err)
	}

	var resource *timing.Resource
	if op.Resource != nil {
		resource = &timing.Resource{
			ResourceType:  op.Resource.ResourceType,
			ResourceGroup: op.Resource.ResourceGroup,
			Name:          op.Resource.Name,
		}
	}

	return ARMOperation{
		OperationType: op.OperationType,
		StartTime:     startTime,
		Duration:      duration,
		Resource:      resource,
		Children:      children,
	}, nil
}

var pattern = regexp.MustCompile(`^P((?P<year>\d+)Y)?((?P<month>\d+)M)?((?P<week>\d+)W)?((?P<day>\d+)D)?(T((?P<hour>\d+)H)?((?P<minute>\d+)M)?((?P<second>\d+\.?\d*)S)?)?$`)

// parseISO8601Duration parses a string into a time.Duration as per the IS08601 specification
// See: https://en.wikipedia.org/wiki/ISO_8601#Durations
func parseISO8601Duration(from string) (time.Duration, error) {
	var match []string
	var d time.Duration

	if pattern.MatchString(from) {
		match = pattern.FindStringSubmatch(from)
	} else {
		return d, errors.New("could not parse duration string")
	}

	for i, name := range pattern.SubexpNames() {
		part := match[i]
		if i == 0 || name == "" || part == "" {
			continue
		}

		val, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return d, err
		}
		switch name {
		case "year":
			return d, fmt.Errorf("unsupported format with year: %s", from)
		case "month":
			return d, fmt.Errorf("unsupported format with month: %s", from)
		case "week":
			return d, fmt.Errorf("unsupported format with week: %s", from)
		case "day":
			return d, fmt.Errorf("unsupported format with day: %s", from)
		case "hour":
			d += time.Hour * time.Duration(val)
		case "minute":
			d += time.Minute * time.Duration(val)
		case "second":
			d += time.Nanosecond * time.Duration(val*1e9)
		default:
			return d, fmt.Errorf("unknown field %s", name)
		}
	}

	return d, nil
}

type TestInfo struct {
	Identifier []string
	StartedAt  time.Time
	FinishedAt time.Time

	Steps       []StepTimingMetadata
	Deployments map[string]map[string][]ARMOperation
}

type StepTimingMetadata struct {
	Name       string
	StartedAt  time.Time
	FinishedAt time.Time
}

type ARMOperation struct {
	OperationType string
	StartTime     time.Time
	Duration      time.Duration
	Resource      *timing.Resource
	Children      []ARMOperation
}

func (o *Options) Visualize(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	if len(o.Times) == 0 {
		logger.Info("No tests seem to have run, exiting.")
		return nil
	}

	slices.SortFunc(o.Times, func(a, b TestInfo) int {
		t := a.StartedAt.Compare(b.StartedAt)
		if t != 0 {
			return t
		}
		return strings.Compare(strings.Join(a.Identifier, " "), strings.Join(b.Identifier, " "))
	})

	startTime := o.Times[0].StartedAt
	endTime := startTime

	testNameSet := sets.New[string]()
	for _, item := range o.Times {
		testNameSet.Insert(strings.Join(item.Identifier, " "))
	}
	testNames := sets.List(testNameSet)

	var highWaterMark []opts.BarData
	var names []string
	data := map[string][]opts.BarData{}
	startTimes := map[string]time.Time{}
	for _, testName := range testNames {
		data[testName] = []opts.BarData{}
		startTimes[testName] = time.Now() // will be after any start time in the set
	}
	for _, item := range o.Times {
		identifier := strings.Join(item.Identifier, " ")
		if item.FinishedAt.After(endTime) {
			endTime = item.FinishedAt
		}
		startTimes[identifier] = item.StartedAt

		highWaterMark = append(highWaterMark, opts.BarData{
			Value: item.StartedAt.Sub(startTime).Milliseconds(),
			Tooltip: &opts.Tooltip{
				Show: ptr.To(false),
			},
		})
		names = append(names, identifier)
		for _, step := range item.Steps {
			highWaterMark = append(highWaterMark, opts.BarData{
				Value: step.StartedAt.Sub(startTime).Milliseconds(),
				Tooltip: &opts.Tooltip{
					Show: ptr.To(false),
				},
			})
			names = append(names, step.Name)
		}
		for testName := range data {
			if testName == identifier {
				data[testName] = append(data[testName], opts.BarData{
					Value: item.FinishedAt.Sub(item.StartedAt).Milliseconds(),
					ItemStyle: &opts.ItemStyle{
						BorderColor: "black",
						BorderWidth: 1,
					},
				})
				for _, step := range item.Steps {
					data[testName] = append(data[testName], opts.BarData{
						Name:  fmt.Sprintf("%s: %s", identifier, step.Name),
						Value: step.FinishedAt.Sub(step.StartedAt).Milliseconds(),
					})
				}
			} else {
				data[testName] = append(data[testName], opts.BarData{
					Value: 0,
					Tooltip: &opts.Tooltip{
						Show: ptr.To(false),
					},
				})
				for range item.Steps {
					data[testName] = append(data[testName], opts.BarData{
						Value: 0,
						Tooltip: &opts.Tooltip{
							Show: ptr.To(false),
						},
					})
				}
			}
		}
	}

	// insert data into the graph in order of start time so we are unlikely to end up with data series next to each other with the same color
	slices.SortFunc(testNames, func(a, b string) int {
		return startTimes[a].Compare(startTimes[b])
	})

	waterfall := charts.NewBar()
	waterfall.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle: "Timing Analysis",
			Renderer:  "svg",
			Height:    "1024px",
		}),
		charts.WithTitleOpts(opts.Title{
			Title:      "ARO HCP End-to-End Test Timing",
			Subtitle:   fmt.Sprintf("Overall Runtime: %s", endTime.Sub(startTime).String()),
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
		charts.WithLegendOpts(opts.Legend{
			Show: ptr.To(false),
		}),
		charts.WithYAxisOpts(opts.YAxis{Show: ptr.To(false)}),
		charts.WithXAxisOpts(opts.XAxis{AxisLabel: &opts.AxisLabel{Rotate: -22.5}}),
	)
	waterfall.AddJSFuncStrs(
		opts.FuncOpts(render.EchartsInstancePlaceholder+`.setOption({"xAxis": {"axisLabel": {"formatter": `+millisecondAxisFormatter(startTime)+`}}});`),
		opts.FuncOpts(render.EchartsInstancePlaceholder+`.setOption({"tooltip": {"formatter": `+multiValueFormatter+`}});`),
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
		XYReversal()
	for _, testName := range testNames {
		waterfall.AddSeries(testName, data[testName],
			charts.WithBarChartOpts(opts.BarChart{
				Stack: "total",
			}))
	}

	if err := os.MkdirAll(o.OutputDir, 0755); err != nil {
		return fmt.Errorf("unable to create output directory: %w", err)
	}
	// filename matches the regex for display in https://github.com/openshift/release/blob/ef035a66f45a195fb6d5f68ce8ec284434aebe9f/core-services/prow/02_config/_config.yaml#L251-L252
	stepFile := filepath.Join(o.OutputDir, "e2e-timelines_spyglass_timing-metadata.html")
	output, err := os.Create(stepFile)
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

	logger.Info("Created visualization.", "output", stepFile)

	for _, item := range o.Times {
		if len(item.Deployments) > 0 {
			if err := o.visualizeARM(ctx, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *Options) visualizeARM(ctx context.Context, item TestInfo) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	operationTypes := sets.New[string]()
	var flatOperations []ARMOperation
	for _, deploymentNames := range item.Deployments {
		for _, operations := range deploymentNames {
			flatOperations = append(flatOperations, operations...)
			for _, op := range operations {
				collectActions(op, operationTypes)
			}
		}
	}

	startTime := item.StartedAt

	var names []string
	var placeholder []opts.BarData
	var deployments []opts.BarData
	actions := map[string][]opts.BarData{}
	for _, action := range operationTypes.UnsortedList() {
		actions[action] = []opts.BarData{}
	}
	slices.SortFunc(flatOperations, func(a, b ARMOperation) int {
		return a.StartTime.Compare(b.StartTime)
	})
	for _, op := range flatOperations {
		names, placeholder, deployments, actions = collectOperations(op, startTime, names, placeholder, deployments, actions)
	}

	waterfall := charts.NewBar()
	waterfall.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle: "ARM Deployment Timing Analysis",
			Renderer:  "svg",
			Height:    "1024px",
		}),
		charts.WithTitleOpts(opts.Title{
			Title:      "ARM Deployment Operation Timing: " + strings.Join(item.Identifier, " "),
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
		charts.WithGridOpts(opts.Grid{
			Top: "80",
		}),
		charts.WithLegendOpts(opts.Legend{
			Top:  "30",
			Data: append([]string{"", "Deployments"}, sets.List(operationTypes)...),
		}),
		charts.WithYAxisOpts(opts.YAxis{Show: ptr.To(false)}),
		charts.WithXAxisOpts(opts.XAxis{AxisLabel: &opts.AxisLabel{Rotate: -22.5}}),
	)
	waterfall.AddJSFuncStrs(
		opts.FuncOpts(render.EchartsInstancePlaceholder+`.setOption({"xAxis": {"axisLabel": {"formatter": `+millisecondAxisFormatter(startTime)+`}}});`),
		opts.FuncOpts(render.EchartsInstancePlaceholder+`.setOption({"tooltip": {"formatter": `+multiValueFormatter+`}});`),
	)
	waterfall.SetXAxis(names).
		AddSeries("Placeholder", placeholder,
			charts.WithBarChartOpts(opts.BarChart{
				Stack: "total",
			}),
			charts.WithItemStyleOpts(opts.ItemStyle{
				BorderColor: "transparent",
				Color:       "transparent",
			})).
		AddSeries("Deployments", deployments,
			charts.WithBarChartOpts(opts.BarChart{
				Stack: "total",
			})).
		XYReversal()
	for _, action := range sets.List(operationTypes) {
		waterfall.AddSeries(action, actions[action],
			charts.WithBarChartOpts(opts.BarChart{
				Stack: "total",
			}))
	}

	encodedIdentifier, err := yaml.Marshal(item.Identifier)
	if err != nil {
		return fmt.Errorf("unable to marshal identifier: %w", err)
	}
	hash := sha256.New()
	hash.Write(encodedIdentifier)
	hashBytes := hash.Sum(nil)
	outputFile := filepath.Join(o.OutputDir, fmt.Sprintf("timing-metadata-%s", hex.EncodeToString(hashBytes)), "arm.html")
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		return fmt.Errorf("unable to create output directory: %w", err)
	}
	output, err := os.Create(outputFile)
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

	logger.Info("Created ARM visualization.", "output", outputFile)
	return nil
}

func collectActions(op ARMOperation, into sets.Set[string]) {
	into.Insert(op.OperationType)
	for _, child := range op.Children {
		collectActions(child, into)
	}
}

func collectOperations(op ARMOperation, startTime time.Time, names []string, placeholder, deployments []opts.BarData, actions map[string][]opts.BarData) ([]string, []opts.BarData, []opts.BarData, map[string][]opts.BarData) {
	names = append(names, nameFor(op))
	placeholder = append(placeholder, opts.BarData{
		Value: op.StartTime.Sub(startTime).Milliseconds(),
		Tooltip: &opts.Tooltip{
			Show: ptr.To(false),
		},
	})
	if op.Resource != nil && strings.EqualFold(op.Resource.ResourceType, "Microsoft.Resources/deployments") {
		deployments = append(deployments, opts.BarData{
			Value: op.Duration.Milliseconds(),
		})
		for action := range actions {
			actions[action] = append(actions[action], opts.BarData{
				Value: 0,
				Tooltip: &opts.Tooltip{
					Show: ptr.To(false),
				},
			})
		}
		var childNames []string
		var childPlaceholders []opts.BarData
		var childDeployments []opts.BarData
		childActions := map[string][]opts.BarData{}
		for action := range actions {
			childActions[action] = []opts.BarData{}
		}
		slices.SortFunc(op.Children, func(a, b ARMOperation) int {
			return a.StartTime.Compare(b.StartTime)
		})
		for _, child := range op.Children {
			childNames, childPlaceholders, childDeployments, childActions = collectOperations(child, startTime, childNames, childPlaceholders, childDeployments, childActions)
		}
		names = append(names, childNames...)
		placeholder = append(placeholder, childPlaceholders...)
		deployments = append(deployments, childDeployments...)
		for action := range childActions {
			actions[action] = append(actions[action], childActions[action]...)
		}
	} else {
		deployments = append(deployments, opts.BarData{
			Value: 0,
			Tooltip: &opts.Tooltip{
				Show: ptr.To(false),
			},
		})
		for action := range actions {
			if strings.EqualFold(action, op.OperationType) {
				actions[action] = append(actions[action], opts.BarData{
					Value: op.Duration.Milliseconds(),
				})
			} else {
				actions[action] = append(actions[action], opts.BarData{
					Value: 0,
					Tooltip: &opts.Tooltip{
						Show: ptr.To(false),
					},
				})
			}
		}
	}
	return names, placeholder, deployments, actions
}

func nameFor(op ARMOperation) string {
	if op.Resource == nil {
		return op.OperationType
	}
	return fmt.Sprintf("%s %s %s/%s", op.OperationType, op.Resource.ResourceType, op.Resource.ResourceGroup, op.Resource.Name)
}

const multiValueFormatter = `(params) => {
	for (const item of params.slice(1)) {
		if (item.value == 0) {
			continue
		}
		const totalMilliseconds = item.value;
    
		const hours = Math.floor(totalMilliseconds / (60 * 60 * 1000));
    	const minutes = Math.floor((totalMilliseconds % (60 * 60 * 1000)) / (60 * 1000));
    	const seconds = Math.floor((totalMilliseconds % (60 * 1000))/1000);
		const milliseconds = totalMilliseconds % (1000);

    	let result = '';
    	if (hours > 0) {
	        result += ` + "`${hours}h`" + `;
	    }
	    if (minutes > 0 || hours > 0) {
	        result += ` + "`${minutes}m`" + `;
	    }
	    result += ` + "`${seconds}.${milliseconds}s`" + `;
	    return item.name + ": " + result;
	}
}`

func millisecondAxisFormatter(base time.Time) string {
	return `(value, index) => {
    const baseDate = new Date('` + base.Format(time.RFC3339) + `');
    const resultDate = new Date(baseDate.getTime() + value);
	const options = {
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        timeZoneName: 'short'
    };
    return resultDate.toLocaleTimeString('en-US', options);
}`
}
