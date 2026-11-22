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

// RawOptions holds the flag values before validation.
type RawOptions struct {
	ReportPath      string
	ChangesPath     string
	Note            string
	PublicationURL  string
	OutputPath      string
	PublicationBody string
	JobName         string
	JobURL          string
	BuildID         string
	BaseCommit      string
}

// validatedOptions holds the raw values that passed validation.
type validatedOptions struct {
	raw *RawOptions
}

// completedOptions holds the decoded reports and the requested outputs.
type completedOptions struct {
	report          *citests.PriorityReport
	summary         *priorityrefresh.Summary
	note            string
	publicationURL  string
	outputPath      string
	publicationBody string
	jobName         string
	jobURL          string
	buildID         string
	baseCommit      string
	out             io.Writer
}

// NewCommand builds the priorities-summary command.
func NewCommand() *cobra.Command {
	raw := &RawOptions{}
	cmd := &cobra.Command{
		Use:   "priorities-summary",
		Short: "Render the start-priority refresh summary page",
		Long: `Render one self-contained HTML page describing a start-priority run: the
collection window, per-environment coverage, every proposed declaration and the
unchanged, insufficient and unmatched totals.

The page is rendered from the reports the other commands already produced, so it
can be regenerated at any point of a run, including after a pull request was
opened. With no report the command renders only the note, which is what a run
that stopped before collecting needs.

With --publication-body the same reports also render the pull request body, so
both outputs read one source of values. Each renderer still chooses its own
fields and formats, so the text is not identical.`,
		Example: `  aro-hcp-tests priorities-summary --report report.json --changes changes.json --note "read-only" --output summary.html
  aro-hcp-tests priorities-summary --note "Publication skipped: an open generated pull request exists" --output summary.html
  aro-hcp-tests priorities-summary --report report.json --changes changes.json --output summary.html --publication-body body.md --job-name periodic-ci-Azure-ARO-HCP-main-e2e-test-priorities-refresh --build-id 42 --base-commit abc123`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return summarize(cmd.OutOrStdout(), raw)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&raw.ReportPath, "report", "", "Priority report produced by the priorities command")
	flags.StringVar(&raw.ChangesPath, "changes", "", "Change summary produced by the refresh-priorities command")
	flags.StringVar(&raw.Note, "note", "", "Outcome line rendered under the run header")
	flags.StringVar(&raw.PublicationURL, "publication-url", "", "Pull request opened by this run, rendered as a link")
	flags.StringVar(&raw.OutputPath, "output", "", "File to write the summary to (required)")
	flags.StringVar(&raw.PublicationBody, "publication-body", "", "File to write the pull request body to")
	flags.StringVar(&raw.JobName, "job-name", "", "Job that produced the run, named in the publication body")
	flags.StringVar(&raw.JobURL, "job-url", "", "Run URL linked from the publication body")
	flags.StringVar(&raw.BuildID, "build-id", "", "Prow build ID named in the publication body")
	flags.StringVar(&raw.BaseCommit, "base-commit", "", "Commit the proposals were computed against")
	return cmd
}

// summarize runs the command phases in order.
func summarize(out io.Writer, raw *RawOptions) error {
	validated, err := raw.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(out)
	if err != nil {
		return err
	}
	return completed.Run()
}

// Validate rejects incomplete input before any file is read.
func (r *RawOptions) Validate() (*validatedOptions, error) {
	if r.OutputPath == "" {
		return nil, fmt.Errorf("--output is required")
	}
	if (r.ReportPath == "") != (r.ChangesPath == "") {
		return nil, fmt.Errorf("--report and --changes must be given together")
	}
	if r.ReportPath == "" && r.Note == "" {
		return nil, fmt.Errorf("a notice summary requires --note")
	}
	if r.PublicationBody == "" {
		return &validatedOptions{raw: r}, nil
	}
	if r.ReportPath == "" {
		return nil, fmt.Errorf("--publication-body requires --report and --changes")
	}
	if r.JobName == "" {
		return nil, fmt.Errorf("--publication-body requires --job-name")
	}
	if r.BaseCommit == "" {
		return nil, fmt.Errorf("--publication-body requires --base-commit")
	}
	return &validatedOptions{raw: r}, nil
}

// Complete decodes the reports the outputs are rendered from.
func (v *validatedOptions) Complete(out io.Writer) (*completedOptions, error) {
	raw := v.raw
	completed := &completedOptions{
		note: raw.Note, publicationURL: raw.PublicationURL, outputPath: raw.OutputPath,
		publicationBody: raw.PublicationBody, jobName: raw.JobName, jobURL: raw.JobURL,
		buildID: raw.BuildID, baseCommit: raw.BaseCommit, out: out,
	}
	if raw.ReportPath == "" {
		return completed, nil
	}
	report, err := readReport(raw.ReportPath)
	if err != nil {
		return nil, err
	}
	summary, err := readChanges(raw.ChangesPath)
	if err != nil {
		return nil, err
	}
	completed.report = &report
	completed.summary = &summary
	return completed, nil
}

// Run writes the requested outputs.
func (c *completedOptions) Run() error {
	input := priorityrefresh.SummaryInput{Note: c.note, PublicationURL: c.publicationURL}
	if c.report != nil {
		input.Report = c.report
		input.Summary = c.summary
	}
	if err := writeOutput(c.outputPath, func(w io.Writer) error {
		return priorityrefresh.WriteSummaryHTML(w, input)
	}); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	if c.publicationBody != "" {
		body := priorityrefresh.PublicationBodyInput{
			Report: c.report, Summary: c.summary, JobName: c.jobName,
			JobURL: c.jobURL, BuildID: c.buildID, BaseCommit: c.baseCommit,
		}
		if err := writeOutput(c.publicationBody, func(w io.Writer) error {
			return priorityrefresh.WritePublicationBody(w, body)
		}); err != nil {
			return fmt.Errorf("write publication body: %w", err)
		}
	}
	fmt.Fprintf(c.out, "Wrote %s\n", c.outputPath)
	return nil
}

// writeFile creates the file and passes it to the renderer, closing it on every
// path so a renderer failure cannot leak a descriptor.
func writeOutput(path string, render func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := render(file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
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
