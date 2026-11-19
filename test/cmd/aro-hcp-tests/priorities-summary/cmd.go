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

// Package prioritiessummary renders the human-readable start-priority summary
// that the scheduled refresh publishes next to its machine-readable reports.
package prioritiessummary

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/internal/priorityrefresh"
	"github.com/Azure/ARO-HCP/test/pkg/citests"
)

// options holds the resolved command configuration.
type options struct {
	reportPath     string
	changesPath    string
	note           string
	publicationURL string
	outputPath     string
	out            io.Writer
}

// NewCommand builds the priorities-summary command.
func NewCommand() *cobra.Command {
	opts := &options{}
	cmd := &cobra.Command{
		Use:   "priorities-summary",
		Short: "Render the start-priority refresh summary page",
		Long: `Render one self-contained HTML page describing a start-priority run: the
collection window, per-environment coverage, every proposed declaration and the
unchanged, insufficient and unmatched totals.

The page is rendered from the reports the other commands already produced, so it
can be regenerated at any point of a run, including after a pull request was
opened. With no report the command renders only the note, which is what a run
that stopped before collecting needs.`,
		Example: `  aro-hcp-tests priorities-summary --report report.json --changes changes.json --note "read-only" --output summary.html
  aro-hcp-tests priorities-summary --note "Publication skipped: an open generated pull request exists" --output summary.html`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.out = cmd.OutOrStdout()
			if err := opts.Validate(); err != nil {
				return err
			}
			return opts.Run()
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.reportPath, "report", "", "Priority report produced by the priorities command")
	flags.StringVar(&opts.changesPath, "changes", "", "Change summary produced by the refresh-priorities command")
	flags.StringVar(&opts.note, "note", "", "Outcome line rendered under the run header")
	flags.StringVar(&opts.publicationURL, "publication-url", "", "Pull request opened by this run, rendered as a link")
	flags.StringVar(&opts.outputPath, "output", "", "File to write the summary to (required)")
	return cmd
}

// Validate rejects incomplete input before any file is read.
func (o *options) Validate() error {
	if o.outputPath == "" {
		return fmt.Errorf("--output is required")
	}
	if (o.reportPath == "") != (o.changesPath == "") {
		return fmt.Errorf("--report and --changes must be given together")
	}
	if o.reportPath == "" && o.note == "" {
		return fmt.Errorf("a notice summary requires --note")
	}
	return nil
}

// Run renders the summary to the requested file.
func (o *options) Run() error {
	input := priorityrefresh.SummaryInput{Note: o.note, PublicationURL: o.publicationURL}
	if o.reportPath != "" {
		report, err := readReport(o.reportPath)
		if err != nil {
			return err
		}
		summary, err := readChanges(o.changesPath)
		if err != nil {
			return err
		}
		input.Report = &report
		input.Summary = &summary
	}

	file, err := os.Create(o.outputPath)
	if err != nil {
		return fmt.Errorf("create summary: %w", err)
	}
	if err := priorityrefresh.WriteSummaryHTML(file, input); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close summary: %w", err)
	}
	fmt.Fprintf(o.out, "Wrote %s\n", o.outputPath)
	return nil
}

// readReport decodes exactly one priorities report object.
func readReport(path string) (citests.PriorityReport, error) {
	var report citests.PriorityReport
	if err := decodeSingle(path, &report); err != nil {
		return citests.PriorityReport{}, fmt.Errorf("read report: %w", err)
	}
	if report.SchemaVersion != citests.ReportSchemaVersion {
		return citests.PriorityReport{}, fmt.Errorf("report schema version %d is not %d", report.SchemaVersion, citests.ReportSchemaVersion)
	}
	if report.Algorithm != citests.AlgorithmEnvironmentNormalizedMean {
		return citests.PriorityReport{}, fmt.Errorf("report algorithm %q is not %q", report.Algorithm, citests.AlgorithmEnvironmentNormalizedMean)
	}
	return report, nil
}

// readChanges decodes exactly one refresh change summary object.
func readChanges(path string) (priorityrefresh.Summary, error) {
	var summary priorityrefresh.Summary
	if err := decodeSingle(path, &summary); err != nil {
		return priorityrefresh.Summary{}, fmt.Errorf("read changes: %w", err)
	}
	return summary, nil
}

// decodeSingle decodes exactly one JSON object and rejects trailing data.
func decodeSingle(path string, into any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s must contain exactly one JSON object", path)
	}
	return nil
}
