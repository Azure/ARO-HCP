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

// Package priorities reviews E2E start-priority scores collected from Kusto.
// It is read-only: refreshing the source labels is a separate command.
package priorities

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/pkg/citests"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// kustoEndpointPart bounds the live cluster and region flags to DNS labels.
var kustoEndpointPart = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// options holds the resolved command configuration.
type options struct {
	cluster     string
	region      string
	database    string
	environment string
	from        string
	to          string
	since       time.Duration
	jobFilter   string
	saveInput   string
	input       string
	timeout     time.Duration
	minSamples  int
	output      string
	now         func() time.Time
	out         io.Writer
	errOut      io.Writer
	selection   citests.Selection
	endpoint    string
}

// NewCommand builds the priorities command.
func NewCommand() *cobra.Command {
	opts := &options{now: time.Now}
	cmd := &cobra.Command{
		Use:   "priorities",
		Short: "Review E2E start-priority scores from Kusto or saved observations",
		Long: `Review environment-normalized E2E test durations and the start priorities they imply.
Live selection uses CI run completion time in [from,to), not test timestamps.
Each spec's mean per environment is divided by that environment's median, the best
ratio is multiplied by the pooled global median, and the resulting minutes select a
priority band. Skipped, failed and unprocessed records never count as zero durations.
This command never edits test sources; use refresh-priorities for that.`,
		Example: `  aro-hcp-tests priorities --kusto hcp-dev-us-2 --region eastus2 --since 720h --output json
  aro-hcp-tests priorities --kusto hcp-dev-us-2 --region eastus2 --from 2026-01-01T00:00:00Z --to 2026-02-01T00:00:00Z --save-input observations.json
  aro-hcp-tests priorities --input observations.json --output json`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(cmd); err != nil {
				return err
			}
			if err := opts.Complete(cmd); err != nil {
				return err
			}
			return opts.Run(cmd.Context())
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.cluster, "kusto", "", "Kusto cluster name (live mode)")
	flags.StringVar(&opts.region, "region", "", "Kusto region (live mode)")
	flags.StringVar(&opts.database, "database", "ServiceLogs", "Kusto database (live mode)")
	flags.StringVar(&opts.environment, "environment", "", "Filter classified job environment: dev, int, stg, prod, or unknown; empty selects all in this database")
	flags.StringVar(&opts.from, "from", "", "Inclusive run completion timestamp in RFC3339")
	flags.StringVar(&opts.to, "to", "", "Exclusive run completion timestamp in RFC3339")
	flags.DurationVar(&opts.since, "since", 0, "Window ending now, e.g. 720h; excludes --from/--to")
	flags.StringVar(&opts.jobFilter, "job-filter", "e2e-parallel", "Case-sensitive literal substring of job name; empty removes the filter")
	flags.StringVar(&opts.saveInput, "save-input", "", "Atomically save complete replay input to a new private file; never overwrite")
	flags.StringVar(&opts.input, "input", "", "Replay saved observations without live flags or Azure credentials")
	flags.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "Total live collection timeout, greater than zero and at most 30m")
	flags.IntVar(&opts.minSamples, "min-samples", citests.DefaultMinSamples, "Minimum distinct eligible builds per spec and environment before scoring it")
	flags.StringVar(&opts.output, "output", "table", "Report format: table or json")
	return cmd
}

