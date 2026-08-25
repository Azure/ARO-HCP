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

package gatherobservability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/internal/testutil"
	"github.com/Azure/ARO-HCP/test/util/junit"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.TimingInputDir, "timing-input", opts.TimingInputDir, "Path to the directory holding timing outputs from an end-to-end test run.")
	cmd.Flags().StringVar(&opts.OutputDir, "output", opts.OutputDir, "Path to the directory where artifacts will be written.")
	cmd.Flags().StringVar(&opts.RenderedConfig, "rendered-config", opts.RenderedConfig, "Path to the rendered configuration YAML file.")
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "Azure subscription ID.")
	cmd.Flags().StringVar(&opts.StartTimeFallback, "start-time-fallback", opts.StartTimeFallback, "Optional RFC3339 time to use as start time fallback when steps and test timing are unavailable.")
	cmd.Flags().StringVar(&opts.SeverityThreshold, "severity-threshold", opts.SeverityThreshold, "Include alerts at this severity level or more critical (Sev0=critical .. Sev4=verbose). E.g. Sev2 includes Sev0, Sev1, Sev2. If not set, all severities are shown.")
	return nil
}

type RawOptions struct {
	TimingInputDir    string
	OutputDir         string
	RenderedConfig    string
	SubscriptionID    string
	StartTimeFallback string
	SeverityThreshold string
}

type validatedOptions struct {
	*RawOptions
	severityThreshold int // -1 means no filter; 0=Sev0 .. 4=Sev4
}

type ValidatedOptions struct {
	*validatedOptions
}

