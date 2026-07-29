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

package gathersnapshot

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

	"github.com/Azure/ARO-HCP/test/util/junit"
	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.TimingInputDir, "timing-input", opts.TimingInputDir, "Path to the directory holding timing outputs from an end-to-end test run.")
	cmd.Flags().StringVar(&opts.OutputDir, "output", opts.OutputDir, "Path to the directory where artifacts will be written.")
	cmd.Flags().StringVar(&opts.RenderedConfig, "rendered-config", opts.RenderedConfig, "Path to the rendered configuration YAML file.")
	return nil
}

type RawOptions struct {
	TimingInputDir string
	OutputDir      string
	RenderedConfig string
}

type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	OutputDir             string
	KustoEndpoint         string
	ServiceDB             string
	HCPDB                 string
	MonitoringEventsDB    string
	ServiceClusterName    string
	ManagementClusterName string
	TestTimingInfo        map[string]timing.TimingInfo
	kustoClient           *azkustodata.Client
}

type Options struct {
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "output", name: "output dir", value: &o.OutputDir},
		{flag: "rendered-config", name: "rendered config", value: &o.RenderedConfig},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}
	return &ValidatedOptions{
		validatedOptions: &validatedOptions{RawOptions: o},
	}, nil
}

// kustoGeoToRegion maps the 2-character geo short ID from a kusto cluster name
// to its Azure region. Dev environments (hcp-dev-*) all reside in eastus2.
var kustoGeoToRegion = map[string]string{
	"au": "australiaeast",
	"br": "brazilsouth",
	"ca": "canadacentral",
	"ch": "switzerlandnorth",
	"eu": "westeurope",
	"in": "centralindia",
	"uk": "uksouth",
	"us": "eastus2",
}

func resolveKustoRegion(kustoName string) (string, error) {
	if strings.HasPrefix(kustoName, "hcp-dev-") {
		return "eastus2", nil
	}
	parts := strings.SplitN(kustoName, "-", 3)
	if len(parts) == 3 && len(parts[2]) >= 2 {
		if region, ok := kustoGeoToRegion[parts[2][:2]]; ok {
			return region, nil
		}
	}
	return "", fmt.Errorf("cannot resolve kusto region for %q", kustoName)
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	if err := os.MkdirAll(o.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", o.OutputDir, err)
	}

	rawCfg, err := os.ReadFile(o.RenderedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read rendered config %s: %w", o.RenderedConfig, err)
	}

	jobConfig, err := snapshot.ParseConfig(rawCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rendered config: %w", err)
	}

	kustoRegion, err := resolveKustoRegion(jobConfig.KustoName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve kusto region: %w", err)
	}

	kustoEndpoint, err := kusto.KustoEndpoint(jobConfig.KustoName, kustoRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to build kusto endpoint: %w", err)
	}

	logger.Info("resolved kusto config",
		"name", jobConfig.KustoName,
		"region", kustoRegion,
		"endpoint", kustoEndpoint.String(),
		"serviceDB", jobConfig.ServiceDatabase,
		"hcpDB", jobConfig.HCPDatabase,
		"monitoringEventsDB", jobConfig.MonitoringEventsDatabase,
		"serviceCluster", jobConfig.ServiceClusterName,
		"managementCluster", jobConfig.ManagementClusterName,
	)

	testTimingInfo, err := timing.LoadTestTimingInfo(ctx, o.TimingInputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load test timing info: %w", err)
	}

	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		AdditionallyAllowedTenants:   []string{"*"},
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	kcsb := azkustodata.NewConnectionStringBuilder(kustoEndpoint.String())
	kcsb = kcsb.WithTokenCredential(cred)
	kustoClient, err := azkustodata.New(kcsb)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}

	return &Options{completedOptions: &completedOptions{
		OutputDir:             o.OutputDir,
		KustoEndpoint:         kustoEndpoint.String(),
		ServiceDB:             jobConfig.ServiceDatabase,
		HCPDB:                 jobConfig.HCPDatabase,
		MonitoringEventsDB:    jobConfig.MonitoringEventsDatabase,
		ServiceClusterName:    jobConfig.ServiceClusterName,
		ManagementClusterName: jobConfig.ManagementClusterName,
		TestTimingInfo:        testTimingInfo,
		kustoClient:           kustoClient,
	}}, nil
}

