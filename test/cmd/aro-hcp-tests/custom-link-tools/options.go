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

package customlinktools

import (
	"bytes"
	"compress/gzip"
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/utils/clock"

	"sigs.k8s.io/yaml"

	configtypes "github.com/Azure/ARO-Tools/config/types"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

//go:embed artifacts/*.html.tmpl
var templatesFS embed.FS

var endGracePeriodDuration = 45 * time.Minute

func mustReadArtifact(name string) []byte {
	ret, err := templatesFS.ReadFile("artifacts/" + name)
	if err != nil {
		panic(err)
	}
	return ret
}

func DefaultOptions() *RawOptions {
	return &RawOptions{}
}

// keeping these options consistent with the visualize command.
func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.TimingInputDir, "timing-input", opts.TimingInputDir, "Path to the directory holding timing outputs from an end-to-end test run.")
	cmd.Flags().StringVar(&opts.OutputDir, "output", opts.OutputDir, "Path to the directory where html will be written.")
	cmd.Flags().StringVar(&opts.RenderedConfig, "rendered-config", opts.RenderedConfig, "Path to the rendered configuration YAML file.")
	cmd.Flags().StringVar(&opts.StartTimeFallback, "start-time-fallback", opts.StartTimeFallback, "Optional RFC3339 time to use as start time fallback when steps and test timing are unavailable.")
	cmd.Flags().StringVar(&opts.SubscriptionID, "subscription-id", opts.SubscriptionID, "Optional Azure subscription ID to include in must-gather query commands.")

	return nil
}