type completedOptions struct {
	OutputDir         string
	Workspaces        map[string]azcorearm.ResourceID
	MetricResources   map[string]azcorearm.ResourceID
	TimeWindow        timing.TimeWindow
	Queries           *QueriesConfig
	SeverityThreshold int // -1 means no filter; 0=Sev0 .. 4=Sev4
	cred              azcore.TokenCredential
	knownIssues       []knownIssue
	// cosmosAutoscaleMax resolves a Cosmos container's configured autoscale
	// ceiling (RU/s) by CollectionName, used to normalize AutoscaledRU into a
	// percentage. Nil-safe callers tolerate an unset lookup.
	cosmosAutoscaleMax autoscaleMaxLookup
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
		{flag: "subscription-id", name: "subscription ID", value: &o.SubscriptionID},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}
	minSev, err := ParseSeverityThreshold(o.SeverityThreshold)
	if err != nil {
		return nil, err
	}
	return &ValidatedOptions{
		validatedOptions: &validatedOptions{RawOptions: o, severityThreshold: minSev},
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}

	// Create output directory early so we fail fast on bad paths before
	// making expensive Azure API calls.
	if err := os.MkdirAll(o.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory %s: %w", o.OutputDir, err)
	}

	cfg, err := testutil.LoadRenderedConfig(o.RenderedConfig)
	if err != nil {
		return nil, err
	}

	regionRG, err := testutil.ConfigGetString(cfg, "regionRG")
	if err != nil {
		return nil, fmt.Errorf("failed to get regionRG from config: %w", err)
	}
	svcWorkspace, err := testutil.ConfigGetString(cfg, "monitoring.svcWorkspaceName")
	if err != nil {
		return nil, fmt.Errorf("failed to get monitoring.svcWorkspaceName from config: %w", err)
	}
	hcpWorkspace, err := testutil.ConfigGetString(cfg, "monitoring.hcpWorkspaceName")
	if err != nil {
		return nil, fmt.Errorf("failed to get monitoring.hcpWorkspaceName from config: %w", err)
	}

	// The RP Cosmos DB account is deployed into the region resource group with
	// the name from frontend.cosmosDB.name (see region.bicep / output-region.bicep).
	// Its platform metrics (NormalizedRUConsumption, AutoscaledRU, ...) are queried
	// via the Azure Monitor metrics API rather than Prometheus.
	cosmosDBName, err := testutil.ConfigGetString(cfg, "frontend.cosmosDB.name")
	if err != nil {
		return nil, fmt.Errorf("failed to get frontend.cosmosDB.name from config: %w", err)
	}

	// The autoscale ceiling (max RU/s) is configured per Cosmos container. We
	// read the values from the same rendered config used to deploy them so the
	// absolute AutoscaledRU metric can be normalized into a percentage of each
	// container's ceiling (see queries.yaml / metricToResults). The Manifests
	// containers (one per management cluster, named "Manifests-MC-<n>") share
	// the kube-applier max-scale value.
	cosmosAutoscaleMax, err := buildCosmosAutoscaleMaxLookup(cfg)
	if err != nil {
		return nil, err
	}

	testTimingInfo, err := timing.LoadTestTimingInfo(ctx, o.TimingInputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load test timing info: %w", err)
	}

	var startFallback *time.Time
	if o.StartTimeFallback != "" {
		t, err := time.Parse(time.RFC3339, o.StartTimeFallback)
		if err != nil {
			return nil, fmt.Errorf("failed to parse --start-time-fallback %q: %w", o.StartTimeFallback, err)
		}
		startFallback = &t
	}

	tw, err := timing.ComputeTimeWindow(ctx, clock.RealClock{}, nil, testTimingInfo, startFallback)
	if err != nil {
		return nil, fmt.Errorf("failed to compute time window: %w", err)
	}

	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		AdditionallyAllowedTenants:   []string{"*"},
		RequireAzureTokenCredentials: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	workspaces := map[string]azcorearm.ResourceID{
		workspaceSvc: *metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Monitor/accounts/%s", o.SubscriptionID, regionRG, svcWorkspace))),
		workspaceHcp: *metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Monitor/accounts/%s", o.SubscriptionID, regionRG, hcpWorkspace))),
	}

	metricResources := map[string]azcorearm.ResourceID{
		resourceCosmosDB: *metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DocumentDB/databaseAccounts/%s", o.SubscriptionID, regionRG, cosmosDBName))),
	}

	queries, err := loadQueriesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load queries config: %w", err)
	}
	var totalQueries int
	for _, p := range queries.Panels {
		totalQueries += len(p.Queries)
	}
	logger.Info("loaded embedded queries config", "panels", len(queries.Panels), "queries", totalQueries)

	knownIssues, err := parseKnownIssues(defaultKnownIssuesData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse known issues config: %w", err)
	}
	logger.Info("loaded known issues config", "patterns", len(knownIssues))

	return &Options{completedOptions: &completedOptions{
		OutputDir:          o.OutputDir,
		Workspaces:         workspaces,
		MetricResources:    metricResources,
		TimeWindow:         tw,
		Queries:            queries,
		SeverityThreshold:  o.severityThreshold,
		cred:               cred,
		knownIssues:        knownIssues,
		cosmosAutoscaleMax: cosmosAutoscaleMax,
	}}, nil
}

// buildCosmosAutoscaleMaxLookup reads the per-container autoscale maximum
// throughput (RU/s) from the rendered configuration and returns a lookup keyed
// by Azure Monitor's CollectionName dimension. The fixed RP containers map to
// dedicated config values; the per-management-cluster "Manifests-MC-<n>"
// containers share the kube-applier max-scale value. Unknown containers resolve
// to 0 so callers leave them unscaled rather than dividing by zero.
func buildCosmosAutoscaleMaxLookup(cfg configtypes.Configuration) (autoscaleMaxLookup, error) {
	fixed := map[string]string{
		"Resources": "frontend.cosmosDB.resourceContainerMaxScale",
		"Billing":   "frontend.cosmosDB.billingContainerMaxScale",
		"Fleet":     "frontend.cosmosDB.fleetContainerMaxScale",
		"Locks":     "frontend.cosmosDB.locksContainerMaxScale",
	}
	byContainer := make(map[string]float64, len(fixed))
	for container, path := range fixed {
		v, err := testutil.ConfigGetInt(cfg, path)
		if err != nil {
			return nil, fmt.Errorf("failed to get %s from config: %w", path, err)
		}
		byContainer[container] = float64(v)
	}
	manifestsMax, err := testutil.ConfigGetInt(cfg, "kubeApplier.cosmosContainerMaxScale")
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeApplier.cosmosContainerMaxScale from config: %w", err)
	}

	return func(container string) float64 {
		if v, ok := byContainer[container]; ok {
			return v
		}
		if strings.HasPrefix(container, "Manifests-MC-") {
			return float64(manifestsMax)
		}
		return 0
	}, nil
}