// Validate rejects invalid flag combinations before any authentication happens.
func (o *options) Validate(cmd *cobra.Command) error {
	if o.saveInput == "-" {
		return fmt.Errorf("--save-input requires a file path, not stdout")
	}
	switch o.output {
	case "table", "json":
	default:
		return fmt.Errorf("--output must be table or json")
	}
	if o.minSamples < citests.DefaultMinSamples {
		return fmt.Errorf("--min-samples must be at least %d", citests.DefaultMinSamples)
	}
	if o.input != "" {
		for _, name := range []string{"kusto", "region", "database", "environment", "from", "to", "since", "job-filter", "timeout"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--input is mutually exclusive with --%s; saved selection is retained", name)
			}
		}
		return nil
	}
	if !kustoEndpointPart.MatchString(o.cluster) || !kustoEndpointPart.MatchString(o.region) {
		return fmt.Errorf("live mode requires valid --kusto and --region DNS names")
	}
	if o.database == "" {
		return fmt.Errorf("live mode requires --database")
	}
	switch o.environment {
	case "", citests.EnvironmentDev, citests.EnvironmentInt, citests.EnvironmentStg, citests.EnvironmentProd, citests.EnvironmentUnknown:
	default:
		return fmt.Errorf("--environment must be dev, int, stg, prod, or unknown (or empty for all)")
	}
	if o.timeout <= 0 || o.timeout > 30*time.Minute {
		return fmt.Errorf("--timeout must be greater than zero and at most 30m")
	}
	if cmd.Flags().Changed("since") {
		if cmd.Flags().Changed("from") || cmd.Flags().Changed("to") {
			return fmt.Errorf("--since is mutually exclusive with --from and --to")
		}
		if o.since <= 0 {
			return fmt.Errorf("--since must be greater than zero")
		}
		return nil
	}
	from, err := time.Parse(time.RFC3339, o.from)
	if err != nil {
		return fmt.Errorf("--from must be RFC3339 (or use --since): %w", err)
	}
	to, err := time.Parse(time.RFC3339, o.to)
	if err != nil {
		return fmt.Errorf("--to must be RFC3339 (or use --since): %w", err)
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return fmt.Errorf("window requires nonzero --from < --to")
	}
	return nil
}

// Complete resolves the selection without authenticating in replay mode.
func (o *options) Complete(cmd *cobra.Command) error {
	o.out, o.errOut = cmd.OutOrStdout(), cmd.ErrOrStderr()
	if o.input != "" {
		return nil
	}
	endpoint, err := kusto.KustoEndpoint(o.cluster, o.region)
	if err != nil {
		return fmt.Errorf("resolve Kusto endpoint: %w", err)
	}
	o.endpoint = endpoint.String()
	var from, to time.Time
	if o.since > 0 {
		to = o.now().UTC()
		from = to.Add(-o.since)
	} else {
		from, err = time.Parse(time.RFC3339, o.from)
		if err != nil {
			return fmt.Errorf("resolve --from: %w", err)
		}
		to, err = time.Parse(time.RFC3339, o.to)
		if err != nil {
			return fmt.Errorf("resolve --to: %w", err)
		}
	}
	o.selection = citests.Selection{
		Origin:      o.endpoint + "/" + o.database,
		From:        from.UTC(),
		To:          to.UTC(),
		JobFilter:   o.jobFilter,
		Environment: o.environment,
	}
	return o.selection.Validate()
}

// Run collects or replays observations, scores them and writes the report.
func (o *options) Run(ctx context.Context) error {
	bundle, err := o.load(ctx)
	if err != nil {
		return err
	}
	report, err := citests.ScorePriorities(bundle, citests.PriorityOptions{MinSamples: o.minSamples})
	if err != nil {
		return err
	}
	if o.saveInput != "" {
		if err := citests.SaveBundle(o.saveInput, bundle); err != nil {
			return err
		}
		fmt.Fprintf(o.errOut, "Saved observations replay input to %s.\n", o.saveInput)
	}
	if o.output == "json" {
		encoder := json.NewEncoder(o.out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("write JSON report: %w", err)
		}
		return nil
	}
	return writeTable(o.out, report)
}

// load reads saved observations or collects them live under one deadline.
func (o *options) load(ctx context.Context) (citests.Bundle, error) {
	if o.input != "" {
		file, err := os.Open(o.input)
		if err != nil {
			return citests.Bundle{}, fmt.Errorf("open saved observations: %w", err)
		}
		defer func() { _ = file.Close() }()
		return citests.ReadBundle(file)
	}
	endpoint, err := url.Parse(o.endpoint)
	if err != nil {
		return citests.Bundle{}, fmt.Errorf("parse Kusto endpoint: %w", err)
	}
	client, err := kusto.NewClient(endpoint, o.timeout)
	if err != nil {
		return citests.Bundle{}, fmt.Errorf("create Kusto client: %w", err)
	}
	defer func() { _ = client.Close() }()
	return citests.Collect(ctx, client, o.database, o.selection, o.timeout)
}

