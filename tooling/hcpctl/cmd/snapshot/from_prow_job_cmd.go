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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/testlib"
	snapshotpkg "github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"

)

// RawFromProwJobOptions holds the unvalidated CLI options for from-prow-job.
type RawFromProwJobOptions struct {
	URL             string
	TestSelector    string // optional: only gather data for tests whose name contains this substring
	OutputDir       string
	SDPPipelinesDir string // optional: path to a local checkout of the sdp-pipelines repo
	QueryTimeout    time.Duration
	Concurrency     int
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
	cmd.Flags().StringVar(&opts.SDPPipelinesDir, "sdp-pipelines-dir", opts.SDPPipelinesDir, "Path to a local checkout of the sdp-pipelines repo (required for non-PR jobs)")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "Timeout for individual Kusto queries")
	cmd.Flags().IntVar(&opts.Concurrency, "concurrency", opts.Concurrency, "Maximum number of concurrent Kusto queries (0 = 4*NumCPU)")

	if err := cmd.MarkFlagRequired("url"); err != nil {
		return fmt.Errorf("failed to mark url as required: %w", err)
	}
	return nil
}

type validatedFromProwJobOptions struct {
	prowInfo        *snapshotpkg.ProwJobInfo
	testSelector    string
	outputDir       string
	sdpPipelinesDir string
	queryTimeout    time.Duration
	concurrency     int
}