type RawOptions struct {
	TimingInputDir    string
	OutputDir         string
	RenderedConfig    string
	StartTimeFallback string
	SubscriptionID    string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// KustoInfo holds the Kusto cluster connection details derived from configuration.
type KustoInfo struct {
	KustoName                      string
	KustoRegion                    string
	ServiceLogsDatabase            string
	HostedControlPlaneLogsDatabase string
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	TimingInputDir    string
	OutputDir         string
	RenderedConfig    string
	Steps             []pipeline.NodeInfo
	SvcClusterName    string
	MgmtClusterName   string
	Kusto             KustoInfo
	StartTimeFallback *time.Time
	SubscriptionID    string
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	for _, item := range []struct {
		flag  string
		name  string
		value *string
	}{
		{flag: "timing-input", name: "timing input dir", value: &o.TimingInputDir},
		{flag: "output", name: "output dir", value: &o.OutputDir},
		{flag: "rendered-config", name: "rendered config", value: &o.RenderedConfig},
	} {
		if item.value == nil || *item.value == "" {
			return nil, fmt.Errorf("the %s must be provided with --%s", item.name, item.flag)
		}
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

// kustoGeoToRegion maps the geoShortId segment from a kusto cluster name
// (format: hcp-<env>-<geoShortId>) to the Azure region it resides in.
// Derived from ARO-Tools/pkg/config/ev2config/config.yaml geoShortId→region mapping.
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

// resolveKustoRegion determines the Azure region for a given kusto cluster name.
// Dev environments (hcp-dev-*) all reside in eastus2.
// Public cloud names follow the format hcp-<env>-<geoShortId>[optional-suffix] and are looked up
// in kustoGeoToRegion. The geoShortId is always 2 characters; any trailing content
// (e.g. hcp-prod-ch2, hcp-stg-br-5) is ignored.
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

func configGetString(cfg configtypes.Configuration, path string) (string, error) {
	val, err := cfg.GetByPath(path)
	if err != nil {
		return "", err
	}
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("config value at %q is %T, not string", path, val)
	}
	return s, nil
}

func (o *ValidatedOptions) Complete(logger logr.Logger) (*Options, error) {
	// Load rendered config
	rawCfg, err := os.ReadFile(o.RenderedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read rendered config %s: %w", o.RenderedConfig, err)
	}
	var cfg configtypes.Configuration
	if err := yaml.Unmarshal(rawCfg, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rendered config: %w", err)
	}

	svcClusterName, err := configGetString(cfg, "svc.aks.name")
	if err != nil {
		return nil, fmt.Errorf("failed to get svc cluster name from config: %w", err)
	}
	mgmtClusterName, err := configGetString(cfg, "mgmt.aks.name")
	if err != nil {
		return nil, fmt.Errorf("failed to get mgmt cluster name from config: %w", err)
	}
	kustoName, err := configGetString(cfg, "kusto.kustoName")
	if err != nil {
		return nil, fmt.Errorf("failed to get kusto name from config: %w", err)
	}
	serviceLogsDB, err := configGetString(cfg, "kusto.serviceLogsDatabase")
	if err != nil {
		return nil, fmt.Errorf("failed to get service logs database from config: %w", err)
	}
	hcpLogsDB, err := configGetString(cfg, "kusto.hostedControlPlaneLogsDatabase")
	if err != nil {
		return nil, fmt.Errorf("failed to get hosted control plane logs database from config: %w", err)
	}

	kustoRegion, err := resolveKustoRegion(kustoName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve kusto region: %w", err)
	}

	// Try loading steps.yaml (optional — used for start time derivation)
	var steps []pipeline.NodeInfo
	compressedPath := path.Join(o.TimingInputDir, "steps.yaml.gz")
	uncompressedPath := path.Join(o.TimingInputDir, "steps.yaml")

	compressedData, err := os.ReadFile(compressedPath)
	if err == nil {
		gzipReader, err := gzip.NewReader(bytes.NewReader(compressedData))
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader for %s: %w", compressedPath, err)
		}
		defer gzipReader.Close()

		stepsYamlBytes, err := io.ReadAll(gzipReader)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress %s: %w", compressedPath, err)
		}
		if err := yaml.Unmarshal(stepsYamlBytes, &steps); err != nil {
			return nil, fmt.Errorf("failed to unmarshal steps file: %w", err)
		}
	} else {
		plainData, err := os.ReadFile(uncompressedPath)
		if err != nil {
			logger.Info("steps.yaml not found, service log links will use fallback start time", "compressed", compressedPath, "uncompressed", uncompressedPath)
		} else {
			if err := yaml.Unmarshal(plainData, &steps); err != nil {
				return nil, fmt.Errorf("failed to unmarshal steps file: %w", err)
			}
		}
	}

	var startTimeFallback *time.Time
	if o.StartTimeFallback != "" {
		t, err := time.Parse(time.RFC3339, o.StartTimeFallback)
		if err != nil {
			return nil, fmt.Errorf("failed to parse --start-time-fallback %q: %w", o.StartTimeFallback, err)
		}
		startTimeFallback = &t
	}

	return &Options{
		completedOptions: &completedOptions{
			Steps:             steps,
			OutputDir:         o.OutputDir,
			TimingInputDir:    o.TimingInputDir,
			RenderedConfig:    o.RenderedConfig,
			SvcClusterName:    svcClusterName,
			MgmtClusterName:   mgmtClusterName,
			StartTimeFallback: startTimeFallback,
			SubscriptionID:    o.SubscriptionID,
			Kusto: KustoInfo{
				KustoName:                      kustoName,
				KustoRegion:                    kustoRegion,
				ServiceLogsDatabase:            serviceLogsDB,
				HostedControlPlaneLogsDatabase: hcpLogsDB,
			},
		},
	}, nil
}

type TestRow struct {
	TestName          string
	ResourceGroupName string
	Database          string
	Status            string
	Links             []LinkDetails
}

type LinkDetails struct {
	DisplayName string
	URL         string
}

type TimingInfo struct {
	StartTime          time.Time
	EndTime            time.Time
	ResourceGroupNames []string
}

type QueryTemplate struct {
	TemplateName   string
	TemplatePath   string
	OutputFileName string
}

func createQueryURL(query kusto.Query, kustoInfo KustoInfo) string {
	currURL := url.URL{
		Scheme: "https",
		Host:   "dataexplorer.azure.com",
		Path:   fmt.Sprintf("clusters/%s.%s/databases/%s", kustoInfo.KustoName, kustoInfo.KustoRegion, query.GetDatabase()),
	}
	urlQuery := currURL.Query()
	urlQuery.Add("query", encodeKustoQuery(query.GetQuery().String()))
	currURL.RawQuery = urlQuery.Encode()
	return currURL.String()
}