// writeTable renders the review with its selection and coverage above the rows.
func writeTable(out io.Writer, report citests.PriorityReport) error {
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	globalMedian := "none"
	if report.GlobalMedianMinutes != nil {
		globalMedian = formatMinutes(*report.GlobalMedianMinutes)
	}
	fmt.Fprintf(writer, "Selection\t%s\n", report.Selection.Origin)
	fmt.Fprintf(writer, "Window\t%s to %s (%s)\n", report.Selection.From.Format(time.RFC3339), report.Selection.To.Format(time.RFC3339), report.Selection.JobFilter)
	if report.Selection.Environment != "" {
		fmt.Fprintf(writer, "Environment filter\t%s\n", report.Selection.Environment)
	}
	fmt.Fprintf(writer, "Algorithm\t%s (min samples %d, global median %s)\n", report.Algorithm, report.MinSamples, globalMedian)
	fmt.Fprintf(writer, "Coverage\t%d selected, %d processed, %d green builds\n",
		report.Quality.SelectedBuildCount, report.Quality.ProcessedBuildCount, report.Quality.GreenBuildCount)
	for _, environment := range report.Environments {
		latest := "none"
		if environment.LatestEligibleCompletion != nil {
			latest = environment.LatestEligibleCompletion.Format(time.RFC3339)
		}
		fmt.Fprintf(writer, "  %s\tmedian %s, %d builds, %d samples, latest %s\n",
			environment.Environment, formatMinutes(environment.MedianMinutes),
			environment.EligibleBuildCount, environment.EligibleSampleCount, latest)
	}
	if report.Quality.Reason != "" {
		fmt.Fprintf(writer, "Quality\t%s\n", report.Quality.Reason)
	}
	if excluded := formatExclusions(report.Quality.Exclusions); excluded != "" {
		fmt.Fprintf(writer, "Excluded\t%s\n", excluded)
	}
	fmt.Fprintf(writer, "\nTEST\tPRIORITY\tSCORE\tBUILDS\tENVIRONMENTS\tSTATUS\n")
	for _, row := range report.Tests {
		priority, score := "-", "-"
		if row.ProposedPriority != nil {
			priority = strconv.Itoa(*row.ProposedPriority)
		}
		if row.ScoreMinutes != nil {
			score = formatMinutes(*row.ScoreMinutes)
		}
		// BUILDS and ENVIRONMENTS describe the eligible evidence for the row. For
		// a scored row they count only the strata that produced the score, so an
		// insufficient stratum mentioned here is never mistaken for a contributor.
		builds := 0
		observed := []string{}
		for _, environment := range row.Environments {
			if row.Status == citests.StatusScored && !environment.Sufficient {
				continue
			}
			builds += environment.EligibleBuildCount
			observed = append(observed, environment.Environment)
		}
		sort.Strings(observed)
		if len(observed) == 0 {
			observed = []string{"-"}
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%s\t%s\n", row.Name, priority, score, builds,
			strings.Join(observed, ","), row.Status)
	}
	return writer.Flush()
}

// formatMinutes renders a duration in minutes with one decimal place.
func formatMinutes(minutes float64) string {
	return strconv.FormatFloat(minutes, 'f', 1, 64) + "m"
}

// formatExclusions renders the non-zero exclusion reasons so a reader can see
// why the scored population is smaller than the selected one.
func formatExclusions(exclusions map[string]int) string {
	reasons := make([]string, 0, len(exclusions))
	for reason, count := range exclusions {
		if count > 0 {
			reasons = append(reasons, fmt.Sprintf("%s: %d", reason, count))
		}
	}
	sort.Strings(reasons)
	return strings.Join(reasons, ", ")
}
