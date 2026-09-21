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
	"errors"
	"fmt"
	"maps"
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

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/internal/testutil"
	"github.com/Azure/ARO-HCP/test/util/junit"
	promutil "github.com/Azure/ARO-HCP/test/util/prometheus"
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
	cmd.Flags().BoolVar(&opts.AMWOnly, "amw-only", false, "Collect only bounded AMW/DCR platform metrics and render the AMW pane; no Prometheus queries or alert evaluation.")
	return nil
}

type RawOptions struct {
	AMWOnly           bool
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
	AMWOnly           bool
	OutputDir         string
	Workspaces        map[string]azcorearm.ResourceID
	MetricResources   map[string]azcorearm.ResourceID
	TimeWindow        timing.TimeWindow
	Queries           *QueriesConfig
	SeverityThreshold int // -1 means no filter; 0=Sev0 .. 4=Sev4
	cred              azcore.TokenCredential
	knownIssues       []knownIssue
	resourceGroups    sets.Set[string]
	workspaceErrors   map[string]error
	queriesError      error
	knownIssuesError  error
	cosmosError       error
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
	workspaceErrors := map[string]error{}
	workspaces := map[string]azcorearm.ResourceID{}
	for _, wsType := range []string{workspaceSvc, workspaceHcp} {
		name, err := testutil.ConfigGetString(cfg, "monitoring."+wsType+"WorkspaceName")
		if err == nil && name == "" {
			err = fmt.Errorf("workspace name is empty")
		}
		if err != nil {
			workspaceErrors[wsType] = fmt.Errorf("failed to get %s workspace name from config: %w", wsType, err)
			continue
		}
		id, err := azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Monitor/accounts/%s", o.SubscriptionID, regionRG, name))
		if err != nil {
			workspaceErrors[wsType] = err
			continue
		}
		workspaces[wsType] = *id
	}

	// The RP Cosmos DB account is deployed into the region resource group with
	// the name from frontend.cosmosDB.name (see region.bicep / output-region.bicep).
	// Its platform metrics (NormalizedRUConsumption, AutoscaledRU, ...) are queried
	// via the Azure Monitor metrics API rather than Prometheus.
	cosmosDBName, err := testutil.ConfigGetString(cfg, "frontend.cosmosDB.name")
	if err == nil && cosmosDBName == "" {
		err = fmt.Errorf("cosmos DB account name is empty")
	}
	var cosmosError error
	if err != nil {
		cosmosError = fmt.Errorf("failed to get frontend.cosmosDB.name from config: %w", err)
	}

	// The autoscale ceiling (max RU/s) is configured per Cosmos container. We
	// read the values from the same rendered config used to deploy them so the
	// absolute AutoscaledRU metric can be normalized into a percentage of each
	// container's ceiling (see queries.yaml / metricToResults). The Manifests
	// containers (one per management cluster, named "Manifests-MC-<n>") share
	// the kube-applier max-scale value.
	cosmosAutoscaleMax, err := buildCosmosAutoscaleMaxLookup(cfg)
	if err != nil {
		cosmosError = errors.Join(cosmosError, err)
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

	metricResources := map[string]azcorearm.ResourceID{}
	if cosmosDBName != "" {
		id, err := azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.DocumentDB/databaseAccounts/%s", o.SubscriptionID, regionRG, cosmosDBName))
		if err != nil {
			cosmosError = errors.Join(cosmosError, err)
		} else {
			metricResources[resourceCosmosDB] = *id
		}
	}

	queries, queriesError := loadQueriesConfig()
	if queriesError != nil {
		queriesError = fmt.Errorf("failed to load queries config: %w", queriesError)
	} else {
		logger.Info("loaded embedded queries config", "panels", len(queries.Panels))
	}

	knownIssues, knownIssuesError := parseKnownIssues(defaultKnownIssuesData)
	if knownIssuesError != nil {
		knownIssuesError = fmt.Errorf("failed to parse known issues config: %w", knownIssuesError)
	}
	logger.Info("loaded known issues config", "patterns", len(knownIssues))

	return &Options{completedOptions: &completedOptions{
		AMWOnly:            o.AMWOnly,
		OutputDir:          o.OutputDir,
		Workspaces:         workspaces,
		MetricResources:    metricResources,
		TimeWindow:         tw,
		Queries:            queries,
		SeverityThreshold:  o.severityThreshold,
		cred:               cred,
		knownIssues:        knownIssues,
		resourceGroups:     sets.New(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", o.SubscriptionID, regionRG)),
		workspaceErrors:    workspaceErrors,
		queriesError:       queriesError,
		knownIssuesError:   knownIssuesError,
		cosmosError:        cosmosError,
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
	var configErrors []error
	for _, container := range slices.Sorted(maps.Keys(fixed)) {
		path := fixed[container]
		v, err := testutil.ConfigGetInt(cfg, path)
		if err != nil {
			configErrors = append(configErrors, fmt.Errorf("failed to get %s from config: %w", path, err))
			continue
		}
		byContainer[container] = float64(v)
	}
	manifestsMax, err := testutil.ConfigGetInt(cfg, "kubeApplier.cosmosContainerMaxScale")
	if err != nil {
		configErrors = append(configErrors, fmt.Errorf("failed to get kubeApplier.cosmosContainerMaxScale from config: %w", err))
	}

	return func(container string) float64 {
		if v, ok := byContainer[container]; ok {
			return v
		}
		if strings.HasPrefix(container, "Manifests-MC-") {
			return float64(manifestsMax)
		}
		return 0
	}, errors.Join(configErrors...)
}

// Explicit dependencies let orchestration tests exercise failures without Azure.
type gatherDependencies struct {
	fetchAlerts           func(context.Context, azcore.TokenCredential, string, time.Time, time.Time) ([]alert, error)
	fetchMetricAlertRules func(context.Context, azcore.TokenCredential, string, string) ([]string, error)
	fetchAlertRules       func(context.Context, azcore.TokenCredential, azcorearm.ResourceID) ([]string, error)
	lookupEndpoint        func(context.Context, azcore.TokenCredential, string, string, string) (string, error)
	queryRange            func(context.Context, *http.Client, azcore.TokenCredential, string, string, time.Time, time.Time, string) (*promutil.Response, error)
	queryMetrics          func(context.Context, azcore.TokenCredential, azcorearm.ResourceID, QuerySpec, time.Time, time.Time, autoscaleMaxLookup) ([]promutil.Result, string, error)
	collectUtilization    func(context.Context, map[string]*workspaceData) utilizationReport
	collectAMW            func(context.Context) amwReport
	renderAMW             func(amwReport) ([]byte, error)
	renderAlerts          func(any) ([]byte, error)
	renderPanel           func(panelPageData) ([]byte, error)
	renderUtilization     func(utilizationReport) ([]byte, error)
	renderResourceHistory func(utilizationReport) ([]byte, error)
	renderPage            func(string, []observabilityTab) error
	writeFile             func(string, []byte, os.FileMode) error
	writeJUnit            func(string, *junit.TestSuites) error
}

func (o Options) dependencies() gatherDependencies {
	return gatherDependencies{
		fetchAlerts: fetchAlerts, fetchMetricAlertRules: fetchMetricAlertRules,
		fetchAlertRules: fetchAlertRules, lookupEndpoint: promutil.LookupPrometheusEndpoint,
		queryRange: promutil.QueryRange, queryMetrics: queryAzureMonitorMetrics,
		collectUtilization: o.collectUtilization, renderUtilization: renderUtilizationHTML,
		collectAMW: o.collectAMW, renderAMW: renderAMWHTML,
		renderResourceHistory: renderResourceHistoryHTML,
		renderAlerts:          renderAlertsHTML, renderPanel: renderPanelHTML,
		renderPage: renderObservabilityPage, writeFile: os.WriteFile, writeJUnit: junit.Write,
	}
}

func (o Options) Run(ctx context.Context) error {
	return o.run(ctx, o.dependencies())
}

func (o Options) run(ctx context.Context, deps gatherDependencies) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("logger not found in context: %w", err)
	}
	if o.AMWOnly {
		tab, err := o.runAMW(ctx, deps)
		return errors.Join(err, deps.renderPage(filepath.Join(o.OutputDir, "observability-summary.html"), []observabilityTab{tab}))
	}

	var fatalErrors []error
	record := func(err error) {
		if err != nil {
			logger.Error(err, "observability collection incomplete")
			fatalErrors = append(fatalErrors, err)
		}
	}
	record(o.queriesError)
	record(o.cosmosError)
	record(o.knownIssuesError)
	resourceGroups := uniqueResourceGroups(o.Workspaces).Union(o.resourceGroups)

	var allAlerts []alert
	var metricAlertRules []string
	alertErrors := map[string]error{}
	var infraErrors []error
	for _, scope := range sets.List(resourceGroups) {
		rgAlerts, err := deps.fetchAlerts(ctx, o.cred, scope, o.TimeWindow.Start, o.TimeWindow.End)
		if err != nil {
			err = fmt.Errorf("failed to fetch alerts for %s: %w", scope, err)
			alertErrors[scope] = err
			infraErrors = append(infraErrors, err)
			record(err)
		}
		allAlerts = append(allAlerts, rgAlerts...)

		rgID, err := azcorearm.ParseResourceID(scope)
		if err != nil {
			err = fmt.Errorf("failed to parse resource group ID %s: %w", scope, err)
			infraErrors = append(infraErrors, err)
			record(err)
			continue
		}
		rgRules, err := deps.fetchMetricAlertRules(ctx, o.cred, rgID.SubscriptionID, rgID.ResourceGroupName)
		if err != nil {
			err = fmt.Errorf("failed to fetch metric alert rules for %s: %w", scope, err)
			infraErrors = append(infraErrors, err)
			record(err)
		}
		metricAlertRules = append(metricAlertRules, rgRules...)
	}
	sortAlerts(allAlerts)
	slices.Sort(metricAlertRules)
	logger.Info("fetched alert data", "resourceGroups", len(resourceGroups), "alerts", len(allAlerts), "metricAlertRules", len(metricAlertRules))

	workspaces := make(map[string]*workspaceData, len(o.Workspaces)+1)
	for _, wsType := range slices.Sorted(maps.Keys(o.Workspaces)) {
		ws := o.Workspaces[wsType]
		wsData := buildWorkspaceAlertData(wsType, ws, allAlerts, o.SeverityThreshold, o.knownIssues)
		workspaces[wsType] = wsData
		rules, err := deps.fetchAlertRules(ctx, o.cred, ws)
		if err != nil {
			err = fmt.Errorf("failed to fetch %s alert rules: %w", wsType, err)
			record(err)
		}
		wsData.AlertRules = rules
		scope := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", ws.SubscriptionID, ws.ResourceGroupName)
		wsData.CollectionError = errors.Join(alertErrors[scope], err, o.knownIssuesError)
		wsData.PromEndpoint, err = deps.lookupEndpoint(ctx, o.cred, ws.SubscriptionID, ws.ResourceGroupName, ws.Name)
		if err == nil && wsData.PromEndpoint == "" {
			err = fmt.Errorf("empty Prometheus endpoint")
		}
		if err != nil {
			wsData.PromError = fmt.Errorf("failed to look up %s Prometheus endpoint: %w", wsType, err)
			wsData.PromEndpoint = ""
			record(wsData.PromError)
		}
	}
	for _, wsType := range slices.Sorted(maps.Keys(o.workspaceErrors)) {
		err := o.workspaceErrors[wsType]
		record(err)
		workspaces[wsType] = &workspaceData{Type: wsType, PromError: err, CollectionError: err}
		infraErrors = append(infraErrors, err)
	}

	workspaces[workspaceInfra] = buildInfraAlertData(allAlerts, metricAlertRules, o.SeverityThreshold, o.knownIssues)
	workspaces[workspaceInfra].CollectionError = errors.Join(append(infraErrors, o.knownIssuesError)...)

	// Collect all alerts across workspaces for JSON/HTML output
	var alerts []alert
	var collectionErrors []string
	for _, wsType := range slices.Sorted(maps.Keys(workspaces)) {
		ws := workspaces[wsType]
		alerts = append(alerts, ws.FiredAlerts...)
		if ws.CollectionError != nil {
			collectionErrors = append(collectionErrors, fmt.Sprintf("%s: %v", wsType, ws.CollectionError))
		}
	}
	// Workspace setup can fail while the resource-group API still returns alerts.
	// Retain those alerts in JSON/HTML even when their workspace cannot be resolved.
	for _, a := range classifyAlerts(filterAlertsBySeverity(allAlerts, o.SeverityThreshold), o.knownIssues) {
		if len(o.workspaceErrors) == 0 || !isWorkspaceTargeted(a) {
			continue
		}
		matched := false
		for _, ws := range o.Workspaces {
			if alertBelongsToWorkspace(a, ws) {
				matched = true
				break
			}
		}
		if !matched {
			alerts = append(alerts, a)
		}
	}
	sortAlerts(alerts)

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
		FilterKeys:       filterKeys,
		FilterOptions:    filterOptions,
		CollectionErrors: collectionErrors,
	}

	writeJSON := func(name string, data any) {
		content, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			record(fmt.Errorf("failed to marshal %s: %w", name, err))
			return
		}
		path := filepath.Join(o.OutputDir, name)
		if err := deps.writeFile(path, content, 0644); err != nil {
			record(fmt.Errorf("failed to write %s: %w", path, err))
		} else {
			logger.Info("wrote JSON artifact", "path", path)
		}
	}
	writeJSON("alerts.json", output)

	// Build the tabbed observability page. The alerts view is the first tab;
	// each metrics panel becomes an additional tab below.
	alertsHTML, err := deps.renderAlerts(output)
	if err != nil {
		record(fmt.Errorf("failed to render alerts HTML: %w", err))
	}
	tabs := []observabilityTab{{Title: "Azure Monitor Alerts", HTML: string(incompleteHTML(alertsHTML, err))}}

	// Write JUnit
	junitPath := filepath.Join(o.OutputDir, "junit_alerts.xml")
	suites := alertsToJUnit(logger, workspaces, o.TimeWindow)
	if err := deps.writeJUnit(junitPath, suites); err != nil {
		record(fmt.Errorf("failed to write JUnit output: %w", err))
	} else {
		logger.Info("wrote alert JUnit artifact", "path", junitPath)
	}

	// Gather bounded ARM-only evidence before expensive PromQL panels. AMW
	// diagnostics are best effort and do not add an alert/JUnit gate.
	amwTab, amwErr := o.runAMW(ctx, deps)
	if amwErr != nil {
		logger.Error(amwErr, "failed to publish AMW evidence")
	}
	tabs = append(tabs, amwTab)

	// Execute panel queries (Prometheus and Azure Monitor) and render timeseries charts
	if o.Queries != nil {
		panelTabs, err := o.runQueries(ctx, workspaces, deps)
		record(err)
		tabs = append(tabs, panelTabs...)
	}
	if o.queriesError != nil {
		tabs = append(tabs, observabilityTab{Title: "Metrics", HTML: string(incompleteHTML(nil, o.queriesError))})
	}

	// The collector owns its timeout; alert and HCP failures must not gate it.
	report := deps.collectUtilization(ctx, workspaces)
	writeJSON("utilization.json", report)
	utilizationHTML, err := deps.renderUtilization(report)
	if err != nil {
		record(fmt.Errorf("failed to render utilization HTML: %w", err))
	}
	tabs = append(tabs, observabilityTab{Title: "Utilization", HTML: string(incompleteHTML(utilizationHTML, err))})
	historyHTML, err := deps.renderResourceHistory(report)
	if err != nil {
		record(fmt.Errorf("failed to render resource history HTML: %w", err))
	}
	tabs = append(tabs, observabilityTab{Title: "Resource History", HTML: string(incompleteHTML(historyHTML, err))})

	// Emit a single tabbed HTML page. The filename must match the Spyglass HTML
	// lens regex .*-summary.*\.html so Prow renders it inline as one iframe.
	htmlPath := filepath.Join(o.OutputDir, "observability-summary.html")
	if err := deps.renderPage(htmlPath, tabs); err != nil {
		record(fmt.Errorf("failed to render observability HTML: %w", err))
	} else {
		logger.Info("wrote observability HTML artifact", "path", htmlPath, "tabs", len(tabs))
	}

	// Fail the process when JUnit contains failures
	var totalFailed uint
	for _, s := range suites.Suites {
		totalFailed += s.NumFailed
	}
	if totalFailed > 0 {
		record(fmt.Errorf("JUnit results contain %d failing test case(s)", totalFailed))
	}

	return utils.TrackError(errors.Join(fatalErrors...))
}

