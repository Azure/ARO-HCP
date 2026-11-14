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

// Package refreshpriorities applies priority proposals to the exact E2E test
// source declarations. It performs no Kusto, GitHub or authentication work.
package refreshpriorities

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"

	"github.com/Azure/ARO-HCP/test/internal/priorityrefresh"
	"github.com/Azure/ARO-HCP/test/pkg/citests"
)

// options holds the resolved command configuration.
type options struct {
	reportPath  string
	catalogPath string
	verifyPath  string
	repo        string
	write       bool
	out         io.Writer
	errOut      io.Writer
}

// NewCommand builds the refresh-priorities command.
func NewCommand() *cobra.Command {
	opts := &options{}
	cmd := &cobra.Command{
		Use:   "refresh-priorities",
		Short: "Apply reviewed start-priority proposals to the E2E test sources",
		Long: `Map every scored proposal in a priorities report onto the exact labels.StartPriority
call that declares it, then optionally write the change.

Without --write the command only previews the plan and never touches the sources.
A single unmappable proposal aborts the whole batch before any write. The catalog
is produced by "aro-hcp-tests list tests --output json" from the checkout being edited.
--verify-catalog checks a rebuilt catalog against the same plan instead of writing.`,
		Example: `  aro-hcp-tests refresh-priorities --report report.json --catalog before.json --repo .
  aro-hcp-tests refresh-priorities --report report.json --catalog before.json --repo . --write
  aro-hcp-tests refresh-priorities --report report.json --catalog before.json --verify-catalog after.json --repo .`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			opts.out, opts.errOut = cmd.OutOrStdout(), cmd.ErrOrStderr()
			return opts.Run()
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.reportPath, "report", "", "Priority report produced by the priorities command (required)")
	flags.StringVar(&opts.catalogPath, "catalog", "", "Before catalog produced by list tests --output json (required)")
	flags.StringVar(&opts.verifyPath, "verify-catalog", "", "After catalog to verify against the plan; mutually exclusive with --write")
	flags.StringVar(&opts.repo, "repo", ".", "Repository checkout whose test/e2e sources are mapped")
	flags.BoolVar(&opts.write, "write", false, "Write the mapped changes; without it the plan is only previewed")
	return cmd
}

// Validate rejects invalid invocations before reading any input.
func (o *options) Validate() error {
	if o.reportPath == "" {
		return fmt.Errorf("--report is required")
	}
	if o.catalogPath == "" {
		return fmt.Errorf("--catalog is required")
	}
	if o.write && o.verifyPath != "" {
		return fmt.Errorf("--write and --verify-catalog are mutually exclusive")
	}
	return nil
}

// Run plans, and either previews, writes or verifies the changes.
func (o *options) Run() error {
	report, err := readReport(o.reportPath)
	if err != nil {
		return err
	}
	before, err := readCatalog(o.catalogPath)
	if err != nil {
		return err
	}
	// Verification compares catalogs only, so it never maps sources from a
	// checkout whose lines the plan already changed.
	var changes priorityrefresh.ChangeSet
	if o.verifyPath != "" {
		changes, err = priorityrefresh.Expected(before, report)
	} else {
		changes, err = priorityrefresh.Plan(o.repo, before, report)
	}
	if err != nil {
		return err
	}
	applied := false
	if o.write {
		if err := priorityrefresh.Apply(changes); err != nil {
			return err
		}
		applied = true
	}
	if o.verifyPath != "" {
		after, err := readCatalog(o.verifyPath)
		if err != nil {
			return err
		}
		if err := priorityrefresh.Verify(before, after, changes); err != nil {
			return err
		}
		fmt.Fprintf(o.errOut, "Verified the rebuilt catalog against %d planned changes.\n", changes.Len())
	}
	summary := changes.Summary(applied)
	if err := writeTable(o.errOut, summary, o.verifyPath == ""); err != nil {
		return err
	}
	encoder := json.NewEncoder(o.out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(summary); err != nil {
		return fmt.Errorf("write JSON summary: %w", err)
	}
	if applied && len(summary.Changes) > 0 {
		fmt.Fprintf(o.errOut, "Wrote %d source changes; the checkout is now dirty by design.\n", len(summary.Changes))
	}
	return nil
}

// readReport decodes exactly one priorities report object.
func readReport(path string) (citests.PriorityReport, error) {
	file, err := os.Open(path)
	if err != nil {
		return citests.PriorityReport{}, fmt.Errorf("open report: %w", err)
	}
	defer func() { _ = file.Close() }()
	var report citests.PriorityReport
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return citests.PriorityReport{}, fmt.Errorf("decode report: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return citests.PriorityReport{}, fmt.Errorf("report must contain exactly one JSON object")
	}
	return report, nil
}

// readCatalog decodes the openshift-tests-extension catalog of one checkout.
func readCatalog(path string) (et.ExtensionTestSpecs, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open catalog: %w", err)
	}
	defer func() { _ = file.Close() }()
	var specs et.ExtensionTestSpecs
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&specs); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("catalog must contain exactly one JSON array")
	}
	return specs, nil
}

// writeTable renders the plan for a human reviewer. Source paths are shown only
// when the plan mapped them; verification works from catalogs alone.
func writeTable(out io.Writer, summary priorityrefresh.Summary, withPaths bool) error {
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "Planned source changes\t%d (applied: %t)\n", len(summary.Changes), summary.Applied)
	if len(summary.Changes) > 0 && withPaths {
		fmt.Fprintf(writer, "\nPATH\tLINE\tPRIORITY\tSCORE\tBUILDS\tTEST\n")
		for _, change := range summary.Changes {
			fmt.Fprintf(writer, "%s\t%d\t%d -> %d\t%.1fm\t%d\t%s\n", change.Path, change.Line,
				change.OldPriority, change.NewPriority, change.ScoreMinutes, change.EligibleBuildCount, change.Name)
		}
	} else if len(summary.Changes) > 0 {
		fmt.Fprintf(writer, "\nPRIORITY\tSCORE\tBUILDS\tTEST\n")
		for _, change := range summary.Changes {
			fmt.Fprintf(writer, "%d -> %d\t%.1fm\t%d\t%s\n",
				change.OldPriority, change.NewPriority, change.ScoreMinutes, change.EligibleBuildCount, change.Name)
		}
	}
	fmt.Fprintf(writer, "\nUnchanged\t%d\n", len(summary.Unchanged))
	fmt.Fprintf(writer, "Insufficient\t%d\n", len(summary.Insufficient))
	fmt.Fprintf(writer, "Unmatched\t%d\n", len(summary.Unmatched))
	for _, name := range summary.Unmatched {
		fmt.Fprintf(writer, "  retired or unknown\t%s\n", name)
	}
	reported := len(summary.Changes) + len(summary.Unchanged) + len(summary.Insufficient) + len(summary.Unmatched)
	fmt.Fprintf(writer, "Reported rows\t%s\n", strconv.Itoa(reported))
	return writer.Flush()
}
