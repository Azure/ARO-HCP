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
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/rightsize-requests/pkg/targets"
)

// rightSizingTargetKeys selects the resolved config path, or every candidate
// path that unresolved evidence must block, using the CLI's identity rules.
func rightSizingTargetKeys(namespace, kind, workload, container, cluster string, init bool) []string {
	if target, ok := targets.Resolve(namespace, kind, workload, container, cluster, init); ok {
		return []string{target.ResourcePath}
	}
	if kind == "Pod" {
		workload = ""
	}
	var keys []string
	for _, target := range targets.CandidateTargets(namespace, workload, container, cluster, init) {
		keys = append(keys, target.ResourcePath)
	}
	return append(keys, rightSizingHCPTargetKeys(namespace, kind, workload, container, "hcp/", targets.MinimalTargets())...)
}

func rightSizingHCPTargetKeys(namespace, kind, workload, container, prefix string, catalog []targets.MinimalTarget) []string {
	var keys []string
	if strings.HasPrefix(namespace, "ocm-arohcp") && len(namespace) > len("ocm-arohcp") {
		resolved := slices.Contains([]string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob"}, kind) && strings.TrimSpace(workload) != "" && !strings.EqualFold(workload, "unknown")
		for _, target := range catalog {
			if container == target.Container && (!resolved || workload == target.Workload) {
				keys = append(keys, prefix+target.Workload+"/"+target.Container)
			}
		}
	}
	return keys
}

// Additional capabilities participate in display guards, but not savings: the
// report cannot establish a configured baseline for these targets.
func rightSizingDisplayTargetKeys(row rightSizingRecommendation) []string {
	keys := rightSizingTargetKeys(row.Namespace, row.Kind, row.Workload, row.Container, row.Cluster, row.InitContainer)
	return append(keys, rightSizingHCPTargetKeys(row.Namespace, row.Kind, row.Workload, row.Container, "additional/", targets.AdditionalMinimalTargets())...)
}

func renderRightSizingHTML(report rightSizingReport) ([]byte, error) {
	if report.Version != 3 {
		return nil, fmt.Errorf("unsupported right-sizing version %d (expected 3 with peak-only ceil rounding); regenerate from replica-peaks.json using render-right-sizing", report.Version)
	}
	if report.Headroom != 1 {
		return nil, fmt.Errorf("right-sizing requires headroom=1; regenerate from replica-peaks.json using render-right-sizing")
	}
	if report.Start.IsZero() || report.End.IsZero() || report.End.Before(report.Start) {
		return nil, fmt.Errorf("right-sizing start and end must be nonzero timestamps with start <= end")
	}
	// Shared config targets are broader than display identities. Include hidden
	// unresolved/init evidence and all clusters before any display filtering.
	type targetScope struct {
		suggested float64
		blocked   bool
	}
	scopes := map[string]targetScope{}
	valid := func(v *float64) bool { return v != nil && *v >= 0 && !math.IsNaN(*v) && !math.IsInf(*v, 0) }
	for _, row := range report.Recommendations {
		for _, key := range rightSizingDisplayTargetKeys(row) {
			key += "/" + row.Resource
			scope := scopes[key]
			if valid(row.Suggested) {
				scope.suggested = math.Max(scope.suggested, *row.Suggested)
			}
			scope.blocked = scope.blocked || !row.Eligible || !targets.EditableInCluster(row.Namespace, row.Kind, row.Workload, row.Container, row.Cluster, row.InitContainer) ||
				row.Replicas <= 0 || row.MeasuredReplicas != row.Replicas || !valid(row.Suggested) || !valid(row.Peak) ||
				!valid(row.RequestMin) || !valid(row.RequestMax) || *row.RequestMin > *row.RequestMax
			scopes[key] = scope
		}
	}
	// Editability and shared-target guards are HTML-only metadata. Keep the
	// persisted report and per-identity measurements/suggestions unchanged.
	type displayRecommendation struct {
		rightSizingRecommendation
		Editable bool `json:"editable"`
	}
	var recommendations []displayRecommendation
	if report.Recommendations != nil {
		recommendations = make([]displayRecommendation, 0, len(report.Recommendations))
	}
	for _, row := range report.Recommendations {
		blocked, suggested := false, 0.0
		for _, key := range rightSizingDisplayTargetKeys(row) {
			scope := scopes[key+"/"+row.Resource]
			blocked = blocked || scope.blocked
			suggested = math.Max(suggested, scope.suggested)
		}
		if blocked {
			row.Eligible, row.Actionable = false, false
			row.Warnings = append(slices.Clone(row.Warnings), "Shared target blocked: ineligible, unresolved or missing measurement/replica evidence in source rows across clusters and namespaces.")
		} else if row.Suggested != nil && *row.Suggested < suggested {
			row.Warnings = append(slices.Clone(row.Warnings), "Shared target has a higher suggestion in other source rows; the displayed suggestion is per identity, not a config edit.")
		}
		if targets.RequiresConfiguredBaseline(row.Namespace, row.Kind, row.Workload, row.Container, row.Cluster, row.InitContainer) {
			row.Eligible, row.Actionable = false, false
			row.Warnings = append(slices.Clone(row.Warnings), "Requires configured baseline in hypershift.additionalMinimalResourceRequests; use --additional-hcp-config. Not automatically editable until configured.")
		}
		recommendations = append(recommendations, displayRecommendation{
			rightSizingRecommendation: row,
			Editable:                  targets.EditableInCluster(row.Namespace, row.Kind, row.Workload, row.Container, row.Cluster, row.InitContainer),
		})
	}
	data, err := json.Marshal(struct {
		rightSizingReport
		Recommendations []displayRecommendation `json:"recommendations"`
	}{report, recommendations})
	if err != nil {
		return nil, fmt.Errorf("encode right-sizing report: %w", err)
	}
	tmpl, err := template.ParseFS(templatesFS, "artifacts/right-sizing.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse right-sizing template: %w", err)
	}
	var out bytes.Buffer
	// RawMessage retains JSON types; html/template's JavaScript context escapes
	// script terminators and HTML-like strings without trusting report content.
	// Embed source identities with display guards; the page groups them after cluster filtering.
	if err := tmpl.Execute(&out, json.RawMessage(data)); err != nil {
		return nil, fmt.Errorf("render right-sizing HTML: %w", err)
	}
	return out.Bytes(), nil
}

