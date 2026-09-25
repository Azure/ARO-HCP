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
	"strings"

	"github.com/spf13/cobra"
)

func renderRightSizingHTML(report rightSizingReport) ([]byte, error) {
	if report.Version != 2 {
		return nil, fmt.Errorf("unsupported right-sizing version %d (expected 2 with ceil rounding); regenerate from replica-peaks.json using render-right-sizing", report.Version)
	}
	if report.Start.IsZero() || report.End.IsZero() || report.End.Before(report.Start) {
		return nil, fmt.Errorf("right-sizing start and end must be nonzero timestamps with start <= end")
	}
	data, err := json.Marshal(report)
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
	if err := tmpl.Execute(&out, json.RawMessage(data)); err != nil {
		return nil, fmt.Errorf("render right-sizing HTML: %w", err)
	}
	return out.Bytes(), nil
}

func newRenderRightSizingCommand() *cobra.Command {
	var input, output string
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
					if os.SameFile(inputInfo, info) {
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
	cmd.Flags().StringVar(&output, "output", "", "Directory for right-sizing.json and right-sizing.html.")
	cmd.Flags().Float64Var(&threshold, "change-threshold", 0.1, "Relative request-change deadband, from 0 to 1; alert risk bypasses it.")
	return cmd
}