func createLink(displayName string, query kusto.Query, kustoInfo KustoInfo) LinkDetails {
	return LinkDetails{
		DisplayName: displayName,
		URL:         createQueryURL(query, kustoInfo),
	}
}

// encodeKustoQuery gzips, then base64 encodes.  The URL encoding happens in the URL library
func encodeKustoQuery(query string) string {
	var buf bytes.Buffer

	// Create gzip writer
	gzipWriter := gzip.NewWriter(&buf)

	// Write the query string to gzip writer
	_, err := gzipWriter.Write([]byte(query))
	if err != nil {
		return ""
	}

	// Close gzip writer to flush data
	err = gzipWriter.Close()
	if err != nil {
		return ""
	}

	// Base64 encode the gzipped data
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func (o Options) Run(ctx context.Context) error {
	allTestRows := []TestRow{}

	timingInfo, err := loadAllTestTimingInfo(o.TimingInputDir)
	if err != nil {
		return utils.TrackError(err)
	}

	for testName, timing := range timingInfo {
		for _, rg := range timing.ResourceGroupNames {
			testFactory, err := kusto.NewQueryFactory()
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to create query factory: %w", err))
			}
			queryOpts := kusto.QueryOptions{
				ResourceGroupName: rg,
				TimestampMin:      timing.StartTime,
				TimestampMax:      timing.EndTime,
				Limit:             -1,
			}
			templateData := kusto.NewTemplateDataFromOptions(queryOpts)

			var links []LinkDetails

			customLinkQueries := []struct {
				queryName       string
				linkDisplayName string
			}{
				{
					queryName:       "hostedControlPlaneLogs",
					linkDisplayName: "Hosted Control Plane Logs",
				},
				{
					queryName:       "detailedServiceLogs",
					linkDisplayName: "Service Logs",
				},
				{
					queryName:       "debugQueries",
					linkDisplayName: "Debug Queries",
				},
			}

			for _, query := range customLinkQueries {
				queryDef, err := testFactory.GetCustomQueryDefinition(query.queryName)
				if err != nil {
					return utils.TrackError(fmt.Errorf("failed to get %s query definition: %w", query.queryName, err))
				}
				q, err := testFactory.BuildMerged(*queryDef, templateData)
				if err != nil {
					return utils.TrackError(fmt.Errorf("failed to build %s query: %w", query.queryName, err))
				}
				links = append(links, createLink(query.linkDisplayName, q, o.Kusto))
			}

			allTestRows = append(allTestRows, TestRow{
				TestName:          testName,
				ResourceGroupName: rg,
				Links:             links,
				Database:          o.Kusto.HostedControlPlaneLogsDatabase,
				Status:            "tbd",
			})
		}
	}

	err = renderTemplate(QueryTemplate{
		TemplateName:   "test-table",
		TemplatePath:   "custom-link-tools-test-table.html.tmpl",
		OutputFileName: path.Join(o.OutputDir, "custom-link-tools-test-table.html"),
	}, struct {
		Elements []TestRow
	}{
		Elements: allTestRows,
	})

	if err != nil {
		return utils.TrackError(err)
	}

	logger, _ := logr.FromContext(ctx)
	tw, err := computeTimeWindow(logger, o.Steps, timingInfo, o.StartTimeFallback)
	if err != nil {
		return utils.TrackError(err)
	}

	serviceLogLinks, err := getServiceLogLinks(logger, tw, o.SvcClusterName, o.MgmtClusterName, o.Kusto)
	if err != nil {
		return utils.TrackError(err)
	}

	err = renderTemplate(QueryTemplate{
		TemplateName:   "custom-link-tools",
		TemplatePath:   "custom-link-tools.html.tmpl",
		OutputFileName: path.Join(o.OutputDir, "custom-link-tools.html"),
	}, struct {
		Links []LinkDetails
	}{
		Links: serviceLogLinks,
	})
	if err != nil {
		return utils.TrackError(err)
	}

	commands := getMustGatherCommands(tw, o.SvcClusterName, o.MgmtClusterName, o.SubscriptionID, o.Kusto)

	var testCommands []TestCommandRow
	if len(timingInfo) > 0 {
		testCommands = getPerTestMustGatherCommands(timingInfo, o.SubscriptionID, o.Kusto)
	}

	err = renderTemplate(QueryTemplate{
		TemplateName:   "custom-link-tools-commands",
		TemplatePath:   "custom-link-tools-commands.html.tmpl",
		OutputFileName: path.Join(o.OutputDir, "custom-link-tools-commands.html"),
	}, struct {
		Commands     []CommandInfo
		TestCommands []TestCommandRow
	}{
		Commands:     commands,
		TestCommands: testCommands,
	})
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func renderTemplate(queryTemplate QueryTemplate, templateData interface{}) error {
	template, err := template.New(queryTemplate.TemplateName).Parse(string(mustReadArtifact(queryTemplate.TemplatePath)))
	if err != nil {
		return utils.TrackError(err)
	}
	outBytes := &bytes.Buffer{}
	if err := template.Execute(outBytes, templateData); err != nil {
		return utils.TrackError(err)
	}
	return os.WriteFile(path.Join(queryTemplate.OutputFileName), outBytes.Bytes(), 0644)
}

// loadTestTimingMetadata loads test timing metadata from the timing input directory.
// It returns a map of test identifier to timing information.
func loadAllTestTimingInfo(timingInputDir string) (map[string]TimingInfo, error) {
	var allTimingFiles []string
	err := filepath.Walk(timingInputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			fileName := filepath.Base(path)
			if (strings.HasSuffix(fileName, ".yaml") || strings.HasSuffix(fileName, ".yaml.gz")) && strings.HasPrefix(fileName, "timing-metadata-") {
				allTimingFiles = append(allTimingFiles, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var allTimingInfo = make(map[string]TimingInfo)

	for _, timingFile := range allTimingFiles {
		fileData, err := os.ReadFile(timingFile)
		if err != nil {
			return nil, err
		}

		var timingFileBytes []byte
		// Check if file is gzipped
		if strings.HasSuffix(timingFile, ".gz") {
			gzipReader, err := gzip.NewReader(bytes.NewReader(fileData))
			if err != nil {
				return nil, fmt.Errorf("failed to create gzip reader for %s: %w", timingFile, err)
			}
			defer gzipReader.Close()

			timingFileBytes, err = io.ReadAll(gzipReader)
			if err != nil {
				return nil, fmt.Errorf("failed to decompress %s: %w", timingFile, err)
			}
		} else {
			timingFileBytes = fileData
		}

		var timing timing.SpecTimingMetadata
		err = yaml.Unmarshal(timingFileBytes, &timing)
		if err != nil {
			return nil, err
		}
		deployment := strings.Join(timing.Identifier, " ")

		var rgNames = make(map[string]bool)
		for resourceGroup := range timing.Deployments {
			rgNames[resourceGroup] = true
		}

		rgNameList := make([]string, 0)
		for rgName := range rgNames {
			if rgName == "" {
				continue
			}
			rgNameList = append(rgNameList, rgName)
		}
		finishedAt, err := time.Parse(time.RFC3339, timing.FinishedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to parse finished at: %w", err)
		}
		startedAt, err := time.Parse(time.RFC3339, timing.StartedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to parse started at: %w", err)
		}

		allTimingInfo[deployment] = TimingInfo{
			StartTime:          startedAt,
			EndTime:            finishedAt.Add(endGracePeriodDuration),
			ResourceGroupNames: rgNameList,
		}
	}

	return allTimingInfo, nil
}

var localClock clock.PassiveClock = clock.RealClock{}

type TimeWindow struct {
	Start time.Time
	End   time.Time
}

func computeTimeWindow(logger logr.Logger, steps []pipeline.NodeInfo, testTimingInfo map[string]TimingInfo, startTimeFallback *time.Time) (TimeWindow, error) {
	// Determine earliest start time from steps
	earliestStartTime := time.Time{}
	startSource := ""
	for _, step := range steps {
		if len(step.Info.StartedAt) > 0 {
			startTime, err := time.Parse(time.RFC3339, step.Info.StartedAt)
			if err != nil {
				return TimeWindow{}, fmt.Errorf("failed to parse started at: %w", err)
			}
			if earliestStartTime.IsZero() || startTime.Before(earliestStartTime) {
				earliestStartTime = startTime
				startSource = "steps"
			}
		}
	}
	// Fallback: earliest test start time
	if earliestStartTime.IsZero() {
		for _, ti := range testTimingInfo {
			if earliestStartTime.IsZero() || ti.StartTime.Before(earliestStartTime) {
				earliestStartTime = ti.StartTime
				startSource = "test timing"
			}
		}
	}
	// Fallback: CLI-provided start time
	if earliestStartTime.IsZero() && startTimeFallback != nil {
		earliestStartTime = *startTimeFallback
		startSource = "CLI fallback"
	}
	// Final fallback: now - 3h
	if earliestStartTime.IsZero() {
		earliestStartTime = localClock.Now().Add(-3 * time.Hour)
		startSource = "clock (now-3h)"
	}

	// Determine end time from latest step FinishedAt + grace period
	endTime := time.Time{}
	endSource := ""
	for _, step := range steps {
		if len(step.Info.FinishedAt) > 0 {
			finishedTime, err := time.Parse(time.RFC3339, step.Info.FinishedAt)
			if err != nil {
				return TimeWindow{}, fmt.Errorf("failed to parse finished at: %w", err)
			}
			finishedWithGrace := finishedTime.Add(endGracePeriodDuration)
			if endTime.IsZero() || finishedWithGrace.After(endTime) {
				endTime = finishedWithGrace
				endSource = "steps (+45m grace)"
			}
		}
	}
	// Fallback: latest test end time (already includes grace period)
	if endTime.IsZero() {
		for _, ti := range testTimingInfo {
			if endTime.IsZero() || ti.EndTime.After(endTime) {
				endTime = ti.EndTime
				endSource = "test timing"
			}
		}
	}
	// Final fallback: now + 30min
	if endTime.IsZero() {
		endTime = localClock.Now().Add(30 * time.Minute)
		endSource = "clock (now+30m)"
	}

	logger.Info("service log query time window",
		"start", earliestStartTime.Format(time.RFC3339), "startSource", startSource,
		"end", endTime.Format(time.RFC3339), "endSource", endSource)

	return TimeWindow{Start: earliestStartTime, End: endTime}, nil
}

type CommandInfo struct {
	Label   string
	Command string
}

func getMustGatherCommands(tw TimeWindow, svcClusterName, mgmtClusterName, subscriptionID string, kusto KustoInfo) []CommandInfo {
	startStr := tw.Start.Format(time.DateTime)
	endStr := tw.End.Format(time.DateTime)

	return []CommandInfo{
		{
			Label:   "must-gather query-infra (SVC cluster)",
			Command: fmt.Sprintf(`hcpctl must-gather query-infra --kusto %s --region %s --timestamp-min "%s" --timestamp-max "%s" --infra-cluster %s`, kusto.KustoName, kusto.KustoRegion, startStr, endStr, svcClusterName),
		},
		{
			Label:   "must-gather query-infra (MGMT cluster)",
			Command: fmt.Sprintf(`hcpctl must-gather query-infra --kusto %s --region %s --timestamp-min "%s" --timestamp-max "%s" --infra-cluster %s`, kusto.KustoName, kusto.KustoRegion, startStr, endStr, mgmtClusterName),
		},
	}
}

type TestCommandRow struct {
	TestName string
	Command  string
}

func getPerTestMustGatherCommands(timingInfo map[string]TimingInfo, subscriptionID string, kusto KustoInfo) []TestCommandRow {
	rows := make([]TestCommandRow, 0, len(timingInfo))
	for testName, ti := range timingInfo {
		cmd := fmt.Sprintf(`hcpctl must-gather query --kusto %s --region %s --timestamp-min "%s" --timestamp-max "%s" --subscription-id %s`,
			kusto.KustoName, kusto.KustoRegion,
			ti.StartTime.Format(time.DateTime), ti.EndTime.Format(time.DateTime),
			subscriptionID)
		rows = append(rows, TestCommandRow{
			TestName: testName,
			Command:  cmd,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].TestName < rows[j].TestName
	})
	return rows
}

func getServiceLogLinks(logger logr.Logger, tw TimeWindow, svcClusterName, mgmtClusterName string, kustoInfo KustoInfo) ([]LinkDetails, error) {
	allLinks := []LinkDetails{}

	factory, err := kusto.NewQueryFactory()
	if err != nil {
		return nil, fmt.Errorf("failed to create query factory: %w", err)
	}
	svcOpts := kusto.QueryOptions{
		InfraClusterName: svcClusterName,
		TimestampMin:     tw.Start,
		TimestampMax:     tw.End,
		Limit:            -1,
	}

	// Service cluster queries: one per service log table + one merged link per custom query definition
	infraServiceLogsDef, err := factory.GetBuiltinQueryDefinition("infraServiceLogs")
	if err != nil {
		return nil, fmt.Errorf("failed to get infra service logs query definition: %w", err)
	}
	serviceTables := []struct {
		table       string
		displayName string
	}{
		{"backendLogs", "Backend Logs"},
		{"frontendLogs", "Frontend Logs"},
		{"clustersServiceLogs", "Clusters Service Logs"},
		{"containerLogs", "Maestro Logs"},
	}
	for _, st := range serviceTables {
		svcTemplateData := kusto.NewTemplateDataFromOptions(svcOpts, kusto.WithTable(st.table))
		q, err := factory.BuildMerged(*infraServiceLogsDef, svcTemplateData)
		if err != nil {
			return nil, fmt.Errorf("failed to build %s query: %w", st.table, err)
		}
		allLinks = append(allLinks, createLink(st.displayName, q, kustoInfo))
	}

	// Only include custom queries that are cluster-scoped (don't require ResourceGroupName)
	svcTemplateData := kusto.NewTemplateDataFromOptions(svcOpts, kusto.WithClusterName(svcClusterName))
	clusterScopedCustomQueries := []struct {
		queryName   string
		displayName string
	}{
		{"backendControllerConditions", "Backend Controller Conditions"},
		{"clustersServicePhases", "Clusters Service Phases"},
	}
	for _, cq := range clusterScopedCustomQueries {
		def, err := factory.GetCustomQueryDefinition(cq.queryName)
		if err != nil {
			return nil, fmt.Errorf("failed to get custom query definition %q: %w", cq.queryName, err)
		}
		q, err := factory.BuildMerged(*def, svcTemplateData)
		if err != nil {
			return nil, fmt.Errorf("failed to build custom query %q: %w", cq.queryName, err)
		}
		allLinks = append(allLinks, createLink(cq.displayName, q, kustoInfo))
	}

	// Management cluster queries
	mgmtOpts := kusto.QueryOptions{
		InfraClusterName: mgmtClusterName,
		TimestampMin:     tw.Start,
		TimestampMax:     tw.End,
		Limit:            -1,
	}
	mgmtTables := []struct {
		table       string
		displayName string
	}{
		{"containerLogs", "Hypershift Logs"},
		{"containerLogs", "ACM Logs"},
	}
	for _, mt := range mgmtTables {
		mgmtTemplateData := kusto.NewTemplateDataFromOptions(mgmtOpts, kusto.WithTable(mt.table))
		q, err := factory.BuildMerged(*infraServiceLogsDef, mgmtTemplateData)
		if err != nil {
			return nil, fmt.Errorf("failed to build mgmt %s query: %w", mt.displayName, err)
		}
		allLinks = append(allLinks, createLink(mt.displayName, q, kustoInfo))
	}

	return allLinks, nil
}
