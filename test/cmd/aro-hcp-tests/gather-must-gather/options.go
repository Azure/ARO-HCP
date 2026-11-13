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

package gathermustgather

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/mustgather"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

const (
	servicesLogDirectory            = "service"
	hostedControlPlaneLogDirectory  = "hosted-control-plane"
	customLogsDirectory             = "custom"
)

type RawOptions struct {
	TimingInputDir string
	OutputDir      string
	RenderedConfig string
	QueryTimeout   time.Duration
	Limit          int
}

func DefaultOptions() *RawOptions {
	return &RawOptions{
		QueryTimeout: 5 * time.Minute,
		Limit:        2000,
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.TimingInputDir, "timing-input", opts.TimingInputDir, "Path to the directory holding timing-metadata-*.yaml(.gz) files from an E2E run.")
	cmd.Flags().StringVar(&opts.OutputDir, "output", opts.OutputDir, "Path to the directory where must-gather artifacts will be written.")
	cmd.Flags().StringVar(&opts.RenderedConfig, "rendered-config", opts.RenderedConfig, "Path to the rendered configuration YAML file.")
	cmd.Flags().DurationVar(&opts.QueryTimeout, "query-timeout", opts.QueryTimeout, "Per-query Kusto timeout.")
	cmd.Flags().IntVar(&opts.Limit, "limit", opts.Limit, "Maximum rows returned per query (-1 for no limit).")
	return nil
}

type validatedOptions struct {
	*RawOptions
	kustoEndpoint *url.URL
}

type ValidatedOptions struct {
	*validatedOptions
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

	return &ValidatedOptions{validatedOptions: &validatedOptions{
		RawOptions:    o,
		kustoEndpoint: kustoEndpoint,
	}}, nil
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

type completedOptions struct {
	OutputDir    string
	QueryTimeout time.Duration
	Limit        int
	timingInfo   map[string]timing.TimingInfo
	kustoClient  kusto.KustoClient
}

type Options struct {
	*completedOptions
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	logger := logr.FromContextOrDiscard(ctx)

	if err := os.MkdirAll(o.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", o.OutputDir, err)
	}

	kustoClient, err := kusto.NewClient(o.kustoEndpoint, o.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kusto client: %w", err)
	}

	logger.Info("resolved kusto config", "endpoint", o.kustoEndpoint.String())

	timingInfo, err := timing.LoadTestTimingInfo(ctx, o.TimingInputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load test timing info: %w", err)
	}

	return &Options{completedOptions: &completedOptions{
		OutputDir:    o.OutputDir,
		QueryTimeout: o.QueryTimeout,
		Limit:        o.Limit,
		timingInfo:   timingInfo,
		kustoClient:  kustoClient,
	}}, nil
}

func (o *Options) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)
	defer func() {
		if err := o.kustoClient.Close(); err != nil {
			logger.Error(err, "Failed to close Kusto client")
		}
	}()

	if len(o.timingInfo) == 0 {
		logger.Info("No timing metadata found, nothing to gather")
		return nil
	}

	var gatherErrors error
	for testName, ti := range o.timingInfo {
		if ctx.Err() != nil {
			break
		}
		if len(ti.ResourceGroupNames) == 0 {
			logger.V(1).Info("Skipping test without resource groups", "test", testName)
			continue
		}

		for _, rg := range ti.ResourceGroupNames {
			if err := o.gatherForRG(ctx, testName, rg, ti); err != nil {
				logger.Error(err, "Failed to gather must-gather", "test", testName, "resourceGroup", rg)
				gatherErrors = errors.Join(gatherErrors, err)
			}
		}
	}
	return gatherErrors
}

func (o *Options) gatherForRG(ctx context.Context, testName, rg string, ti timing.TimingInfo) error {
	logger := logr.FromContextOrDiscard(ctx)

	rgOutputDir := filepath.Join(o.OutputDir, snapshot.SanitizeTestName(testName), rg)
	for _, subDir := range []string{servicesLogDirectory, hostedControlPlaneLogDirectory, customLogsDirectory} {
		if err := os.MkdirAll(filepath.Join(rgOutputDir, subDir), 0755); err != nil {
			return fmt.Errorf("failed to create output subdir %s: %w", subDir, err)
		}
	}

	logger.Info("Gathering must-gather",
		"test", testName,
		"resourceGroup", rg,
		"startTime", ti.StartTime.Format(time.RFC3339),
		"endTime", ti.EndTime.Format(time.RFC3339),
	)

	qo := kusto.NewQueryOptions()
	qo.SubscriptionId = ti.SubscriptionID
	qo.ResourceGroupName = rg
	qo.TimestampMin = ti.StartTime
	qo.TimestampMax = ti.EndTime
	qo.Limit = o.Limit

	queryClient := mustgather.NewQueryClient(o.kustoClient, o.QueryTimeout, rgOutputDir)
	gatherer := mustgather.NewCliGatherer(
		queryClient, rgOutputDir,
		servicesLogDirectory, hostedControlPlaneLogDirectory, customLogsDirectory,
		mustgather.GathererOptions{QueryOptions: &qo},
		false,
	)

	if err := gatherer.GatherLogs(ctx); err != nil {
		return fmt.Errorf("gather failed: %w", err)
	}

	logger.Info("Must-gather complete", "test", testName, "resourceGroup", rg)
	return nil
}
