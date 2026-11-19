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

// Package prioritiesrun collects, scores and plans one start-priority refresh in
// a single invocation, so a caller does not assemble the steps itself.
package prioritiesrun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/Azure/ARO-HCP/test/internal/priorityrefresh"
	"github.com/Azure/ARO-HCP/test/pkg/citests"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// kustoEndpointPart bounds the cluster and region flags to DNS labels.
var kustoEndpointPart = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// options holds the resolved command configuration.
type options struct {
	cluster     string
	region      string
	database    string
	since       time.Duration
	from        string
	to          string
	jobFilter   string
	timeout     time.Duration
	minSamples  int
	repo        string
	artifactDir string
	write       bool
	maxAge      time.Duration
	summaryNote string
	now         func() time.Time
	out         io.Writer
	errOut      io.Writer
	selection   citests.Selection
	endpoint    *url.URL
	report      citests.PriorityReport
	changes     priorityrefresh.ChangeSet
	summary     priorityrefresh.Summary
}

// NewCommand builds the priorities-run command.
func NewCommand() *cobra.Command {
	opts := &options{now: time.Now}
	cmd := &cobra.Command{
		Use:   "priorities-run",
		Short: "Collect, score and plan one start-priority refresh",
		Long: `Run the whole read-only refresh in one place: collect the selected CI runs
from Kusto, score them, require recent coverage in every environment, plan the
source changes and render the summary.

The catalog comes from the running binary, so no intermediate file is needed.
--write applies the planned changes to the checkout; without it nothing is
modified. Publication stays out of the command.`,
		Example:       `  aro-hcp-tests priorities-run --kusto hcp-dev-us-2 --region eastus2 --since 720h --repo . --artifact-dir /tmp/out`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.out, opts.errOut = cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := opts.Validate(); err != nil {
				return err
			}
			return opts.Run(cmd.Context())
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.cluster, "kusto", "", "Kusto cluster name (required)")
	flags.StringVar(&opts.region, "region", "", "Kusto region (required)")
	flags.StringVar(&opts.database, "database", "ServiceLogs", "Kusto database")
	flags.DurationVar(&opts.since, "since", 720*time.Hour, "Window ending now, e.g. 720h")
	flags.StringVar(&opts.from, "from", "", "Inclusive run completion timestamp in RFC3339")
	flags.StringVar(&opts.to, "to", "", "Exclusive run completion timestamp in RFC3339")
	flags.StringVar(&opts.jobFilter, "job-filter", "e2e-parallel", "Case-sensitive literal substring of job name")
	flags.DurationVar(&opts.timeout, "timeout", 10*time.Minute, "Total live collection timeout")
	flags.IntVar(&opts.minSamples, "min-samples", citests.DefaultMinSamples, "Minimum distinct eligible builds per spec and environment")
	flags.StringVar(&opts.repo, "repo", ".", "Repository checkout whose test/e2e sources are mapped")
	flags.StringVar(&opts.artifactDir, "artifact-dir", "", "Directory for the score, change and summary artifacts")
	flags.BoolVar(&opts.write, "write", false, "Apply the planned changes to the checkout")
	flags.DurationVar(&opts.maxAge, "max-age", 7*24*time.Hour, "Maximum age of the newest eligible run per environment")
	flags.StringVar(&opts.summaryNote, "summary-note", "", "Outcome line rendered on the summary page")
	return cmd
}

// Validate rejects invalid input before any credential or network use.
func (o *options) Validate() error {
	if !kustoEndpointPart.MatchString(o.cluster) || !kustoEndpointPart.MatchString(o.region) {
		return fmt.Errorf("--kusto and --region must be DNS names")
	}
	if o.database == "" {
		return fmt.Errorf("--database is required")
	}
	if o.timeout <= 0 || o.timeout > 30*time.Minute {
		return fmt.Errorf("--timeout must be greater than zero and at most 30m")
	}
	if o.minSamples < citests.DefaultMinSamples {
		return fmt.Errorf("--min-samples must be at least %d", citests.DefaultMinSamples)
	}
	if o.maxAge <= 0 {
		return fmt.Errorf("--max-age must be greater than zero")
	}
	if _, err := os.Stat(filepath.Join(o.repo, "test", "e2e")); err != nil {
		return fmt.Errorf("--repo must contain test/e2e: %w", err)
	}
	from, to, err := o.window()
	if err != nil {
		return err
	}
	endpoint, err := kusto.KustoEndpoint(o.cluster, o.region)
	if err != nil {
		return fmt.Errorf("resolve Kusto endpoint: %w", err)
	}
	o.endpoint = endpoint
	o.selection = citests.Selection{
		Origin:    endpoint.String() + "/" + o.database,
		From:      from,
		To:        to,
		JobFilter: o.jobFilter,
	}
	return o.selection.Validate()
}

