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

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test/util/timing"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

//go:embed artifacts/*.tmpl
var templatesFS embed.FS

var endGracePeriodDuration = 45 * time.Minute

var (
	serviceClusterStepID = pipeline.Identifier{
		ServiceGroup:  "Microsoft.Azure.ARO.HCP.Service.Infra",
		ResourceGroup: "service",
		Step:          "cluster",
	}
)

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

	return nil
}

type RawOptions struct {
	TimingInputDir string
	OutputDir      string
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	TimingInputDir string
	OutputDir      string
	Steps          []pipeline.NodeInfo
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

func (o *ValidatedOptions) Complete(logger logr.Logger) (*Options, error) {
	// we consume steps.yaml (output of templatize and stored for us by the visualization) to determine the cluster name
	stepsYamlBytes, err := os.ReadFile(path.Join(o.TimingInputDir, "steps.yaml"))
	if err != nil {
		return nil, utils.TrackError(err)
	}

	var steps []pipeline.NodeInfo
	if err := yaml.Unmarshal(stepsYamlBytes, &steps); err != nil {
		return nil, fmt.Errorf("failed to unmarshal timing input file: %w", err)
	}
	return &Options{
		completedOptions: &completedOptions{
			Steps:          steps,
			OutputDir:      o.OutputDir,
			TimingInputDir: o.TimingInputDir,
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

func createQueryURL(templatePath string, info QueryInfo) string {
	currURL := url.URL{
		Scheme: "https",
		Host:   "dataexplorer.azure.com",
		Path:   fmt.Sprintf("clusters/hcp-dev-us.westus3/databases/%s", info.Database),
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

func createLinkForTest(displayName, templatePath string, info QueryInfo) LinkDetails {
	return LinkDetails{
		DisplayName: displayName,
		URL:         createQueryURL(templatePath, info),
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
						Database:          "HostedControlPlaneLogs",
						StartTime:         timing.StartTime.Format(time.RFC3339),
						EndTime:           timing.EndTime.Format(time.RFC3339),
					}),
					createLinkForTest("Service Logs", "service-logs.kql.tmpl", QueryInfo{
						ResourceGroupName: rg,
						Database:          "ServiceLogs",
						StartTime:         timing.StartTime.Format(time.RFC3339),
						EndTime:           timing.EndTime.Format(time.RFC3339),
					}),
				},
				Database: "HostedControlPlaneLogs",
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

	serviceLogLinks, err := getServiceLogLinks(o.Steps)
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
			if strings.HasSuffix(fileName, ".yaml") && strings.HasPrefix(fileName, "timing-metadata-") {
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
		timingFileBytes, err := os.ReadFile(timingFile)
		if err != nil {
			return nil, err
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

func getServiceLogLinks(steps []pipeline.NodeInfo) ([]LinkDetails, error) {
	allLinks := []LinkDetails{}

	earliestStartTime := time.Time{}
	allClusterNames := []string{}
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

		// we're looking for the service cluster's step to make a query for backend and frontend
		// forming like this so that we can easily add more steps (like the management cluster) that we want queries for
		if step.Identifier == serviceClusterStepID {
			if step.Details != nil && step.Details.ARM != nil {
				for _, operation := range step.Details.ARM.Operations {
					allClusterNames = append(allClusterNames, locateAllServiceClusters(operation)...)
				}
			}
		}
	}
	if earliestStartTime.IsZero() {
		earliestStartTime = localClock.Now().Add(-6 * time.Hour) // lots longer than default timeouts, but still shorter than forever
	}

	if len(allClusterNames) != 1 {
		return nil, fmt.Errorf("expecting only one service cluster, found %d: %s", len(allClusterNames), strings.Join(allClusterNames, ", "))
	}

	endTime := localClock.Now().Add(1 * time.Hour) // we need to include all cleanup, this is a good bet.
	for _, clusterName := range allClusterNames {
		allLinks = append(allLinks, createLinkForTest("Backend Logs", "backend-logs.kql.tmpl", QueryInfo{
			ResourceGroupName: clusterName,
			Database:          "ServiceLogs",
			ClusterName:       clusterName,
			StartTime:         earliestStartTime.Format(time.RFC3339),
			EndTime:           endTime.Format(time.RFC3339),
		}))
	}
	for _, clusterName := range allClusterNames {
		allLinks = append(allLinks, createLinkForTest("Frontend Logs", "frontend-logs.kql.tmpl", QueryInfo{
			ResourceGroupName: clusterName,
			Database:          "ServiceLogs",
			ClusterName:       clusterName,
			StartTime:         earliestStartTime.Format(time.RFC3339),
			EndTime:           endTime.Format(time.RFC3339),
		}))
	}
	for _, clusterName := range allClusterNames {
		allLinks = append(allLinks, createLinkForTest("Clusters Service Logs", "clusters-service-logs.kql.tmpl", QueryInfo{
			ResourceGroupName: clusterName,
			Database:          "ServiceLogs",
			ClusterName:       clusterName,
			StartTime:         earliestStartTime.Format(time.RFC3339),
			EndTime:           endTime.Format(time.RFC3339),
		}))
	}

	return allLinks, nil
}

func locateAllServiceClusters(operation pipeline.Operation) []string {
	allClusterNames := []string{}
	for _, currChild := range operation.Children {
		currClusterNames := locateAllServiceClusters(currChild)
		if len(currClusterNames) > 0 {
			allClusterNames = append(allClusterNames, currClusterNames...)
		}
	}
	if operation.Resource == nil {
		return allClusterNames
	}
	if operation.OperationType == "Create" && operation.Resource.ResourceType == "Microsoft.ContainerService/managedClusters" {
		allClusterNames = append(allClusterNames, operation.Resource.Name)
	}
	return allClusterNames
}
