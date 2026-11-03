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
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
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

// Complete loads recorded timing intervals, retaining lifecycle and operation metadata.
func (o *ValidatedOptions) Complete(logger logr.Logger) (*Options, error) {
	timingPathMatcher, err := regexp.Compile(`timing-metadata-[a-z0-9]+\.yaml(\.gz)?`)
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
		fileData, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read timing input file: %w", err)
		}

		var rawTiming []byte
		// Check if file is gzipped
		if strings.HasSuffix(d.Name(), ".gz") {
			gzipReader, err := gzip.NewReader(bytes.NewReader(fileData))
			if err != nil {
				return fmt.Errorf("failed to create gzip reader for %s: %w", file, err)
			}
			defer gzipReader.Close()

			rawTiming, err = io.ReadAll(gzipReader)
			if err != nil {
				return fmt.Errorf("failed to decompress %s: %w", file, err)
			}
		} else {
			rawTiming = fileData
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
		steps := parseSteps(item.Steps, logger)
		phases := parseSteps(item.Phases, logger)
		var cleanupStarted time.Time
		if item.CleanupStartedAt != "" {
			cleanupStarted, err = time.Parse(time.RFC3339Nano, item.CleanupStartedAt)
			if err != nil {
				logger.Error(err, "failed to parse cleanup start date", "identifier", item.Identifier)
			}
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
			Identifier:       item.Identifier,
			StartedAt:        s,
			FinishedAt:       f,
			CleanupStartedAt: cleanupStarted,
			Outcome:          item.Outcome,
			Phases:           phases,
			Steps:            steps,
			Deployments:      deployments,
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
	Identifier       []string
	StartedAt        time.Time
	FinishedAt       time.Time
	CleanupStartedAt time.Time
	Outcome          string
	Phases           []StepTimingMetadata
	Steps            []StepTimingMetadata
	Deployments      map[string]map[string][]ARMOperation
}

type StepTimingMetadata struct {
	Name       string
	Kind       string
	Outcome    string
	Incomplete bool
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

// Visualize writes timing charts and successful-test operation summaries.
func (o *Options) Visualize(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return err
	}

	if len(o.Times) == 0 {
		logger.Info("No tests seem to have run, exiting.")
		return nil
	}

	waterfall := testTimingChart(o.Times)
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
	rendered := waterfall.RenderContent()
	rendered = bytes.Replace(rendered, []byte("<head>"), []byte(`<head><meta name="viewport" content="width=device-width, initial-scale=1">`), 1)
	summary, err := lifecycleSummaryHTML(o.Times)
	if err != nil {
		return fmt.Errorf("failed to render timing summary: %w", err)
	}
	rendered = bytes.Replace(rendered, []byte("<body>"), append([]byte("<body>"), summary...), 1)
	// workaround for spyglass cutting off page bottoms: inject spacer before </body>
	rendered = bytes.Replace(rendered, []byte("</body>"), []byte(`<div style="height: 30px;"></div>`+"\n"+"</body>"), 1)
	if _, err := output.Write(rendered); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
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

// Renders lifecycle rows with semantic colors and responsive zoom behavior.
func testTimingChart(times []TestInfo) *charts.Bar {
	slices.SortFunc(times, func(a, b TestInfo) int {
		t := a.StartedAt.Compare(b.StartedAt)
		if t != 0 {
			return t
		}
		return strings.Compare(strings.Join(a.Identifier, " "), strings.Join(b.Identifier, " "))
	})

	startTime := times[0].StartedAt
	endTime := startTime

	waterfall := charts.NewBar()
	waterfall.Renderer = render.NewChartRender(testChart{waterfall}, waterfall.Validate)
	var highWaterMark, durations []opts.BarData
	var names []string
	var groups []timingChartGroup
	for _, item := range times {
		if item.FinishedAt.After(endTime) {
			endTime = item.FinishedAt
		}
	}
	for _, item := range times {
		identifier := strings.Join(item.Identifier, " ")
		group := timingChartGroup{
			Name:          identifier,
			StartTime:     item.StartedAt.UnixMilli(),
			Duration:      float64(item.FinishedAt.Sub(item.StartedAt)) / float64(time.Millisecond),
			FirstRow:      len(names),
			Outcome:       item.Outcome,
			FullLifecycle: hasFullLifecycle(item),
		}
		highWaterMark = append(highWaterMark, opts.BarData{Value: float64(item.StartedAt.Sub(startTime)) / float64(time.Millisecond)})
		durations = append(durations, opts.BarData{Name: identifier, Value: group.Duration})
		names = append(names, identifier)
		for _, row := range lifecycleRows(item) {
			color := timingColor(row.Kind)
			if color == "" {
				color = "#5470c6"
			}
			group.Segments = append(group.Segments, timingChartSegment{
				Name: row.Name, Kind: row.Kind, Color: color,
				Start: float64(row.StartedAt.Sub(startTime)) / float64(time.Millisecond),
				End:   float64(row.FinishedAt.Sub(startTime)) / float64(time.Millisecond),
			})
		}
		groups = append(groups, group)
	}

	waterfall.SetGlobalOptions(
		charts.WithAnimation(false),
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle: "Test Timing Analysis",
			Renderer:  "canvas",
			Height:    "60vh",
			Width:     "100%",
		}),
		charts.WithTitleOpts(opts.Title{
			Title:      "Test timing",
			Subtitle:   lifecycleSubtitle(times, endTime.Sub(startTime)),
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
		charts.WithXAxisOpts(opts.XAxis{AxisLabel: &opts.AxisLabel{
			Rotate: -22.5,
		}}),
		charts.WithDataZoomOpts(opts.DataZoom{
			Type: "slider", Orient: "vertical", YAxisIndex: []int{0}, Start: 0, End: 100,
		}),
		charts.WithDataZoomOpts(opts.DataZoom{
			Type: "slider", Orient: "horizontal", XAxisIndex: []int{0}, Start: 0, End: 100, FilterMode: "none",
		}),
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
		AddSeries("Duration", durations,
			charts.WithBarChartOpts(opts.BarChart{
				Stack: "total",
			})).
		XYReversal()
	waterfall.AddJSFuncStrs(
		opts.FuncOpts(render.EchartsInstancePlaceholder+`.setOption({xAxis: {axisLabel: {formatter: `+millisecondAxisFormatter(startTime.Format(time.RFC3339Nano))+`}}, tooltip: {confine: true, formatter: `+testTimingTooltipFormatter+`}});`),
		opts.FuncOpts(`window.addEventListener('resize', () => {`+render.EchartsInstancePlaceholder+`.resize();});`),
		opts.FuncOpts(`(() => {
			const chart = `+render.EchartsInstancePlaceholder+`;
			const applyTheme = () => {
				const style = getComputedStyle(document.body);
				const color = style.color;
				chart.setOption({
					backgroundColor: 'transparent',
					textStyle: {color},
					title: {textStyle: {color}, subtextStyle: {color}},
					xAxis: {axisLabel: {color}, axisLine: {lineStyle: {color}}, splitLine: {lineStyle: {color, opacity: 0.15}}},
					tooltip: {backgroundColor: style.backgroundColor, borderColor: color, textStyle: {color}},
					dataZoom: [{textStyle: {color}, borderColor: color, backgroundColor: style.backgroundColor}, {textStyle: {color}, borderColor: color, backgroundColor: style.backgroundColor}],
				});
			};
			applyTheme();
			window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyTheme);
			new MutationObserver(applyTheme).observe(document.documentElement, {
				attributes: true, attributeFilter: ['class', 'style', 'data-darkreader-scheme']
			});
		})();`),
		opts.FuncOpts(timingChartSortingJS(render.EchartsInstancePlaceholder, groups, float64(endTime.Sub(startTime))/float64(time.Millisecond))),
	)

	return waterfall
}

const timingChartMinimumWindowMillis = 1000
const timingChartRowHeight = 26
const timingChartMinimumOverviewHeight = 320

type timingChartGroup struct {
	Name          string               `json:"name"`
	StartTime     int64                `json:"startTime"`
	Duration      float64              `json:"duration"`
	FirstRow      int                  `json:"firstRow"`
	Segments      []timingChartSegment `json:"segments"`
	Outcome       string               `json:"outcome"`
	FullLifecycle bool                 `json:"fullLifecycle"`
}

type timingChartSegment struct {
	Name  string  `json:"name"`
	Kind  string  `json:"kind"`
	Color string  `json:"color"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Builds the segmented test bars, sorting, and zoom script for the timing chart.
func timingChartSortingJS(chart string, groups []timingChartGroup, totalDurationMillis float64) string {
	encoded, _ := json.Marshal(groups)
	if bytes.Contains(encoded, []byte("__f__")) {
		encoded = bytes.ReplaceAll(encoded, []byte("__f__"), []byte(`\u005f\u005ff\u005f\u005f`))
	}
	if totalDurationMillis <= 0 {
		totalDurationMillis = timingChartMinimumWindowMillis
	}
	replacements := strings.NewReplacer(
		"__CHART__", chart,
		"__GROUPS__", string(encoded),
		"__TIME_RANGE__", strconv.FormatFloat(totalDurationMillis, 'f', -1, 64),
		"__MINIMUM_WINDOW__", strconv.Itoa(timingChartMinimumWindowMillis),
		"__ROW_HEIGHT__", strconv.Itoa(timingChartRowHeight),
		"__MINIMUM_OVERVIEW_HEIGHT__", strconv.Itoa(timingChartMinimumOverviewHeight),
	)
	return replacements.Replace(`(() => {
	const chart = __CHART__;
	const groups = __GROUPS__;
	const initial = chart.getOption();
	chart.setOption({dataZoom: initial.dataZoom.map((zoom, index) => ({...zoom, id: index === 0 ? 'timing-rows' : 'timing-time', ...(index === 0 ? {showDataShadow:true} : {})}))}, {replaceMerge: ['dataZoom']});
	const allOffsets = initial.series[0].data;
	const allDurations = initial.series[1].data;
	let currentLabels = groups.map(group => group.name);
	let currentOffsets = allOffsets;
	let currentDurations = allDurations;
	const sort = document.getElementById('timing-sort');
	const outcomeFilters = {
		skipped: document.getElementById('timing-show-skipped'),
		passed: document.getElementById('timing-show-passed'),
		failed: document.getElementById('timing-show-failed')
	};
	const isVisible = test => {
		const outcome = String(test.outcome || '').toLowerCase();
		const category = ['skipped','pending'].includes(outcome) ? 'skipped'
			: outcome === 'passed' ? 'passed'
			: ['failed','timedout','panicked','interrupted','aborted'].includes(outcome) ? 'failed' : null;
		return category === null || outcomeFilters[category]?.checked !== false;
	};
	/* An auto-height iframe cannot also determine its content height. */
	let viewportWindow;
	try {
		viewportWindow = window.top || window;
		void viewportWindow.innerHeight;
	} catch {
		viewportWindow = null;
	}
	const overviewHeight = () => viewportWindow
		? Math.max(__MINIMUM_OVERVIEW_HEIGHT__, Math.floor(viewportWindow.innerHeight - (document.querySelector('.container').getBoundingClientRect().top + window.scrollY) - 16))
		: __MINIMUM_OVERVIEW_HEIGHT__;
	const renderItem = (params, api) => {
		const test = ordered[params.dataIndex];
		const y = api.coord([0, api.value(0)])[1];
		const height = Math.max(1, Math.min(__ROW_HEIGHT__, api.size([0, 1])[1]) * 0.8);
		const segments = test.segments || [];
		const start = api.value(1), end = api.value(2);
		const boundaries = [...new Set([start, end, ...segments.flatMap(s => [s.start, s.end])])].sort((a,b) => a-b);
		const children = [];
		for (let i = 1; i < boundaries.length; i++) {
			const left = boundaries[i-1], right = boundaries[i];
			if (right <= left) continue;
			const active = segments.filter(s => s.start < right && s.end > left);
			const operations = active.filter(s => s.kind !== 'phase' && s.kind !== 'gap');
			const gaps = active.filter(s => s.kind === 'gap');
			const phases = active.filter(s => s.kind === 'phase');
			const layers = operations.length ? operations : gaps.length ? gaps : phases;
			const colors = layers.length ? layers.map(s => s.color) : ['#64748b'];
			const x = api.coord([left, 0])[0], xEnd = api.coord([right, 0])[0];
			colors.forEach((color, lane) => {
				const shape = echarts.graphic.clipRectByRect({x, y:y-height/2+lane*height/colors.length, width:xEnd-x, height:height/colors.length}, params.coordSys);
				if (shape) children.push({type:'rect', shape, style:{fill:color}});
			});
		}
		return {type:'group', children};
	};
	let ordered = groups.slice();
	const tooltip = params => {
		const item = Array.isArray(params) ? params.find(value => value.seriesId === 'timing-tests') : params;
		if (!item) return '';
		const test = ordered[item.dataIndex];
		if (!test) return '';
		const start = Number(currentOffsets[item.dataIndex].value);
		const duration = Math.max(0, test.duration);
		const testEnd = start + duration;
		const endpointTolerance = Number.EPSILON * Math.max(1, Math.abs(start), duration, Math.abs(testEnd)) * 4;
		const formatDuration = value => {
			if (value > 0 && value < 1000) return '<1s';
			const total = Math.floor(value / 1000);
			const hours = Math.floor(total / 3600);
			const minutes = Math.floor(total % 3600 / 60);
			return (hours ? hours + 'h' : '') + (minutes ? minutes + 'm' : '') + total % 60 + 's';
		};
		const element = (tag, style, text) => {
			const node = document.createElement(tag);
			Object.assign(node.style, style);
			if (text !== undefined) node.textContent = text;
			return node;
		};
		const content = element('div', {width:'min(720px, 80vw)', maxHeight:'min(520px, 65vh)', overflowY:'auto', whiteSpace:'normal', fontSize:'12px', lineHeight:'1.4'});
		content.append(
			element('div', {fontWeight:'600', fontSize:'14px', overflowWrap:'anywhere'}, test.name),
			element('div', {opacity:'0.75', margin:'4px 0 12px'}, 'Total duration: ' + formatDuration(duration))
		);
		const gaps = (test.segments || []).filter(segment => segment.kind === 'gap');
		const gapDuration = gaps.reduce((total, segment) => total + segment.end - segment.start, 0);
		const gapPercent = duration > 0 ? gapDuration / duration * 100 : 0;
		const percentage = gapPercent > 0 && gapPercent < 1 ? '<1%' : Math.round(gapPercent) + '%';
		content.append(
			element('div', {opacity:'0.8', margin:'0 0 12px'}, 'No recorded operation: ' + formatDuration(gapDuration) + ' (' + percentage + ' of recorded time)'),
			...(test.fullLifecycle === false ? [element('div', {opacity:'0.7', marginBottom:'12px'}, 'Recorded interval only; cleanup coverage unknown.')] : [])
		);
		const timelineGrid = (segments, showAxis = false) => {
			const grid = element('div', {display:'grid', gridTemplateColumns:'minmax(0, 1.2fr) minmax(0, 1.8fr) 64px', gap:'8px 12px', alignItems:'center'});
			if (showAxis) {
				const axis = element('div', {display:'flex', justifyContent:'space-between', opacity:'0.65', fontSize:'11px'});
				axis.append(element('span', {}, '0s'));
				if (duration > 0) axis.append(element('span', {}, formatDuration(duration / 2)), element('span', {}, formatDuration(duration)));
				grid.append(element('span', {opacity:'0.65'}, 'Stage / operation'), axis, element('span', {opacity:'0.65'}, 'Duration'));
			}
			for (const segment of segments) {
				const stage = segment.kind === 'phase';
				const label = element(stage ? 'h3' : 'div', {borderLeft:'3px solid ' + segment.color, paddingLeft:stage ? '6px' : '14px', margin:'0', fontSize:stage ? '13px' : '12px', fontWeight:stage ? '600' : '400', overflowWrap:'anywhere'}, segment.displayName || (segment.kind === 'gap' ? 'No recorded operation' : segment.name));
				const track = element('div', {position:'relative', height:'10px', borderRadius:'2px', overflow:'hidden', background:'rgba(128,128,128,0.18)'});
				const left = duration > 0 ? (segment.start - start) / duration * 100 : 0;
				const width = duration > 0 ? (segment.end - segment.start) / duration * 100 : 0;
				track.append(element('div', {position:'absolute', top:'0', bottom:'0', left:width === 0 ? 'min(' + left + '%, calc(100% - 2px))' : left + '%', width:width + '%', minWidth:'2px', background:segment.color, borderRadius:'2px'}));
				grid.append(label, track, element('span', {textAlign:'right', fontVariantNumeric:'tabular-nums', whiteSpace:'nowrap'}, formatDuration(segment.end - segment.start)));
			}
			return grid;
		};
		const segments = (test.segments || []).slice().sort((a,b) => a.start - b.start || b.end - a.end);
		const stageName = phase => phase.name.replace(/^Phase: /, '');
		const stages = segments.filter(segment => segment.kind === 'phase').map(phase => ({phase, rows:[]}));
		const crossing = [], outside = [];
		for (const segment of segments.filter(segment => segment.kind !== 'phase')) {
			const overlaps = stages.filter(({phase}) => segment.start === segment.end
				? phase.start <= segment.start && (segment.start < phase.end || Math.abs(phase.end - testEnd) <= endpointTolerance && segment.start === phase.end || phase.start === phase.end && segment.start === phase.start)
				: phase.start < segment.end && phase.end > segment.start);
			if (overlaps.length === 1 && overlaps[0].phase.start <= segment.start && overlaps[0].phase.end >= segment.end) {
				overlaps[0].rows.push(segment);
			} else if (overlaps.length) {
				let cursor = segment.start, uncovered = false;
				for (const {phase} of overlaps) {
					if (phase.start > cursor) uncovered = true;
					cursor = Math.max(cursor, phase.end);
				}
				uncovered ||= cursor < segment.end;
				const context = overlaps.map(({phase}) => stageName(phase));
				if (uncovered) context.push('outside recorded stages');
				crossing.push({...segment, displayName:(segment.kind === 'gap' ? 'No recorded operation' : segment.name) + '; overlaps: ' + context.join(', ')});
			} else {
				outside.push({...segment, displayName:segment.name});
			}
		}
		const appendRows = (section, rows) => {
			const tiny = segment => segment.kind === 'gap' && segment.end > segment.start && segment.end - segment.start < 1000;
			const tinyGaps = rows.filter(tiny);
			section.append(timelineGrid(rows.filter(segment => !tiny(segment))));
			if (tinyGaps.length) {
				const details = element('details', {marginTop:'10px'});
				const tinyDuration = tinyGaps.reduce((total, segment) => total + segment.end - segment.start, 0);
				details.append(
					element('summary', {cursor:'pointer', opacity:'0.8', marginBottom:'8px', paddingLeft:'14px'}, 'Small uncovered intervals (<1s): ' + tinyGaps.length + ', total ' + formatDuration(tinyDuration)),
					timelineGrid(tinyGaps)
				);
				section.append(details);
			}
		};
		content.append(timelineGrid([], true));
		for (const {phase, rows} of stages) {
			const section = element('section', {marginTop:'12px', padding:'8px 0', borderTop:'1px solid rgba(128,128,128,0.25)', background:'rgba(128,128,128,0.06)'});
			const heading = timelineGrid([{...phase, displayName:stageName(phase)}]);
			heading.style.marginBottom = '8px';
			section.append(heading);
			appendRows(section, rows);
			content.append(section);
		}
		for (const [name, rows] of [['Across recorded stages', crossing], ['Outside recorded stages', outside]]) {
			if (!rows.length) continue;
			const section = element('section', {marginTop:'12px', paddingTop:'8px', borderTop:'1px solid rgba(128,128,128,0.25)'});
			section.append(element('h3', {fontSize:'13px', margin:'0 0 8px', fontWeight:'600'}, name));
			appendRows(section, rows);
			content.append(section);
		}
		return content;
	};
	const visibleRowBounds = (start, end) => {
		const lastIndex = currentLabels.length - 1;
		if (lastIndex < 0) return [0, -1];
		return [
			Math.max(0, Math.round(lastIndex * start / 100)),
			Math.min(lastIndex, Math.round(lastIndex * end / 100))
		];
	};
	const fitViewportHeight = () => {
		const height = overviewHeight();
		if (chart.getHeight() === height && chart.getDom().style.height === height + 'px') return;
		chart.getDom().style.height = height + 'px';
		chart.resize({height});
	};
	window.addEventListener('resize', fitViewportHeight);
	if (viewportWindow && viewportWindow !== window) viewportWindow.addEventListener('resize', fitViewportHeight);
	const fitTimeRange = (start, end) => {
		const [first, last] = visibleRowBounds(start, end);
		let earliest = Infinity;
		let latest = -Infinity;
		for (let i = first; i <= last; i++) {
			const offset = Number(currentOffsets[i].value);
			earliest = Math.min(earliest, offset);
			latest = Math.max(latest, offset + Number(currentDurations[i].value));
		}
		if (!Number.isFinite(earliest)) return;
		const padding = Math.max(__MINIMUM_WINDOW__, (latest - earliest) * 0.08);
		const totalDuration = __TIME_RANGE__;
		const timeStart = Math.max(0, earliest - padding);
		const timeEnd = Math.min(totalDuration, latest + padding);
		const startPercent = timeStart / totalDuration * 100;
		const endPercent = Math.min(100, Math.max(startPercent + 0.1, timeEnd / totalDuration * 100));
		chart.dispatchAction({type: 'dataZoom', dataZoomId: 'timing-time', start: startPercent, end: endPercent});
	};
	const update = () => {
		const visible = groups.filter(isVisible);
		const mode = sort.value;
		visible.sort((a, b) => {
			if (mode === 'duration' && a.duration !== b.duration) return b.duration - a.duration;
			if (mode === 'name') {
				const byName = a.name.localeCompare(b.name);
				if (byName) return byName;
			}
			return a.startTime - b.startTime || a.name.localeCompare(b.name);
		});
		ordered = visible;
		const labels = visible.map(group => group.name);
		const offsets = visible.map(group => allOffsets[group.firstRow]);
		const durations = visible.map(group => allDurations[group.firstRow]);
		const visibleRows = labels.length;
		const end = 100;
		const timeRange = {start: 0, end: 100};
		let earliest = Infinity;
		let latest = -Infinity;
		for (let i = 0; i < visibleRows; i++) {
			const offset = Number(offsets[i].value);
			earliest = Math.min(earliest, offset);
			latest = Math.max(latest, offset + Number(durations[i].value));
		}
		if (visibleRows > 0) {
			const padding = Math.max(__MINIMUM_WINDOW__, (latest - earliest) * 0.08);
			const totalDuration = __TIME_RANGE__;
			const timeStart = Math.max(0, earliest - padding);
			const timeEnd = Math.min(totalDuration, latest + padding);
			timeRange.start = timeStart / totalDuration * 100;
			timeRange.end = Math.min(100, Math.max(timeRange.start + 0.1, timeEnd / totalDuration * 100));
		}
		currentLabels = labels;
		currentOffsets = offsets;
		currentDurations = durations;
		chart.setOption({
			xAxis: {min:0, max:__TIME_RANGE__}, yAxis: {data: labels},
			tooltip: {trigger:'axis', axisPointer:{axis:'y',type:'shadow'}, enterable:true, hideDelay:200, formatter:tooltip},
			series: [{id:'timing-tests', type:'custom', renderItem, encode:{x:[1,2],y:0},
				data:visible.map((group,index) => ({name:group.name, value:[index,Number(offsets[index].value),Number(offsets[index].value)+Number(durations[index].value)]}))}],
			dataZoom: [{id: 'timing-rows', start: 0, end}, {id: 'timing-time', ...timeRange}]
		}, {replaceMerge:['series']});
		fitViewportHeight();
		const counter = document.getElementById('timing-visible-count');
		if (counter) counter.textContent = visibleRows + ' of ' + groups.length + ' tests shown';
	};
	sort.addEventListener('change', update);
	Object.values(outcomeFilters).forEach(control => control?.addEventListener?.('change', update));
	chart.on('datazoom', event => {
		const updates = event.batch || [event];
		const rows = updates.find(item => item.dataZoomId === 'timing-rows');
		if (!rows) return;
		fitTimeRange(rows.start, rows.end);
	});
	update();
	window.addEventListener('load', fitViewportHeight);
})();`)
}

type testChart struct {
	*charts.Bar
}

// go-echarts disables HTML escaping and interprets __f__ markers as JavaScript.
// Keep labels as data; install the trusted formatters separately via AddJSFuncStrs.
func (c testChart) JSONNotEscaped() template.HTML {
	data, err := json.Marshal(c.JSON())
	if err != nil {
		panic(err)
	}
	return template.HTML(strings.ReplaceAll(string(data), "__f__", `\u005f\u005ff\u005f\u005f`)) //nolint:gosec // HTML-safe JSON, not user-authored HTML
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
		opts.FuncOpts(render.EchartsInstancePlaceholder+`.setOption({"xAxis": {"axisLabel": {"formatter": `+millisecondAxisFormatter(startTime.Format(time.RFC3339))+`}}});`),
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

// Axis hover keeps zero-length and subpixel bars inspectable without inflating durations.
const testTimingTooltipFormatter = `(params) => {
	const item = params.find(item => item.seriesIndex === 1);
	if (!item) return '';
	const content = document.createElement('div');
	content.style.whiteSpace = 'normal';
	content.style.maxWidth = 'min(600px, 80vw)';
	const hours = Math.floor(item.value / 3600000);
	const minutes = Math.floor(item.value % 3600000 / 60000);
	const seconds = Math.floor(item.value % 60000 / 1000);
	content.textContent = item.name + ': ' + (hours ? hours + 'h' : '') +
		(hours || minutes ? minutes + 'm' : '') + seconds + 's';
	return content;
}`

const multiValueFormatter = `(params) => {
	for (const item of params.slice(1)) {
		if (item.value == 0) {
			continue
		}
		const totalMilliseconds = item.value;
    
		const hours = Math.floor(totalMilliseconds / (60 * 60 * 1000));
    	const minutes = Math.floor((totalMilliseconds % (60 * 60 * 1000)) / (60 * 1000));
    	const seconds = Math.floor((totalMilliseconds % (60 * 1000))/1000);

    	let result = '';
    	if (hours > 0) {
	        result += ` + "`${hours}h`" + `;
	    }
	    if (minutes > 0 || hours > 0) {
	        result += ` + "`${minutes}m`" + `;
	    }
	    result += ` + "`${seconds}s`" + `;
	    return item.name + ": " + result;
	}
}`

func millisecondAxisFormatter(base string) string {
	return `(value, index) => {
    const baseDate = new Date('` + base + `');
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
