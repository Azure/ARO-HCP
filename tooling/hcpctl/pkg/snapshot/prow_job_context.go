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

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

// ProwJobContext holds pre-computed, per-job state derived from a Prow job URL.
// It owns the Kusto client and gatherer, so callers must call Close when done.
//
// The typical flow is:
//
//	pjCtx, err := snapshot.NewProwJobContext(ctx, prowURL, cred, sdpPipelinesDir)
//	if err != nil { ... }
//	defer pjCtx.Close()
//
//	for _, test := range pjCtx.AllTests {
//	    opts := pjCtx.GatherOptionsForTest(ctx, &test)
//	    opts.OutputDir = "..."
//	    result, err := snapshot.GatherForTest(ctx, opts)
//	}
type ProwJobContext struct {
	// ProwInfo is the parsed Prow job metadata (job name, run ID, GCS prefix).
	ProwInfo *ProwJobInfo

	// JobConfig is the resolved Kusto and cluster configuration for this job.
	JobConfig *ProwJobConfig

	// AllTests contains every test result from this Prow job run.
	AllTests []TestResult

	// SiblingTests contains summary metadata for all tests, suitable for
	// writing into a snapshot's sibling_tests.json.
	SiblingTests []TestSummary

	// KustoEndpoint is the fully-qualified Kusto cluster URI.
	KustoEndpoint string

	gatherer    *Gatherer
	kustoClient *azkustodata.Client
}

// NewProwJobContext resolves all per-job state from a Prow job URL: it parses
// the URL, fetches the job configuration and test results from GCS, creates a
// Kusto client, and builds a snapshot Gatherer.
//
// sdpPipelinesDir is an optional path to a local checkout of the sdp-pipelines
// repo, used to resolve Kusto configuration for non-PR jobs. It may be empty.
//
// The caller must call Close on the returned context when done.
func NewProwJobContext(ctx context.Context, prowURL string, cred azcore.TokenCredential, sdpPipelinesDir string) (*ProwJobContext, error) {
	prowInfo, err := ParseProwURL(prowURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Prow URL %q: %w", prowURL, err)
	}

	jobConfig, err := FetchProwJobConfig(ctx, prowInfo, sdpPipelinesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Prow job config: %w", err)
	}

	allTests, err := FetchProwJobTestResults(ctx, prowInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Prow job test results: %w", err)
	}

	kustoEndpoint, err := kusto.KustoEndpoint(jobConfig.KustoName, jobConfig.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to build Kusto endpoint: %w", err)
	}

	kcsb := azkustodata.NewConnectionStringBuilder(kustoEndpoint.String()).
		WithTokenCredential(cred)
	kustoClient, err := azkustodata.New(kcsb)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}

	return &ProwJobContext{
		ProwInfo:      prowInfo,
		JobConfig:     jobConfig,
		AllTests:      allTests,
		SiblingTests:  ConvertTestResults(allTests),
		KustoEndpoint: kustoEndpoint.String(),
		gatherer:      NewGatherer(kustoClient),
		kustoClient:   kustoClient,
	}, nil
}

// Close releases the Kusto client owned by this context.
func (p *ProwJobContext) Close() error {
	if p.kustoClient != nil {
		return p.kustoClient.Close()
	}
	return nil
}

// GatherOptionsForTest returns a GatherForTestOptions pre-populated with all
// per-job fields derived from this context, including node console logs fetched
// from GCS. The caller must set OutputDir (and optionally QueryTimeout and
// Concurrency) on the returned value before passing it to GatherForTest.
func (p *ProwJobContext) GatherOptionsForTest(ctx context.Context, test *TestResult) GatherForTestOptions {
	nodeConsoleLogs, err := FetchNodeConsoleLogs(ctx, p.ProwInfo, test.Name)
	if err != nil {
		slog.Warn("Failed to fetch node console logs, continuing without them", "error", err, "test", test.Name)
		nodeConsoleLogs = nil
	}

	return GatherForTestOptions{
		Gatherer:                 p.gatherer,
		Test:                     test,
		ProwJobURL:               p.ProwInfo.URL,
		KustoEndpoint:            p.KustoEndpoint,
		ServiceDatabase:          p.JobConfig.ServiceDatabase,
		HCPDatabase:              p.JobConfig.HCPDatabase,
		MonitoringEventsDatabase: p.JobConfig.MonitoringEventsDatabase,
		ServiceClusterName:       p.JobConfig.ServiceClusterName,
		ManagementClusterName:    p.JobConfig.ManagementClusterName,
		SiblingTests:             p.SiblingTests,
		NodeConsoleLogs:          nodeConsoleLogs,
	}
}