func (o Options) Run(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}

	// Deduplicate resource groups across workspaces and fetch all alert
	// data once per resource group, then subdivide into workspace-scoped
	// (Prometheus) and infrastructure (metric) groups.
	resourceGroups := uniqueResourceGroups(o.Workspaces)

	var allAlerts []alert
	var metricAlertRules []string
	for _, scope := range sets.List(resourceGroups) {
		rgID, err := azcorearm.ParseResourceID(scope)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to parse resource group ID %s: %w", scope, err))
		}
		rgAlerts, err := fetchAlerts(ctx, o.cred, scope, o.TimeWindow.Start, o.TimeWindow.End)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to fetch alerts for %s: %w", scope, err))
		}
		allAlerts = append(allAlerts, rgAlerts...)

		rgRules, err := fetchMetricAlertRules(ctx, o.cred, rgID.SubscriptionID, rgID.ResourceGroupName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to fetch metric alert rules for %s: %w", scope, err))
		}
		metricAlertRules = append(metricAlertRules, rgRules...)
	}
	sortAlerts(allAlerts)
	slices.Sort(metricAlertRules)
	logger.Info("fetched alert data", "resourceGroups", len(resourceGroups), "alerts", len(allAlerts), "metricAlertRules", len(metricAlertRules))

	workspaces := make(map[string]*workspaceData, len(o.Workspaces)+1)
	for wsType, ws := range o.Workspaces {
		wsData, err := fetchWorkspaceData(ctx, o.cred, wsType, ws, allAlerts, o.SeverityThreshold, o.knownIssues)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to fetch data for %s workspace: %w", wsType, err))
		}
		workspaces[wsType] = wsData
	}

	workspaces[workspaceInfra] = buildInfraAlertData(allAlerts, metricAlertRules, o.SeverityThreshold, o.knownIssues)

	// Collect all alerts across workspaces for JSON/HTML output
	var alerts []alert
	for _, ws := range workspaces {
		alerts = append(alerts, ws.FiredAlerts...)
	}

	// Build output used for both JSON and HTML
	severityCounts := map[armalertsmanagement.Severity]int{}
	var knownCount int
	for _, a := range alerts {
		severityCounts[a.Alert.Severity]++
		if a.Metadata.KnownIssue {
			knownCount++
		}
	}
	unknownCount := len(alerts) - knownCount

	logger.Info("classified alerts", "known", knownCount, "unknown", unknownCount)

	filterKeys, filterOptions := collectFilterOptions(alerts)
	output := alertsOutput{
		Alerts: alerts,
		Summary: alertsSummary{
			Total:      len(alerts),
			Known:      knownCount,
			Unknown:    unknownCount,
			BySeverity: severityCounts,
		},
		TimeWindow: timeWindow{
			Start: o.TimeWindow.Start.UTC().Format(time.RFC3339),
			End:   o.TimeWindow.End.UTC().Format(time.RFC3339),
		},
		FilterKeys:    filterKeys,
		FilterOptions: filterOptions,
	}

	// Write JSON artifact
	jsonPath := filepath.Join(o.OutputDir, "alerts.json")
	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal alerts to JSON: %w", err))
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return utils.TrackError(fmt.Errorf("failed to write %s: %w", jsonPath, err))
	}
	logger.Info("wrote alert JSON artifact", "path", jsonPath, "alerts", len(alerts))

	// Build the tabbed observability page. The alerts view is the first tab;
	// each metrics panel becomes an additional tab below.
	alertsHTML, err := renderAlertsHTML(output)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to render alerts HTML: %w", err))
	}
	tabs := []observabilityTab{{Title: "Azure Monitor Alerts", HTML: string(alertsHTML)}}

	// Write JUnit
	junitPath := filepath.Join(o.OutputDir, "junit_alerts.xml")
	suites := alertsToJUnit(logger, workspaces, o.TimeWindow)
	if err := junit.Write(junitPath, suites); err != nil {
		return utils.TrackError(fmt.Errorf("failed to write JUnit output: %w", err))
	}
	logger.Info("wrote alert JUnit artifact", "path", junitPath)

	// Execute panel queries (Prometheus and Azure Monitor) and render timeseries charts
	if o.Queries != nil {
		panelTabs, err := o.runQueries(ctx, workspaces)
		if err != nil {
			return utils.TrackError(fmt.Errorf("panel query execution failed: %w", err))
		}
		tabs = append(tabs, panelTabs...)
	}

	// Emit a single tabbed HTML page. The filename must match the Spyglass HTML
	// lens regex .*-summary.*\.html so Prow renders it inline as one iframe.
	htmlPath := filepath.Join(o.OutputDir, "observability-summary.html")
	if err := renderObservabilityPage(htmlPath, tabs); err != nil {
		return utils.TrackError(fmt.Errorf("failed to render observability HTML: %w", err))
	}
	logger.Info("wrote observability HTML artifact", "path", htmlPath, "tabs", len(tabs))

	// Fail the process when JUnit contains failures
	var totalFailed uint
	for _, s := range suites.Suites {
		totalFailed += s.NumFailed
	}
	if totalFailed > 0 {
		return fmt.Errorf("JUnit results contain %d failing test case(s)", totalFailed)
	}

	return nil
}