func (o Options) runQueries(ctx context.Context, workspaces map[string]*workspaceData, deps gatherDependencies) ([]observabilityTab, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("logger not found in context: %w", err)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}

	var tabs []observabilityTab
	var fatalErrors []error
	for _, panel := range o.Queries.Panels {
		logger.Info("executing panel queries", "panel", panel.Title, "queries", len(panel.Queries))

		var panelCharts []chartData
		for _, q := range panel.Queries {
			var results []promutil.Result
			var queryErr string
			var warning string
			var metricResourceID string

			switch q.Source {
			case sourceAzureMonitor:
				logger.Info("executing Azure Monitor metrics query", "panel", panel.Title, "title", q.Title, "resource", q.Resource)
				resourceID, ok := o.MetricResources[q.Resource]
				if !ok {
					err := fmt.Errorf("unknown metric resource %q for query %q", q.Resource, q.Title)
					fatalErrors = append(fatalErrors, err)
					queryErr = err.Error()
					break
				}
				metricResourceID = resourceID.String()
				if q.Resource == resourceCosmosDB && o.cosmosError != nil {
					warning = o.cosmosError.Error()
				}
				res, warn, err := deps.queryMetrics(ctx, o.cred, resourceID, q, o.TimeWindow.Start, o.TimeWindow.End, o.cosmosAutoscaleMax)
				results = res
				if err != nil {
					logger.Error(err, "Azure Monitor metrics query failed", "title", q.Title)
					queryErr = err.Error()
				}
				if warn != "" {
					logger.Info("Azure Monitor metrics query partially failed", "title", q.Title, "warning", warn)
					warning = strings.TrimSpace(warning + "\n" + warn)
				}
			default:
				ws, ok := workspaces[q.Workspace]
				if !ok || ws == nil {
					err := fmt.Errorf("unknown workspace %q for query %q", q.Workspace, q.Title)
					fatalErrors = append(fatalErrors, err)
					queryErr = err.Error()
					break
				}
				endpoint := ws.PromEndpoint
				if ws.PromError != nil || endpoint == "" {
					err := fmt.Errorf("missing Prometheus endpoint for workspace %q, query %q", q.Workspace, q.Title)
					err = errors.Join(err, ws.PromError)
					fatalErrors = append(fatalErrors, err)
					queryErr = err.Error()
					break
				}

				logger.Info("executing PromQL query", "panel", panel.Title, "title", q.Title, "workspace", q.Workspace)

				query := resolveReportRange(q.Query, o.TimeWindow.Start, o.TimeWindow.End)
				// Keep q.Query in sync with the resolved query so the report
				// footer (queryFooter) shows what was actually executed
				// instead of the unresolved __REPORT_RANGE__ placeholder.
				q.Query = query
				resp, err := deps.queryRange(ctx, httpClient, o.cred, endpoint, query, o.TimeWindow.Start, o.TimeWindow.End, q.Step)
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

		html, err := deps.renderPanel(pageData)
		if err != nil {
			logger.Error(err, "failed to render panel", "panel", panel.Title)
		}
		tabs = append(tabs, observabilityTab{Title: panel.Title, HTML: string(incompleteHTML(html, err))})
		logger.Info("rendered panel tab", "panel", panel.Title, "charts", len(panelCharts))
	}
	return tabs, errors.Join(fatalErrors...)
}