func (o Options) Run(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}
	defer func() {
		if err := o.kustoClient.Close(); err != nil {
			logger.Error(err, "Failed to close Kusto client")
		}
	}()

	gatherer := snapshot.NewGatherer(o.kustoClient)

	// Collect all verification reports across tests for aggregated jUnit output.
	var allReports []*snapshot.VerificationReport
	var allManifests []*snapshot.Manifest

	for testName, ti := range o.TestTimingInfo {
		if ctx.Err() != nil {
			break
		}
		if len(ti.ResourceGroupNames) == 0 {
			logger.V(1).Info("Skipping test without resource groups", "test", testName)
			continue
		}

		for _, rg := range ti.ResourceGroupNames {
			testOutputDir := filepath.Join(o.OutputDir, snapshot.SanitizeTestName(testName), rg)

			logger.Info("Gathering snapshot",
				"test", testName,
				"resourceGroup", rg,
				"startTime", ti.StartTime.Format(time.RFC3339),
				"endTime", ti.EndTime.Format(time.RFC3339),
			)

			input := snapshot.GatherInput{
				ClusterURI:               o.KustoEndpoint,
				ServiceDatabase:          o.ServiceDB,
				HCPDatabase:              o.HCPDB,
				MonitoringEventsDatabase: o.MonitoringEventsDB,
				ResourceGroup:            rg,
				ServiceClusterName:       o.ServiceClusterName,
				ManagementClusterName:    o.ManagementClusterName,
				TimeWindow: snapshot.TimeWindow{
					Start:           ti.StartTime,
					End:             ti.EndTime,
					SetupFinishTime: ti.SetupFinishTime,
				},
				QueryTimeout:     5 * time.Minute,
				TestStartTime:    ti.TestStartTime,
				CleanupStartTime: ti.CleanupStartTime,
			}

			if input.TestStartTime.IsZero() {
				return fmt.Errorf("test %q: TestStartTime is zero; timing metadata is incomplete", testName)
			}
			if input.CleanupStartTime.IsZero() {
				return fmt.Errorf("test %q: CleanupStartTime is zero; timing metadata is incomplete", testName)
			}

			manifest, report, err := gatherer.Gather(ctx, input, testOutputDir)
			if err != nil {
				logger.Error(err, "Failed to gather snapshot", "test", testName, "resourceGroup", rg)
				allReports = append(allReports, &snapshot.VerificationReport{
					Cases: []snapshot.VerificationCase{{
						Suite:        fmt.Sprintf("gather/%s", rg),
						Query:        "gather",
						Category:     "infrastructure",
						ResourceType: "gather",
						Status:       snapshot.VerificationFail,
						Message:      fmt.Sprintf("failed to gather snapshot for test %q resource group %q: %v", testName, rg, err),
					}},
				})
				if ctx.Err() != nil {
					break
				}
				continue
			}

			manifest.TestName = testName
			if writeErr := snapshot.WriteManifest(testOutputDir, manifest); writeErr != nil {
				logger.Error(writeErr, "Failed to write manifest", "test", testName)
			}

			allManifests = append(allManifests, manifest)
			allReports = append(allReports, report)

			logger.Info("Snapshot complete",
				"test", testName,
				"resourceGroup", rg,
				"phases", len(manifest.Phases),
				"verificationCases", len(report.Cases),
			)
		}
	}

	// Write aggregated jUnit XML.
	junitPath := filepath.Join(o.OutputDir, "junit_snapshot.xml")
	suites := reportsToJUnit(allReports)
	if err := junit.Write(junitPath, suites); err != nil {
		return fmt.Errorf("failed to write jUnit output: %w", err)
	}
	logger.Info("wrote snapshot jUnit artifact", "path", junitPath)

	// Write a single HTML overview covering all tests.
	if err := WriteHTMLOverview(o.OutputDir, allManifests, allReports); err != nil {
		logger.Error(err, "Failed to write HTML overview")
	}

	// Dump the raw snapshot data so the rendering pipeline can be tested
	// locally with real fixture data.
	if err := WriteSnapshotData(o.OutputDir, allManifests, allReports); err != nil {
		logger.Error(err, "Failed to write snapshot data")
	}

	// Exit non-zero only when the jUnit we just wrote contains at least one
	// failing test case. This keeps the exit code aligned with what CI
	// consumers actually see in the jUnit XML rather than the raw per-resource
	// verification case count (which may be higher due to aggregation).
	var junitFailures uint
	for _, s := range suites.Suites {
		junitFailures += s.NumFailed
	}
	if junitFailures > 0 {
		return fmt.Errorf("%d jUnit test failure(s) detected across all tests", junitFailures)
	}

	return nil
}
