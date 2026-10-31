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

package gatherobservability

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	promutil "github.com/Azure/ARO-HCP/test/util/prometheus"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

// metricsReport preserves renderer inputs before label compaction, gap insertion,
// or legend filtering. Azure Monitor samples are already normalized by the
// collector; replay never needs credentials or an autoscale-capacity lookup.
type metricsReport struct {
	Version    int            `json:"version"`
	TimeWindow timeWindow     `json:"timeWindow"`
	Panels     []metricsPanel `json:"panels"`
}

type metricsPanel struct {
	Title   string         `json:"title"`
	Queries []metricsQuery `json:"queries"`
}

type metricsQuery struct {
	Spec       QuerySpec         `json:"spec"`
	ResourceID string            `json:"resourceID,omitempty"`
	Results    []promutil.Result `json:"results"`
	Error      string            `json:"error,omitempty"`
	Warning    string            `json:"warning,omitempty"`
}

func renderMetricsPanels(report metricsReport, render func(panelPageData) ([]byte, error)) ([]observabilityTab, error) {
	if report.Version != 1 {
		return nil, fmt.Errorf("unsupported metrics report version %d", report.Version)
	}
	start, err := time.Parse(time.RFC3339, report.TimeWindow.Start)
	if err != nil {
		return nil, fmt.Errorf("invalid metrics start time: %w", err)
	}
	end, err := time.Parse(time.RFC3339, report.TimeWindow.End)
	if err != nil || !end.After(start) {
		return nil, fmt.Errorf("metrics end time must be a valid RFC3339 timestamp after start")
	}
	tw := timing.TimeWindow{Start: start, End: end}
	var tabs []observabilityTab
	var errs []error
	for _, panel := range report.Panels {
		page := panelPageData{Title: panel.Title}
		page.TimeWindow.Start, page.TimeWindow.End = report.TimeWindow.Start, report.TimeWindow.End
		for _, query := range panel.Queries {
			page.Charts = append(page.Charts, buildChartData(query.Spec, query.ResourceID, query.Error, query.Warning, query.Results, tw))
		}
		html, err := render(page)
		if err != nil {
			errs = append(errs, fmt.Errorf("render metrics panel %q: %w", panel.Title, err))
		}
		tabs = append(tabs, observabilityTab{Title: panel.Title, HTML: string(incompleteHTML(html, err))})
	}
	return tabs, errors.Join(errs...)
}

func newRenderMetricsCommand() *cobra.Command {
	var input, output string
	cmd := &cobra.Command{
		Use:   "render-metrics --input metrics.json --output metrics-summary.html",
		Short: "Render saved metrics without Azure configuration or credentials.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if input == "" || output == "" {
				return fmt.Errorf("--input and --output are required")
			}
			inputInfo, err := os.Stat(input)
			if err != nil {
				return err
			}
			if outputInfo, err := os.Stat(output); err == nil && os.SameFile(inputInfo, outputInfo) {
				return fmt.Errorf("metrics output must not overwrite the input or an alias of it")
			}
			data, err := os.ReadFile(input)
			if err != nil {
				return fmt.Errorf("read metrics input: %w", err)
			}
			var report metricsReport
			if err := json.Unmarshal(data, &report); err != nil {
				return fmt.Errorf("decode metrics input: %w", err)
			}
			tabs, err := renderMetricsPanels(report, renderPanelHTML)
			if err != nil {
				return err
			}
			tmp, err := os.CreateTemp(filepath.Dir(output), ".metrics-*.html")
			if err != nil {
				return err
			}
			defer os.Remove(tmp.Name())
			if err := tmp.Close(); err != nil {
				return err
			}
			if err := renderObservabilityPage(tmp.Name(), tabs); err != nil {
				return err
			}
			if err := os.Chmod(tmp.Name(), 0644); err != nil {
				return err
			}
			return os.Rename(tmp.Name(), output)
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "Saved metrics JSON report.")
	cmd.Flags().StringVar(&output, "output", "", "Output HTML file containing the metrics tabs.")
	return cmd
}
