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

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

//go:embed artifacts/*.tmpl
var templatesFS embed.FS

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
	Steps     []pipeline.NodeInfo
	OutputDir string
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

var (
	serviceClusterStepID = pipeline.Identifier{
		ServiceGroup:  "Microsoft.Azure.ARO.HCP.Service.Infra",
		ResourceGroup: "service",
		Step:          "cluster",
	}

	managementClusterQueries = map[string]string{
		"Backend Logs": `database('ServiceLogs').table('backendLogs') 
| where cluster == '%s'
| where container_name == 'aro-hcp-backend'
| project timestamp, msg, log`,

		"Frontend Logs": `database('ServiceLogs').table('frontendLogs') 
| where cluster == '%s'
| where container_name == 'aro-hcp-frontend'
| project timestamp, msg, log`,
	}
)

type LinkDetails struct {
	DisplayName string
	URL         string
}

func createLinksForServiceCluster(clusterName string) []LinkDetails {
	ret := []LinkDetails{}
	for _, displayName := range sets.StringKeySet(managementClusterQueries).List() {
		query := managementClusterQueries[displayName]
		completedQuery := fmt.Sprintf(query, clusterName)
		currURL := url.URL{
			Scheme: "https",
			Host:   "dataexplorer.azure.com",
			Path:   "clusters/hcp-dev-us.westus3/databases/HostedControlPlaneLogs",
		}
		urlQuery := currURL.Query()
		urlQuery.Add("query", encodeKustoQuery(completedQuery))
		currURL.RawQuery = urlQuery.Encode()
		ret = append(ret, LinkDetails{
			DisplayName: displayName,
			URL:         currURL.String(),
		})
		fmt.Printf("#### template URL is %v\n", currURL.String())
	}

	return ret
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
			Steps:     steps,
			OutputDir: o.OutputDir,
		},
	}, nil
}

func locateAllServiceClusters(operation pipeline.Operation) []LinkDetails {
	var allLinks []LinkDetails
	for _, currChild := range operation.Children {
		currChildLinks := locateAllServiceClusters(currChild)
		if currChildLinks != nil {
			allLinks = append(allLinks, currChildLinks...)
		}
	}
	if operation.Resource == nil {
		return nil
	}
	if operation.OperationType == "Create" && operation.Resource.ResourceType == "Microsoft.ContainerService/managedClusters" {
		clusterName := operation.Resource.Name
		newLinks := createLinksForServiceCluster(clusterName)
		allLinks = append(allLinks, newLinks...)
	}

	return allLinks
}

func (o Options) Run(ctx context.Context) error {
	// TODO read which tests have failed and harvest the resourcegroups so we can create links direct to the logs related to that resource-group

	allLinks := []LinkDetails{}
	for _, step := range o.Steps {
		// we're looking for the service cluster's step to make a query for backend and frontend
		// forming like this so that we can easily add more steps (like the management cluster) that we want queries for
		if step.Identifier == serviceClusterStepID {
			if step.Details != nil && step.Details.ARM != nil {
				for _, operation := range step.Details.ARM.Operations {
					allLinks = append(allLinks, locateAllServiceClusters(operation)...)
				}
			}
		}
	}

	allLinks = append(allLinks, LinkDetails{
		DisplayName: "README",
		URL:         "readme.html",
	})

	customLinkToolsTemplate, err := template.New("custom-link-tools").Parse(string(mustReadArtifact("custom-link-tools.tmpl")))
	if err != nil {
		return utils.TrackError(err)
	}
	// Create template data with allLinks as Links
	templateData := struct {
		Links []LinkDetails
	}{
		Links: allLinks,
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