func (o Options) runQueries(ctx context.Context, workspaces map[string]*workspaceData) ([]observabilityTab, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}

	var tabs []observabilityTab
	for _, panel := range o.Queries.Panels {
		logger.Info("executing panel queries", "panel", panel.Title, "queries", len(panel.Queries))

		var panelCharts []chartData
		for _, q := range panel.Queries {
			var results []PrometheusResult
			var queryErr string
			var warning string
			var metricResourceID string

			switch q.Source {
			case sourceAzureMonitor:
				logger.Info("executing Azure Monitor metrics query", "panel", panel.Title, "title", q.Title, "resource", q.Resource)
				resourceID, ok := o.MetricResources[q.Resource]
				if !ok {
					return nil, fmt.Errorf("unknown metric resource %q for query %q", q.Resource, q.Title)
				}
				metricResourceID = resourceID.String()
				res, warn, err := queryAzureMonitorMetrics(ctx, o.cred, resourceID, q, o.TimeWindow.Start, o.TimeWindow.End, o.cosmosAutoscaleMax)
				if err != nil {
					logger.Error(err, "Azure Monitor metrics query failed", "title", q.Title)
					queryErr = err.Error()
				} else {
					results = res
					if warn != "" {
						logger.Info("Azure Monitor metrics query partially failed", "title", q.Title, "warning", warn)
						warning = warn
					}
				}
			default:
				ws, ok := workspaces[q.Workspace]
				if !ok {
					return nil, fmt.Errorf("unknown workspace %q for query %q", q.Workspace, q.Title)
				}
				endpoint := ws.PromEndpoint

				logger.Info("executing PromQL query", "panel", panel.Title, "title", q.Title, "workspace", q.Workspace)

				resp, err := queryRange(ctx, httpClient, o.cred, endpoint, q.Query, o.TimeWindow.Start, o.TimeWindow.End, q.Step)
				if err != nil {
					logger.Error(err, "PromQL query failed", "title", q.Title)
					queryErr = err.Error()
				} else {
					results = resp.Data.Result
				}
			}

			panelCharts = append(panelCharts, buildChartData(q, metricResourceID, queryErr, warning, results, o.TimeWindow))
		}

		pageData := panelPageData{Title: panel.Title, Charts: panelCharts}
		pageData.TimeWindow.Start = o.TimeWindow.Start.UTC().Format(time.RFC3339)
		pageData.TimeWindow.End = o.TimeWindow.End.UTC().Format(time.RFC3339)

		html, err := renderPanelHTML(pageData)
		if err != nil {
			logger.Error(err, "failed to render panel", "panel", panel.Title)
			continue
		}
		tabs = append(tabs, observabilityTab{Title: panel.Title, HTML: string(html)})
		logger.Info("rendered panel tab", "panel", panel.Title, "charts", len(panelCharts))
	}
	return tabs, nil
}
