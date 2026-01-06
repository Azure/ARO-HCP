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

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test/util/timing"
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
	return &Options{
		completedOptions: &completedOptions{
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
	Database          string
}

type TimingInfo struct {
	StartTime          string
	EndTime            string
	ResourceGroupNames []string
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

	timingInfo, err := gatherTimingInfo(o.TimingInputDir)
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
						StartTime:         timing.StartTime,
						EndTime:           timing.EndTime,
					}),
					createLinkForTest("Service Logs", "service-logs.kql.tmpl", QueryInfo{
						ResourceGroupName: rg,
						Database:          "ServiceLogs",
						StartTime:         timing.StartTime,
						EndTime:           timing.EndTime,
					}),
				},
				Database: "HostedControlPlaneLogs",
				Status:   "tbd",
			})
		}
	}

	customLinkToolsTemplate, err := template.New("custom-link-tools").Parse(string(mustReadArtifact("custom-link-tools.tmpl")))
	if err != nil {
		return utils.TrackError(err)
	}
	// Create template data with allLinks as Links
	templateData := struct {
		Elements []TestRow
	}{
		Elements: allTestRows,
	}
	outBytes := &bytes.Buffer{}
	if err := customLinkToolsTemplate.Execute(outBytes, templateData); err != nil {
		return utils.TrackError(err)
	}
	if err := os.WriteFile(path.Join(o.OutputDir, "custom-link-tools.html"), outBytes.Bytes(), 0644); err != nil {
		return utils.TrackError(err)
	}

	readmeTemplate, err := template.New("readme").Parse(string(mustReadArtifact("readme.tmpl")))
	if err != nil {
		return utils.TrackError(err)
	}
	outBytes = &bytes.Buffer{}
	if err := readmeTemplate.Execute(outBytes, nil); err != nil {
		return utils.TrackError(err)
	}
	if err := os.WriteFile(path.Join(o.OutputDir, "readme.html"), outBytes.Bytes(), 0644); err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func gatherTimingInfo(sharedDir string) (map[string]TimingInfo, error) {
	var allTimingFiles []string
	err := filepath.Walk(sharedDir, func(path string, info os.FileInfo, err error) error {
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
		allTimingInfo[deployment] = TimingInfo{
			StartTime:          timing.StartedAt,
			EndTime:            finishedAt.Add(endGracePeriodDuration).Format(time.RFC3339),
			ResourceGroupNames: rgNameList,
		}
	}

	return allTimingInfo, nil
}