// window resolves the explicit or relative collection window.
func (o *options) window() (time.Time, time.Time, error) {
	if o.from == "" && o.to == "" {
		to := o.now().UTC()
		return to.Add(-o.since), to, nil
	}
	from, err := time.Parse(time.RFC3339, o.from)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("--from must be RFC3339: %w", err)
	}
	to, err := time.Parse(time.RFC3339, o.to)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("--to must be RFC3339: %w", err)
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("window requires --from < --to")
	}
	return from.UTC(), to.UTC(), nil
}

// Run performs the refresh and writes its artifacts.
func (o *options) Run(ctx context.Context) error {
	bundle, err := o.collect(ctx)
	if err != nil {
		return err
	}
	if err := o.score(bundle); err != nil {
		return err
	}
	if err := o.checkCoverage(); err != nil {
		return o.reportCoverageFailure(err)
	}
	catalog, err := buildCatalog()
	if err != nil {
		return err
	}
	if o.changes, err = priorityrefresh.Plan(o.repo, catalog, o.report); err != nil {
		return err
	}
	if o.write {
		if err := priorityrefresh.Apply(o.changes); err != nil {
			return err
		}
	}
	o.summary = o.changes.Summary(o.write)
	if err := o.writeArtifacts(); err != nil {
		return err
	}
	return o.printTable()
}

// collect reads the selected runs from Kusto.
func (o *options) collect(ctx context.Context) (citests.Bundle, error) {
	// The client speaks to the cluster host; the database is passed to Collect
	// separately, exactly as the priorities command does.
	client, err := kusto.NewClient(o.endpoint, o.timeout)
	if err != nil {
		return citests.Bundle{}, fmt.Errorf("create Kusto client: %w", err)
	}
	defer func() { _ = client.Close() }()
	return citests.Collect(ctx, client, o.database, o.selection, o.timeout)
}

// score turns the collected runs into a priority report.
func (o *options) score(bundle citests.Bundle) error {
	report, err := citests.ScorePriorities(bundle, citests.PriorityOptions{MinSamples: o.minSamples})
	if err != nil {
		return err
	}
	o.report = report
	return nil
}

// checkCoverage refuses to plan when any environment is thin or stale.
func (o *options) checkCoverage() error {
	byEnvironment := make(map[string]citests.EnvironmentSummary, len(o.report.Environments))
	for _, environment := range o.report.Environments {
		byEnvironment[environment.Environment] = environment
	}
	windowEnd := o.report.Selection.To
	for _, name := range citests.ScoringEnvironments {
		environment, found := byEnvironment[name]
		if !found || environment.EligibleBuildCount < o.minSamples {
			return fmt.Errorf("environment %s has %d eligible builds, need %d", name, environment.EligibleBuildCount, o.minSamples)
		}
		if environment.LatestEligibleCompletion == nil {
			return fmt.Errorf("environment %s has no eligible completion", name)
		}
		if windowEnd.Sub(*environment.LatestEligibleCompletion) > o.maxAge {
			return fmt.Errorf("environment %s last had an eligible run at %s, more than %s before the window end",
				name, environment.LatestEligibleCompletion.UTC().Format(time.RFC3339), o.maxAge)
		}
	}
	return nil
}

// reportCoverageFailure keeps the collected scores and the refusal reason as
// artifacts before returning the gate error, so a refused run still explains
// itself instead of discarding everything it learned.
func (o *options) reportCoverageFailure(cause error) error {
	note := "Coverage insufficient: " + cause.Error()
	if err := o.discardChanges(); err != nil {
		return fmt.Errorf("%w (and the stale change summary could not be removed: %v)", cause, err)
	}
	if err := o.writeScores(); err != nil {
		return fmt.Errorf("%w (and the score artifact could not be written: %v)", cause, err)
	}
	if err := o.writePage(note, priorityrefresh.Summary{}); err != nil {
		return fmt.Errorf("%w (and the summary page could not be written: %v)", cause, err)
	}
	if err := o.printTable(); err != nil {
		return fmt.Errorf("%w (and the coverage table could not be printed: %v)", cause, err)
	}
	return cause
}