func newRenderRightSizingCommand() *cobra.Command {
	var input, output, utilizationInput string
	var threshold float64
	cmd := &cobra.Command{
		Use:   "render-right-sizing --input replica-peaks.json --output DIR",
		Short: "Build a right-sizing report from saved replica peaks without Azure credentials.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(input) == "" || strings.TrimSpace(output) == "" {
				return fmt.Errorf("--input and --output are required")
			}
			if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 || threshold > 1 {
				return fmt.Errorf("--change-threshold must be a finite fraction between 0 and 1")
			}
			file, err := os.Open(input)
			if err != nil {
				return fmt.Errorf("open replica peaks input: %w", err)
			}
			defer file.Close()
			decoder := json.NewDecoder(file)
			decoder.DisallowUnknownFields()
			var peaks replicaPeakReport
			if err := decoder.Decode(&peaks); err != nil {
				return fmt.Errorf("decode replica peaks input: %w", err)
			}
			var trailing any
			if err := decoder.Decode(&trailing); err != io.EOF {
				return fmt.Errorf("replica peaks input must contain exactly one JSON report (trailing data)")
			}
			if peaks.Version != 1 {
				return fmt.Errorf("unsupported replica peaks version %d (expected 1)", peaks.Version)
			}
			if peaks.Start.IsZero() || peaks.End.IsZero() || peaks.End.Before(peaks.Start) || peaks.GeneratedAt.IsZero() {
				return fmt.Errorf("replica peaks generatedAt, start and end must be nonzero timestamps with start <= end")
			}
			if _, err := json.Marshal(peaks); err != nil {
				return fmt.Errorf("validate replica peaks input: %w", err)
			}
			report := buildRightSizingReport(peaks, threshold)
			var utilizationInfo os.FileInfo
			if utilizationInput != "" {
				baseline, err := os.Open(utilizationInput)
				if err != nil {
					return fmt.Errorf("open utilization input: %w", err)
				}
				defer baseline.Close()
				utilizationInfo, err = baseline.Stat()
				if err != nil {
					return err
				}
				utilization, err := decodeUtilizationReport(baseline)
				if err != nil {
					return err
				}
				if err := validateUtilizationReport(utilization); err != nil {
					return err
				}
				report.Savings = buildRightSizingSavings(report, utilization)
			}
			html, err := renderRightSizingHTML(report)
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return fmt.Errorf("encode right-sizing JSON: %w", err)
			}
			inputInfo, err := file.Stat()
			if err != nil {
				return fmt.Errorf("stat replica peaks input: %w", err)
			}
			for _, name := range []string{"right-sizing.json", "right-sizing.html"} {
				if info, err := os.Stat(filepath.Join(output, name)); err == nil {
					if os.SameFile(inputInfo, info) || (utilizationInfo != nil && os.SameFile(utilizationInfo, info)) {
						return fmt.Errorf("right-sizing output must not overwrite the input or an alias of it")
					}
				} else if !os.IsNotExist(err) {
					return fmt.Errorf("stat right-sizing output: %w", err)
				}
			}
			if err := os.MkdirAll(output, 0755); err != nil {
				return fmt.Errorf("create right-sizing output directory: %w", err)
			}
			if err := os.WriteFile(filepath.Join(output, "right-sizing.json"), append(data, '\n'), 0644); err != nil {
				return fmt.Errorf("write right-sizing JSON: %w", err)
			}
			// No -summary suffix: this standalone page must not create another
			// Spyglass inline report alongside the observability summary.
			if err := os.WriteFile(filepath.Join(output, "right-sizing.html"), html, 0644); err != nil {
				return fmt.Errorf("write right-sizing HTML: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "Saved version 1 replica-peaks.json (not a right-sizing report).")
	cmd.Flags().StringVar(&utilizationInput, "utilization-input", "", "Optional utilization.json from the same run for concurrent request savings estimates.")
	cmd.Flags().StringVar(&output, "output", "", "Directory for right-sizing.json and right-sizing.html.")
	cmd.Flags().Float64Var(&threshold, "change-threshold", 0.1, "Relative request-change deadband, from 0 to 1; alert risk bypasses it.")
	return cmd
}
