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
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/utils/clock"

	"sigs.k8s.io/yaml"

	configtypes "github.com/Azure/ARO-Tools/config/types"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

//go:embed artifacts/*.tmpl
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

	return nil
}

type RawOptions struct {
	TimingInputDir string
	OutputDir      string
	RenderedConfig string
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
	TimingInputDir   string
	OutputDir        string
	RenderedConfig   string
	Steps            []pipeline.NodeInfo
	SvcClusterName   string
	MgmtClusterName  string
	Kusto            KustoInfo
	ConfigFileModTime time.Time
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
// Public cloud names follow the format hcp-<env>-<geoShortId> and are looked up in kustoGeoToRegion.
func resolveKustoRegion(kustoName string) (string, error) {
	if strings.HasPrefix(kustoName, "hcp-dev-") {
		return "eastus2", nil
	}
	// format: hcp-<env>-<geoShortId>, e.g. hcp-int-us, hcp-prod-eu
	parts := strings.SplitN(kustoName, "-", 3)
	if len(parts) == 3 {
		if region, ok := kustoGeoToRegion[parts[2]]; ok {
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

	// Get config file modification time for use as fallback start time
	configStat, err := os.Stat(o.RenderedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to stat rendered config %s: %w", o.RenderedConfig, err)
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

	return &Options{
		completedOptions: &completedOptions{
			Steps:             steps,
			OutputDir:         o.OutputDir,
			TimingInputDir:    o.TimingInputDir,
			RenderedConfig:    o.RenderedConfig,
			SvcClusterName:    svcClusterName,
			MgmtClusterName:   mgmtClusterName,
			ConfigFileModTime: configStat.ModTime(),
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

type QueryInfo struct {
	ResourceGroupName string
	StartTime         string
	EndTime           string
	ClusterName       string
	Database          string
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

func createQueryURL(templatePath string, info QueryInfo, kusto KustoInfo) string {
	currURL := url.URL{
		Scheme: "https",
		Host:   "dataexplorer.azure.com",
		Path:   fmt.Sprintf("clusters/%s.%s/databases/%s", kusto.KustoName, kusto.KustoRegion, info.Database),
	}
	urlQuery := currURL.Query()
	template, err := template.New("custom-link-tools").Parse(string(mustReadArtifact(templatePath)))
	if err != nil {
		return ""
	}
	var buf bytes.Buffer
	if err := template.Execute(&buf, info); err != nil {
		return ""
	}
	urlQuery.Add("query", encodeKustoQuery(buf.String()))
	currURL.RawQuery = urlQuery.Encode()
	return currURL.String()
}

func createLinkForTest(displayName, templatePath string, info QueryInfo, kusto KustoInfo) LinkDetails {
	return LinkDetails{
		DisplayName: displayName,
		URL:         createQueryURL(templatePath, info, kusto),
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
			allTestRows = append(allTestRows, TestRow{
				TestName:          testName,
				ResourceGroupName: rg,
				Links: []LinkDetails{
					createLinkForTest("Hosted Control Plane Logs", "hosted-controlplane.kql.tmpl", QueryInfo{
						ResourceGroupName: rg,
						Database:          o.Kusto.HostedControlPlaneLogsDatabase,
						StartTime:         timing.StartTime.Format(time.RFC3339),
						EndTime:           timing.EndTime.Format(time.RFC3339),
					}, o.Kusto),
					createLinkForTest("Service Logs", "service-logs.kql.tmpl", QueryInfo{
						ResourceGroupName: rg,
						Database:          o.Kusto.ServiceLogsDatabase,
						StartTime:         timing.StartTime.Format(time.RFC3339),
						EndTime:           timing.EndTime.Format(time.RFC3339),
					}, o.Kusto),
				},
				Database: o.Kusto.HostedControlPlaneLogsDatabase,
				Status:   "tbd",
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

	serviceLogLinks, err := getServiceLogLinks(o.Steps, o.SvcClusterName, o.MgmtClusterName, o.Kusto, o.ConfigFileModTime)
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

func getServiceLogLinks(steps []pipeline.NodeInfo, svcClusterName, mgmtClusterName string,
	kusto KustoInfo, configFileModTime time.Time) ([]LinkDetails, error) {
	allLinks := []LinkDetails{}

	// Determine earliest start time from steps, falling back to config file modification time
	earliestStartTime := time.Time{}
	for _, step := range steps {
		if len(step.Info.StartedAt) > 0 {
			startTime, err := time.Parse(time.RFC3339, step.Info.StartedAt)
			if err != nil {
				return nil, fmt.Errorf("failed to parse started at: %w", err)
			}
			if earliestStartTime.IsZero() || startTime.Before(earliestStartTime) {
				earliestStartTime = startTime
			}
		}
	}
	if earliestStartTime.IsZero() {
		if !configFileModTime.IsZero() {
			earliestStartTime = configFileModTime
		} else {
			earliestStartTime = localClock.Now().Add(-6 * time.Hour)
		}
	}

	endTime := localClock.Now().Add(1 * time.Hour)

	// Service cluster components
	svcComponents := []struct {
		component string
		template  string
	}{
		{"Backend Logs", "backend-logs.kql.tmpl"},
		{"Frontend Logs", "frontend-logs.kql.tmpl"},
		{"Clusters Service Logs", "clusters-service-logs.kql.tmpl"},
		{"Maestro Logs", "maestro-logs.kql.tmpl"},
	}

	for _, comp := range svcComponents {
		allLinks = append(allLinks, createLinkForTest(comp.component, comp.template, QueryInfo{
			ResourceGroupName: svcClusterName,
			Database:          kusto.ServiceLogsDatabase,
			ClusterName:       svcClusterName,
			StartTime:         earliestStartTime.Format(time.RFC3339),
			EndTime:           endTime.Format(time.RFC3339),
		}, kusto))
	}

	// Management cluster components
	mgmtComponents := []struct {
		component string
		template  string
	}{
		{"Hypershift Logs", "hypershift-logs.kql.tmpl"},
		{"ACM Logs", "acm-logs.kql.tmpl"},
	}

	for _, comp := range mgmtComponents {
		allLinks = append(allLinks, createLinkForTest(comp.component, comp.template, QueryInfo{
			ResourceGroupName: mgmtClusterName,
			Database:          kusto.ServiceLogsDatabase,
			ClusterName:       mgmtClusterName,
			StartTime:         earliestStartTime.Format(time.RFC3339),
			EndTime:           endTime.Format(time.RFC3339),
		}, kusto))
	}

	return allLinks, nil
}