func (o *RawFromProwJobOptions) validate() (*validatedFromProwJobOptions, error) {
	info, err := snapshotpkg.ParseProwURL(o.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid --url: %w", err)
	}
	if o.SDPPipelinesDir != "" {
		fi, err := os.Stat(o.SDPPipelinesDir)
		if err != nil {
			return nil, fmt.Errorf("invalid --sdp-pipelines-dir: %w", err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("--sdp-pipelines-dir %q is not a directory", o.SDPPipelinesDir)
		}
	}
	return &validatedFromProwJobOptions{
		prowInfo:        info,
		testSelector:    o.TestSelector,
		outputDir:       o.OutputDir,
		sdpPipelinesDir: o.SDPPipelinesDir,
		queryTimeout:    o.QueryTimeout,
		concurrency:     o.Concurrency,
	}, nil
}

func (o *validatedFromProwJobOptions) run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	logger.Info("Fetching Prow job data",
		"job", o.prowInfo.JobName,
		"prowID", o.prowInfo.ProwID,
		"isPR", o.prowInfo.IsPullRequest(),
	)

	// Phase 1 (per-job): Resolve Kusto config and download test results.
	jobConfig, err := snapshotpkg.FetchProwJobConfig(ctx, o.prowInfo, o.sdpPipelinesDir)
	if err != nil {
		return fmt.Errorf("failed to fetch Prow job config: %w", err)
	}

	prowResults, err := snapshotpkg.FetchProwJobTestResults(ctx, o.prowInfo)
	if err != nil {
		return fmt.Errorf("failed to fetch Prow job test results: %w", err)
	}

	// When --test is provided, match against all tests regardless of pass/fail.
	// Otherwise, only gather data for failed tests.
	var tests []snapshotpkg.TestResult
	if o.testSelector != "" {
		for _, t := range prowResults.Tests {
			if strings.Contains(t.Name, o.testSelector) {
				tests = append(tests, t)
			}
		}
		if len(tests) == 0 {
			return fmt.Errorf("no tests match selector %q (found %d tests total)", o.testSelector, len(prowResults.Tests))
		}
		logger.Info("Filtered tests by selector", "selector", o.testSelector, "matched", len(tests), "total", len(prowResults.Tests))
	} else {
		for _, t := range prowResults.Tests {
			if t.Failed {
				tests = append(tests, t)
			}
		}
		if len(tests) == 0 {
			logger.Info("No failed tests found in this Prow job")
			return nil
		}
	}

	logger.Info("Processing tests", "count", len(tests))

	// Compute sibling test summaries once for all tests.
	siblingTests := snapshotpkg.ConvertTestResults(prowResults.Tests)

	// Phase 1 (per-job): Create Kusto client from the job's config.
	kustoEndpoint, err := kusto.KustoEndpoint(jobConfig.KustoName, jobConfig.Region)
	if err != nil {
		return fmt.Errorf("failed to create Kusto endpoint: %w", err)
	}

	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		AdditionallyAllowedTenants:   []string{"*"},
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	kcsb := azkustodata.NewConnectionStringBuilder(kustoEndpoint.String())
	kcsb = kcsb.WithTokenCredential(cred)
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

	// Phase 2 (per-test): Gather an enriched snapshot for each test.
	var gatherErrors []error
	for i := range tests {
		test := &tests[i]
		testName := testlib.SanitizeTestName(test.Name)
		testOutputDir := filepath.Join(o.outputDir, o.prowInfo.JobName, o.prowInfo.ProwID, testName)

		logger.Info("Gathering snapshot for test",
			"test", test.Name,
			"resourceGroup", test.ResourceGroup,
			"outputDir", testOutputDir,
		)

		// Fetch test artifact files (eg. console logs) from GCS and write to
		// test_artifacts directory.
		artifactFiles, err := snapshotpkg.FetchTestArtifacts(ctx, o.prowInfo, prowResults.ArtifactDir, prowResults.TestStep, test.Name)
		if err != nil {
			return fmt.Errorf("failed to fetch artifacts of %s test: %w", test.Name, err)
		}
		err = snapshotpkg.WriteTestArtifacts(testOutputDir, artifactFiles)
		if err != nil {
			return fmt.Errorf("failed to write artifacts of %s test: %w", test.Name, err)
		}

		result, err := snapshotpkg.GatherForTest(ctx, snapshotpkg.GatherForTestOptions{
			Gatherer:              gatherer,
			Test:                  test,
			ProwJobURL:            o.prowInfo.URL,
			KustoEndpoint:         kustoEndpoint.String(),
			ServiceDatabase:       jobConfig.ServiceDatabase,
			HCPDatabase:           jobConfig.HCPDatabase,
			ServiceClusterName:    jobConfig.ServiceClusterName,
			ManagementClusterName: jobConfig.ManagementClusterName,
			SiblingTests:          siblingTests,
			OutputDir:             testOutputDir,
			QueryTimeout:          o.queryTimeout,
			Concurrency:           o.concurrency,
		})
		if err != nil {
			logger.Error(err, "Failed to gather snapshot for test", "test", test.Name)
			gatherErrors = append(gatherErrors, fmt.Errorf("test %q: %w", test.Name, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}

		logger.Info("Snapshot complete for test",
			"test", test.Name,
			"phases", len(result.Manifest.Phases),
		)
	}

	if len(gatherErrors) > 0 {
		logger.Info("Some tests had errors", "errors", len(gatherErrors), "total", len(tests))
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
		Short: "Gather enriched diagnostic snapshots for tests in a Prow job",
		Long: `Gather structured, enriched diagnostic snapshots by downloading Prow job
artifacts from GCS, resolving the Kusto connection info, identifying failed
tests, and running the full data gathering pipeline for each one.

For non-PR (EV2-triggered) jobs, the Kusto config is resolved by downloading
prowjob.json to extract ev2.rollout/* annotations, then reading the rendered
config from the sdp-pipelines repo at the annotated commit SHA. The
--sdp-pipelines-dir flag must point to a local checkout of the sdp-pipelines
repo.

For PR jobs, the Kusto config is extracted from the
aro-hcp-provision-environment step artifact in GCS.

Each test snapshot includes Kusto query results, test logs (error and output),
sibling test metadata, and a manifest.json index suitable for automated
analysis via the analyze subcommand.

The resource group and time window are extracted from each test's output logs
and metadata.

Use --test to filter to a specific test by name substring.`,
		Example: `  # Gather snapshots for all failed tests in an EV2-triggered job
  hcpctl snapshot from-prow-job \
    --url https://prow.ci.openshift.org/view/gs/test-platform-results/logs/branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel/1234567890 \
    --sdp-pipelines-dir /path/to/sdp-pipelines

  # Gather snapshot for a specific test
  hcpctl snapshot from-prow-job \
    --url https://prow.ci.openshift.org/view/gs/test-platform-results/logs/branch-ci-Azure-ARO-HCP-main-e2e-prod-e2e-parallel/1234567890 \
    --sdp-pipelines-dir /path/to/sdp-pipelines \
    --test TestNodePoolCreation

  # PR job (no --sdp-pipelines-dir needed)
  hcpctl snapshot from-prow-job \
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
