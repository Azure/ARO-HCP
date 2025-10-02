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

package mustgather

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/spf13/cobra"
)

func NewCommand(group string) (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:     "must-gather",
		Aliases: []string{"mg"},
		Short:   "Azure Data Explorer must-gather operations",
		GroupID: group,
		Long: `must-gather provides data collection operations for Azure Data Explorer clusters.

This command group includes subcommands for querying Azure Data Explorer instances
and collecting diagnostic data for troubleshooting and analysis.`,
		Example: `  hcpctl must-gather query --kusto-name my-kusto-cluster
  hcpctl must-gather query --kusto-name my-kusto-cluster --output results.json`,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	// Add query subcommand
	queryCmd, err := newQueryCommand()
	if err != nil {
		return nil, err
	}
	cmd.AddCommand(queryCmd)

	return cmd, nil
}

func newQueryCommand() (*cobra.Command, error) {
	opts := DefaultMustGatherOptions()

	cmd := &cobra.Command{
		Use:     "query",
		Aliases: []string{"q"},
		Short:   "Execute queries against Azure Data Explorer",
		Long: `Execute preconfigured queries against Azure Data Explorer clusters.

This command runs diagnostic queries against the specified Azure Data Explorer
cluster and outputs the results to a file for analysis.`,
		Args:             cobra.NoArgs,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuery(cmd.Context(), opts)
		},
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	if err := BindMustGatherOptions(opts, cmd); err != nil {
		return nil, err
	}

	return cmd, nil
}

func runQuery(ctx context.Context, opts *RawMustGatherOptions) error {
	// Validate options
	validated, err := opts.Validate(ctx)
	if err != nil {
		return err
	}

	// Complete options
	completed, err := validated.Complete(ctx)
	if err != nil {
		return err
	}

	// Execute the query operation
	if err := executeQuery(ctx, completed); err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}

	return nil
}

// executeQuery performs the actual query execution against Azure Data Explorer
func executeQuery(ctx context.Context, opts *MustGatherOptions) error {
	// Create Kusto client
	client, err := kusto.NewClient(opts.KustoName)
	if err != nil {
		return fmt.Errorf("failed to create Kusto client: %w", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close Kusto client: %v\n", closeErr)
		}
	}()

	// Get preconfigured queries
	queries := kusto.GetPreconfiguredQueries()

	// Create output file
	file, err := os.Create(opts.OutputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Write header
	fmt.Fprintf(file, "# Must-gather results for Azure Data Explorer cluster: %s\n", opts.KustoName)
	fmt.Fprintf(file, "# Generated at: %s\n", opts.QueryTimeout)
	fmt.Fprintf(file, "# Output format: %s\n\n", opts.OutputFormat)

	// Execute each query
	for i, query := range queries {
		fmt.Printf("Executing query %d/%d: %s\n", i+1, len(queries), getQueryDescription(query))

		result, err := client.ExecuteQuery(ctx, query, opts.QueryTimeout)
		if err != nil {
			fmt.Printf("Query %d failed: %v\n", i+1, err)
			fmt.Fprintf(file, "// Query %d failed: %v\n", i+1, err)
			continue
		}

		fmt.Printf("Query %d completed: %d rows in %v\n", i+1, result.QueryStats.TotalRows, result.QueryStats.ExecutionTime)

		// Write query results to file
		fmt.Fprintf(file, "// Query %d results:\n", i+1)
		fmt.Fprintf(file, "// Execution time: %v, Total rows: %d, Data size: %d bytes\n",
			result.QueryStats.ExecutionTime, result.QueryStats.TotalRows, result.QueryStats.DataSize)

		// Write data based on output format
		if err := writeQueryResults(file, result, string(opts.OutputFormat)); err != nil {
			fmt.Printf("Failed to write results for query %d: %v\n", i+1, err)
			fmt.Fprintf(file, "// Failed to write results: %v\n", err)
		}

		fmt.Fprintf(file, "\n")
	}

	fmt.Printf("Results written to: %s\n", opts.OutputPath)
	return nil
}

// writeQueryResults writes query results to file in the specified format
func writeQueryResults(file *os.File, result *kusto.QueryResult, format string) error {
	switch format {
	case "json":
		return writeJSONResults(file, result)
	case "csv":
		return writeCSVResults(file, result)
	case "table":
		return writeTableResults(file, result)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

// writeJSONResults writes results in JSON format
func writeJSONResults(file *os.File, result *kusto.QueryResult) error {
	// TODO: Implement JSON output formatting
	fmt.Fprintf(file, "// JSON output not yet implemented\n")
	return nil
}

// writeCSVResults writes results in CSV format
func writeCSVResults(file *os.File, result *kusto.QueryResult) error {
	// TODO: Implement CSV output formatting
	fmt.Fprintf(file, "// CSV output not yet implemented\n")
	return nil
}

// writeTableResults writes results in table format
func writeTableResults(file *os.File, result *kusto.QueryResult) error {
	// TODO: Implement table output formatting
	fmt.Fprintf(file, "// Table output not yet implemented\n")
	return nil
}

// getQueryDescription extracts a description from the query comment
func getQueryDescription(query string) string {
	lines := strings.Split(query, "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			return strings.TrimSpace(strings.TrimPrefix(line, "//"))
		}
	}
	return "Unknown query"
}
