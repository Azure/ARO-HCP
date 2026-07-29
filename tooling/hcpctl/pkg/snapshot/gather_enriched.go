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
	"log/slog"
	"time"
)

// GatherForTestOptions configures a data gathering run for a single test case.
// The caller is responsible for fetching Prow job data, creating the Kusto
// client and Gatherer, and computing sibling test summaries — all of which are
// per-job concerns. This function handles only per-test work: writing test
// logs, running Kusto queries, enriching the manifest, and writing sibling
// test metadata.
type GatherForTestOptions struct {
	// Gatherer is the pre-configured snapshot gatherer backed by a Kusto client.
	Gatherer *Gatherer

	// Test is the specific test result to gather data for.
	Test *TestResult

	// ProwJobURL is the original Prow job URL, written into the manifest.
	ProwJobURL string

	// KustoEndpoint is the Kusto cluster URI (e.g. "https://foo.eastus.kusto.windows.net").
	KustoEndpoint string

	// ServiceDatabase is the Kusto database name for service-side data.
	ServiceDatabase string

	// HCPDatabase is the Kusto database name for HCP-side data.
	HCPDatabase string

	// MonitoringEventsDatabase is the Kusto database name for monitoring events (alerts).
	MonitoringEventsDatabase string

	// ServiceClusterName and ManagementClusterName are AKS cluster names
	// used to filter Kusto queries to only relevant clusters for PR jobs.
	ServiceClusterName    string
	ManagementClusterName string

	// SiblingTests contains metadata for all e2e tests from the same Prow job run.
	SiblingTests []TestSummary

	// OutputDir is the directory where the structured data should be written.
	OutputDir string

	// QueryTimeout is the timeout for individual Kusto queries.
	// A zero value defaults to 10 minutes.
	QueryTimeout time.Duration

	// Concurrency is the maximum number of concurrent Kusto queries.
	// A value of 0 defaults to 4 * runtime.NumCPU().
	Concurrency int

	// NodeConsoleLogs contains VM serial console log files downloaded from
	// the Prow job's GCS artifacts. These are optional — not every test creates VMs.
	NodeConsoleLogs []NodeConsoleLogFile

	// AzureLog is the client-side azure.log from GCS, or nil if not present.
	AzureLog *AzureLogFile
}

// GatherForTestResult contains metadata about what was gathered.
type GatherForTestResult struct {
	// ManifestPath is the path to the manifest.json file in the output directory.
	ManifestPath string

	// DataDir is the root of the structured data directory.
	DataDir string

	// Manifest is the parsed manifest from the snapshot library.
	Manifest *Manifest

	// VerificationReport contains pass/fail/skip results for each query.
	VerificationReport *VerificationReport

	// StartTime is the start of the time window for the test execution.
	StartTime time.Time

	// EndTime is the end of the time window for the test execution.
	EndTime time.Time

	// CleanupStartTime is the time at which the test's cleanup phase began.
	CleanupStartTime time.Time
}

// GatherForTest runs the per-test data gathering pipeline: it writes test
// logs, queries Kusto for traces and state, enriches the manifest with test
// metadata, and writes sibling test summaries.
//
// Per-job setup (Prow artifact download, Kusto client creation, sibling test
// summary computation) must be done by the caller and passed in via opts.
func GatherForTest(ctx context.Context, opts GatherForTestOptions) (*GatherForTestResult, error) {
	test := opts.Test

	if test.ResourceGroup == "" {
		return nil, fmt.Errorf("test %q: no resource group found in test output", test.Name)
	}
	if test.StartTime.IsZero() || test.EndTime.IsZero() {
		return nil, fmt.Errorf("test %q: no start/end time available", test.Name)
	}

	// Write test logs.
	if err := WriteTestLogs(opts.OutputDir, test); err != nil {
		return nil, fmt.Errorf("failed to write test logs: %w", err)
	}

	// Write node console logs (if any).
	if err := WriteNodeConsoleLogs(opts.OutputDir, opts.NodeConsoleLogs); err != nil {
		return nil, fmt.Errorf("failed to write node console logs: %w", err)
	}

	// Write the client-side azure.log (if present).
	if err := WriteAzureLog(opts.OutputDir, opts.AzureLog); err != nil {
		return nil, fmt.Errorf("failed to write azure.log: %w", err)
	}

	// Build the gather input with 5-minute padding.
	startTime := test.StartTime.Add(-5 * time.Minute)
	endTime := test.EndTime.Add(5 * time.Minute)

	queryTimeout := opts.QueryTimeout
	if queryTimeout == 0 {
		queryTimeout = 10 * time.Minute
	}

	input := GatherInput{
		ClusterURI:               opts.KustoEndpoint,
		ServiceDatabase:          opts.ServiceDatabase,
		HCPDatabase:              opts.HCPDatabase,
		MonitoringEventsDatabase: opts.MonitoringEventsDatabase,
		ResourceGroup:            test.ResourceGroup,
		ServiceClusterName:       opts.ServiceClusterName,
		ManagementClusterName:    opts.ManagementClusterName,
		TimeWindow: TimeWindow{
			Start:           startTime,
			End:             endTime,
			SetupFinishTime: test.SetupFinishTime,
		},
		CleanupStartTime: test.CleanupStartTime,
		QueryTimeout:     queryTimeout,
		Concurrency:      opts.Concurrency,
		TestStartTime:    test.TestStartTime,
	}

	// Run the snapshot gatherer.
	manifest, report, err := opts.Gatherer.Gather(ctx, input, opts.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("snapshot gather failed: %w", err)
	}

	// Enrich manifest with test metadata.
	manifest.TestName = test.Name
	manifest.ProwJobURL = opts.ProwJobURL
	for _, cl := range opts.NodeConsoleLogs {
		manifest.NodeConsoleLogs = append(manifest.NodeConsoleLogs, NodeConsoleLog{
			NodeName:    cl.NodeName,
			File:        fmt.Sprintf("node_boot_logs/%s", cl.FileName),
			ArtifactURL: cl.ArtifactURL,
		})
	}
	if opts.AzureLog != nil && len(opts.AzureLog.Content) > 0 {
		manifest.AzureLog = &AzureLog{
			File:        "azure_sdk_log/azure.log",
			ArtifactURL: opts.AzureLog.ArtifactURL,
		}
	}
	if err := WriteManifest(opts.OutputDir, manifest); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}

	// Write sibling test summaries.
	if err := WriteSiblingTests(opts.OutputDir, opts.SiblingTests); err != nil {
		slog.Warn("Failed to write sibling_tests.json; continuing.", "error", err)
	}

	return &GatherForTestResult{
		ManifestPath:       fmt.Sprintf("%s/manifest.json", opts.OutputDir),
		DataDir:            opts.OutputDir,
		Manifest:           manifest,
		VerificationReport: report,
		StartTime:          test.StartTime,
		EndTime:            test.EndTime,
		CleanupStartTime:   test.CleanupStartTime,
	}, nil
}
