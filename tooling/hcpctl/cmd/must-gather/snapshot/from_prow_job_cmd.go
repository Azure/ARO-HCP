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

package snapshot

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-kusto-go/azkustodata"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	snapshotpkg "github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

// RawFromProwJobOptions holds the unvalidated CLI options for from-prow-job.
type RawFromProwJobOptions struct {
	URL          string
	TestSelector string // optional: only gather data for tests whose name contains this substring
	OutputDir    string
	QueryTimeout time.Duration
}

func defaultFromProwJobOptions() *RawFromProwJobOptions {
	return &RawFromProwJobOptions{
		QueryTimeout: 5 * time.Minute,
		OutputDir:    fmt.Sprintf("snapshot-%s", time.Now().Format("20060102-150405")),
	}
}

func bindFromProwJobOptions(opts *RawFromProwJobOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.URL, "url", opts.URL, "Prow job URL (required)")
	cmd.Flags().StringVar(&opts.TestSelector, "test", opts.TestSelector, "Only gather data for tests whose name contains this substring")
	cmd.Flags().StringVar(&opts.OutputDir, "output-dir", opts.OutputDir, "Directory to write snapshot output")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "Timeout for individual Kusto queries")

	if err := cmd.MarkFlagRequired("url"); err != nil {
		return fmt.Errorf("failed to mark url as required: %w", err)
	}
	return nil
}

type validatedFromProwJobOptions struct {
	prowInfo     *snapshotpkg.ProwJobInfo
	testSelector string
	outputDir    string
	queryTimeout time.Duration
}

func (o *RawFromProwJobOptions) validate() (*validatedFromProwJobOptions, error) {
	info, err := snapshotpkg.ParseProwURL(o.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid --url: %w", err)
	}
	return &validatedFromProwJobOptions{
		prowInfo:     info,
		testSelector: o.TestSelector,
		outputDir:    o.OutputDir,
		queryTimeout: o.QueryTimeout,
	}, nil
}

