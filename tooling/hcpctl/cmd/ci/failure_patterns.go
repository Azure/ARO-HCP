// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/cifailures"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

var kustoEndpointPart = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

type failurePatternsOptions struct {
	cluster     string
	region      string
	database    string
	environment string
	from        string
	to          string
	since       time.Duration
	source      string
	jobFilter   string
	saveInput   string
	input       string
	timeout     time.Duration
	now         func() time.Time
	out         io.Writer
	errOut      io.Writer
	metadata    cifailures.Metadata
	endpoint    string
}

func newFailurePatternsCommand() *cobra.Command {
	opts := &failurePatternsOptions{now: time.Now}
	cmd := &cobra.Command{
		Use:   "failure-patterns",
		Short: "Extract deterministic CI failure patterns from Kusto or saved input",
		Long: `Extract complete failure findings for each selected CI input occurrence.
Live selection uses CI run completion time in [from,to), not test timestamps.
By default, include all environments in this database and jobs containing e2e-parallel.
Output is a JSON report with a grouped review index and complete per-input results.
Replay retains the saved scope without Azure authentication. Saved raw input
and report output contain exact evidence and may contain secrets; normalization is not redaction.`,
		Example: `  hcpctl ci failure-patterns --kusto hcp-dev-us-2 --region eastus2 --since 24h --save-input failures.json > report.json
  hcpctl ci failure-patterns --input failures.json > replay.json`,
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
	flags.DurationVar(&opts.since, "since", 0, "Window ending now, e.g. 24h or 168h; excludes --from/--to")
	flags.StringVar(&opts.source, "source", "", "Exact source filter; empty selects all sources including historical unspecified values")
	flags.StringVar(&opts.jobFilter, "job-filter", "e2e-parallel", "Case-sensitive literal substring of job name; empty removes the filter")
	flags.StringVar(&opts.saveInput, "save-input", "", "Atomically save complete replay input to a new private file; never overwrite")
	flags.StringVar(&opts.input, "input", "", "Replay saved input without live flags or Azure credentials")
	flags.DurationVar(&opts.timeout, "timeout", 2*time.Minute, "Total live collection timeout, greater than zero and at most 30m")
	return cmd
}

func (o *failurePatternsOptions) Validate(cmd *cobra.Command) error {
	if o.saveInput == "-" {
		return fmt.Errorf("--save-input requires a file path, not stdout")
	}
	if o.input != "" {
		for _, name := range []string{"kusto", "region", "database", "environment", "from", "to", "since", "source", "job-filter", "timeout"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--input is mutually exclusive with --%s; saved metadata is retained", name)
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
	case "", "dev", "int", "stg", "prod", "unknown":
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

func (o *failurePatternsOptions) Complete(cmd *cobra.Command) error {
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
	o.metadata = cifailures.Metadata{Origin: o.endpoint + "/" + o.database, Environment: o.environment,
		Window: cifailures.Window{From: from.UTC(), To: to.UTC()}, Filters: cifailures.Filters{Source: o.source, JobFilter: o.jobFilter}}
	return o.metadata.Validate()
}

func (o *failurePatternsOptions) Run(ctx context.Context) error {
	bundle, err := o.load(ctx)
	if err != nil {
		return err
	}
	report, err := cifailures.BuildReport(bundle)
	if err != nil {
		return err
	}
	if o.saveInput != "" {
		if err := cifailures.SaveBundle(o.saveInput, bundle); err != nil {
			return err
		}
		fmt.Fprintf(o.errOut, "Saved raw replay input to %s (contains unredacted evidence).\n", o.saveInput)
	}
	encoder := json.NewEncoder(o.out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	return nil
}

func (o *failurePatternsOptions) load(ctx context.Context) (cifailures.Bundle, error) {
	if o.input != "" {
		file, err := os.Open(o.input)
		if err != nil {
			return cifailures.Bundle{}, fmt.Errorf("open replay input: %w", err)
		}
		defer func() { _ = file.Close() }()
		return cifailures.ReadBundle(file)
	}
	endpoint, err := url.Parse(o.endpoint)
	if err != nil {
		return cifailures.Bundle{}, fmt.Errorf("parse Kusto endpoint: %w", err)
	}
	client, err := kusto.NewClient(endpoint, o.timeout)
	if err != nil {
		return cifailures.Bundle{}, fmt.Errorf("create Kusto client: %w", err)
	}
	defer func() { _ = client.Close() }()
	return cifailures.Collect(ctx, client, o.database, o.metadata, o.timeout)
}