// writeArtifacts stores the scores, the change summary and the page.
func (o *options) writeArtifacts() error {
	if err := o.writeScores(); err != nil {
		return err
	}
	if err := o.writeChanges(); err != nil {
		return err
	}
	return o.writePage(o.summaryNote, o.summary)
}

// writeScores stores the score report.
func (o *options) writeScores() error {
	if o.artifactDir == "" {
		return nil
	}
	if err := os.MkdirAll(o.artifactDir, 0o755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	scores, err := json.MarshalIndent(o.report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scores: %w", err)
	}
	if err := os.WriteFile(filepath.Join(o.artifactDir, "priority-scores.json"), append(scores, '\n'), 0o644); err != nil {
		return fmt.Errorf("write scores: %w", err)
	}
	return nil
}

// discardChanges removes a change summary left by an earlier run, so a refused
// run never presents previous changes beside the current scores.
func (o *options) discardChanges() error {
	if o.artifactDir == "" {
		return nil
	}
	path := filepath.Join(o.artifactDir, "priority-changes.json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// writeChanges stores the machine-readable change summary.
func (o *options) writeChanges() error {
	if o.artifactDir == "" {
		return nil
	}
	changes, err := json.MarshalIndent(o.summary, "", "  ")
	if err != nil {
		return fmt.Errorf("encode changes: %w", err)
	}
	if err := os.WriteFile(filepath.Join(o.artifactDir, "priority-changes.json"), append(changes, '\n'), 0o644); err != nil {
		return fmt.Errorf("write changes: %w", err)
	}
	return nil
}

// writePage renders the summary page.
func (o *options) writePage(note string, summary priorityrefresh.Summary) error {
	if o.artifactDir == "" {
		return nil
	}
	page, err := os.Create(filepath.Join(o.artifactDir, "priorities-summary.html"))
	if err != nil {
		return fmt.Errorf("create summary: %w", err)
	}
	if err := priorityrefresh.WriteSummaryHTML(page, priorityrefresh.SummaryInput{
		Report: &o.report, Summary: &summary, Note: note,
	}); err != nil {
		_ = page.Close()
		return err
	}
	return page.Close()
}

// printTable renders the decision on stdout.
func (o *options) printTable() error {
	writer := tabwriter.NewWriter(o.out, 0, 0, 2, ' ', 0)
	globalMedian := "none"
	if o.report.GlobalMedianMinutes != nil {
		globalMedian = fmt.Sprintf("%.1fm", *o.report.GlobalMedianMinutes)
	}
	fmt.Fprintf(writer, "Window\t%s to %s\tmedian %s\n",
		o.report.Selection.From.UTC().Format(time.RFC3339), o.report.Selection.To.UTC().Format(time.RFC3339), globalMedian)
	for _, environment := range o.report.Environments {
		fmt.Fprintf(writer, "Coverage\t%s\t%d builds\t%d samples\t%.1fm\tlatest %s\n",
			environment.Environment, environment.EligibleBuildCount, environment.EligibleSampleCount,
			environment.MedianMinutes, environment.LatestEligibleCompletion)
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "DECLARATION\tPRIORITY\tSCORE\tBUILDS\tSPEC")
	for _, change := range o.summary.Changes {
		fmt.Fprintf(writer, "%s:%d\t%d to %d\t%.1fm\t%d\t%s\n",
			change.Path, change.Line, change.OldPriority, change.NewPriority, change.ScoreMinutes, change.EligibleBuildCount, change.Name)
	}
	fmt.Fprintf(writer, "\n%d changed, %d unchanged, %d insufficient, %d unmatched\n",
		len(o.summary.Changes), len(o.summary.Unchanged), len(o.summary.Insufficient), len(o.summary.Unmatched))
	if !o.write && len(o.summary.Changes) > 0 {
		fmt.Fprintln(writer, "The checkout was not modified; pass --write to apply these changes.")
	}
	return writer.Flush()
}

// buildCatalog renders the registered specs, the same source the list command uses.
func buildCatalog() (et.ExtensionTestSpecs, error) {
	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		return nil, fmt.Errorf("build test specs: %w", err)
	}
	return specs, nil
}