func (o *validatedFromProwJobOptions) run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("Fetching Prow job data",
		"job", o.prowInfo.JobName,
		"prowID", o.prowInfo.ProwID,
		"isPR", o.prowInfo.IsPR,
	)

	// Phase 1: Download Prow artifacts and parse config + failed tests.
	jobConfig, failedTests, err := snapshotpkg.FetchProwJobData(ctx, o.prowInfo)
	if err != nil {
		return fmt.Errorf("failed to fetch Prow job data: %w", err)
	}

	if len(failedTests) == 0 {
		logger.Info("No failed tests found in this Prow job")
		return nil
	}

	// Apply test selector if provided.
	if o.testSelector != "" {
		var filtered []snapshotpkg.FailedTest
		for _, t := range failedTests {
			if strings.Contains(t.Name, o.testSelector) {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("no failed tests match selector %q (found %d failed tests total)", o.testSelector, len(failedTests))
		}
		logger.Info("Filtered failed tests", "selector", o.testSelector, "matched", len(filtered), "total", len(failedTests))
		failedTests = filtered
	}

	logger.Info("Processing failed tests", "count", len(failedTests))

	// Phase 2: Create Kusto client from the job's config.
	kustoEndpoint, err := kusto.KustoEndpoint(jobConfig.KustoName, jobConfig.Region)
	if err != nil {
		return fmt.Errorf("failed to create Kusto endpoint: %w", err)
	}

	kcsb := azkustodata.NewConnectionStringBuilder(kustoEndpoint.String())
	kcsb = kcsb.WithDefaultAzureCredential()
	kustoClient, err := azkustodata.New(kcsb)
	if err != nil {
		return fmt.Errorf("failed to create Kusto client: %w", err)
	}
	defer func() {
		if err := kustoClient.Close(); err != nil {
			logger.Error(err, "Failed to close Kusto client")
		}
	}()

	gatherer := snapshotpkg.NewGatherer(kustoClient)

	// Phase 3: For each failed test, gather a snapshot.
	var gatherErrors []error
	for _, test := range failedTests {
		testName := snapshotpkg.SanitizeTestName(test.Name)
		testOutputDir := filepath.Join(o.outputDir, o.prowInfo.JobName, o.prowInfo.ProwID, testName)

		if test.ResourceGroup == "" {
			logger.Info("Skipping test: could not extract resource group from test output",
				"test", test.Name,
			)
			gatherErrors = append(gatherErrors, fmt.Errorf("test %q: no resource group found in test output", test.Name))
			continue
		}

		// Determine time window: use test start/end times, with padding.
		startTime := test.StartTime
		endTime := test.EndTime
		if startTime.IsZero() || endTime.IsZero() {
			logger.Info("Skipping test: no start/end time available",
				"test", test.Name,
			)
			gatherErrors = append(gatherErrors, fmt.Errorf("test %q: no start/end time available", test.Name))
			continue
		}
		// Add 5-minute padding on each side to capture setup/teardown activity.
		startTime = startTime.Add(-5 * time.Minute)
		endTime = endTime.Add(5 * time.Minute)

		logger.Info("Gathering snapshot for test",
			"test", test.Name,
			"resourceGroup", test.ResourceGroup,
			"startTime", startTime.Format(time.RFC3339),
			"endTime", endTime.Format(time.RFC3339),
			"outputDir", testOutputDir,
		)

		input := snapshotpkg.GatherInput{
			ClusterURI:      kustoEndpoint.String(),
			ServiceDatabase: jobConfig.ServiceDatabase,
			HCPDatabase:     jobConfig.HCPDatabase,
			ResourceGroup:   test.ResourceGroup,
			TimeWindow: snapshotpkg.TimeWindow{
				Start: startTime,
				End:   endTime,
			},
			QueryTimeout: o.queryTimeout,
		}

		manifest, _, err := gatherer.Gather(ctx, input, testOutputDir)
		if err != nil {
			logger.Error(err, "Failed to gather snapshot for test", "test", test.Name)
			gatherErrors = append(gatherErrors, fmt.Errorf("test %q: %w", test.Name, err))
			continue
		}

		// Set the test name in the manifest.
		manifest.TestName = test.Name
		manifest.ProwJobURL = o.prowInfo.URL

		// Re-write the manifest with the test name populated.
		if err := snapshotpkg.WriteManifest(testOutputDir, manifest); err != nil {
			logger.Error(err, "Failed to write manifest", "test", test.Name)
			gatherErrors = append(gatherErrors, fmt.Errorf("test %q: failed to write manifest: %w", test.Name, err))
			continue
		}

		logger.Info("Snapshot complete for test",
			"test", test.Name,
			"resources", len(manifest.Resources),
		)
	}

	if len(gatherErrors) > 0 {
		logger.Info("Some tests had errors", "errors", len(gatherErrors), "total", len(failedTests))
		for _, err := range gatherErrors {
			logger.Error(err, "Test error")
		}
	}

	return nil
}

func newFromProwJobCommand() (*cobra.Command, error) {
	opts := defaultFromProwJobOptions()
	cmd := &cobra.Command{
		Use:   "from-prow-job",
		Short: "Gather diagnostic snapshots for failed tests in a Prow job",
		Long: `Gather structured diagnostic snapshots by downloading Prow job artifacts from
GCS, parsing the job's config.yaml for Kusto connection info, identifying
failed tests, and running the data gathering pipeline for each one.

The Kusto cluster, region, and database names are automatically extracted
from the job's config.yaml artifact. The resource group and time window
are extracted from each test's output logs and metadata.

Use --test to filter to a specific test by name substring.`,
		Example: `  # Gather snapshots for all failed tests in a periodic job
  hcpctl must-gather snapshot from-prow-job \
    --url https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-Azure-ARO-HCP-main-aro-hcp-e2e-parallel/1234567890

  # Gather snapshot for a specific test
  hcpctl must-gather snapshot from-prow-job \
    --url https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-Azure-ARO-HCP-main-aro-hcp-e2e-parallel/1234567890 \
    --test TestNodePoolCreation

  # PR job
  hcpctl must-gather snapshot from-prow-job \
    --url https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/Azure_ARO-HCP/9999/pull-ci-Azure-ARO-HCP-main-aro-hcp-e2e-parallel/1234567890`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			validated, err := opts.validate()
			if err != nil {
				return err
			}
			return validated.run(cmd.Context())
		},
	}
	if err := bindFromProwJobOptions(opts, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}
