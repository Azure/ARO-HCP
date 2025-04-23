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

package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/mcerepkg/internal/customize"
	"github.com/Azure/ARO-HCP/tooling/mcerepkg/internal/olm"

	"github.com/google/go-containerregistry/pkg/crane"
	yaml "gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

var (
	cmd = &cobra.Command{
		Use:   "mce-repkg",
		Short: "mce-repkg",
		Long:  "mce-repkg",
		RunE: func(cmd *cobra.Command, args []string) error {
			return buildChart(
				outputDir, mceBundle, sourceLink, scaffoldDir,
			)
		},
	}
	mceBundle   string
	outputDir   string
	scaffoldDir string
	sourceLink  string
)

func main() {
	cmd.Flags().StringVarP(&mceBundle, "mce-bundle", "b", "", "MCE OLM bundle image tgz")
	cmd.Flags().StringVarP(&scaffoldDir, "scaffold-dir", "s", "", "Directory containing additional templates to be added to the generated Helm Chart")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory for the generated Helm Chart")
	cmd.Flags().StringVarP(&sourceLink, "source-link", "l", "", "Link to the Bundle image that is repackaged")
	err := cmd.MarkFlagRequired("mce-bundle")
	if err != nil {
		log.Fatalf("failed to mark flag as required: %v", err)
	}
	err = cmd.MarkFlagRequired("output-dir")
	if err != nil {
		log.Fatalf("failed to mark flag as required: %v", err)
	}

	err = cmd.Execute()
	if err != nil {
		log.Fatalf("failed to build chart: %v", err)
	}
}

func buildChart(outputDir, mceOlmBundle, sourceLink, scaffoldDir string) error {
	ctx := context.Background()

	// load OLM bundle manifests
	img, err := crane.Load(mceOlmBundle)
	if err != nil {
		return fmt.Errorf("failed to load OLM bundle image: %v", err)
	}
	olmManifests, reg, err := olm.ExtractOLMBundleImage(ctx, img)
	if err != nil {
		return fmt.Errorf("failed to extract OLM bundle image: %v", err)
	}

	// sanity check manifests
	err = customize.SanityCheck(olmManifests)
	if err != nil {
		return fmt.Errorf("failed sanity checks on manifests: %v", err)
	}

	// load scaffolding manifests
	scaffoldManifests, err := customize.LoadScaffoldTemplates(scaffoldDir)
	if err != nil {
		return fmt.Errorf("failed to load scaffold templates: %v", err)
	}

	// customize manifests
	customizedManifests, values, err := customize.CustomizeManifests(append(olmManifests, scaffoldManifests...))
	if err != nil {
		return fmt.Errorf("failed to customize manifests: %v", err)
	}

	// build chart
	mceChart := &chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion:  "v2",
			Name:        "multicluster-engine",
			Description: "A Helm chart for multicluster-engine",
			Version:     reg.CSV.Spec.Version.String(),
			AppVersion:  reg.CSV.Spec.Version.String(),
			Type:        "application",
			Sources:     []string{sourceLink},
			Keywords:    reg.CSV.Spec.Keywords,
		},
	}
	var chartFiles []*chart.File

	// add values file
	valuesYaml, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("failed to marshal values to YAML: %v", err)
	}
	chartFiles = append(chartFiles, &chart.File{
		Name: "values.yaml",
		Data: valuesYaml,
	})

	// add manifests and CRDs
	for _, manifest := range customizedManifests {
		yamlData, err := yaml.Marshal(manifest.Object)

		if err != nil {
			return fmt.Errorf("failed to marshal object to YAML: %v", err)
		}

		path := fmt.Sprintf("templates/%s.%s.yaml", manifest.GetName(), strings.ToLower(manifest.GetKind()))
		if manifest.GetKind() == "CustomResourceDefinition" {
			path = fmt.Sprintf("crds/%s.yaml", manifest.GetName())
		}

		chartFiles = append(chartFiles, &chart.File{
			Name: path,
			Data: yamlData,
		})
	}
	mceChart.Templates = chartFiles

	// store chart
	err = chartutil.SaveDir(mceChart, outputDir)
	if err != nil {
		return fmt.Errorf("failed to save chart to directory: %v", err)
	}

	return nil
}
