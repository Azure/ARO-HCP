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

	"github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/customize"
	"github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/olm"
	"github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/rukpak/convert"

	"github.com/google/go-containerregistry/pkg/crane"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yaml "sigs.k8s.io/yaml"
)

var (
	cmd = &cobra.Command{
		Use:   "olm-bundle-repkg",
		Short: "olm-bundle-repkg",
		Long:  "olm-bundle-repkg",
		RunE: func(cmd *cobra.Command, args []string) error {
			return buildChart(
				outputDir, olmBundle, sourceLink, scaffoldDir,
				configFile, chartName, chartDescription,
			)
		},
	}
	olmBundle        string
	outputDir        string
	scaffoldDir      string
	sourceLink       string
	configFile       string
	chartName        string
	chartDescription string
)

func main() {
	// Original flags
	cmd.Flags().StringVarP(&olmBundle, "olm-bundle", "b", "", "OLM bundle input with protocol prefix: oci:// for bundle images, file:// for manifest directories")
	cmd.Flags().StringVarP(&scaffoldDir, "scaffold-dir", "s", "", "Directory containing additional templates to be added to the generated Helm Chart")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory for the generated Helm Chart")
	cmd.Flags().StringVarP(&sourceLink, "source-link", "l", "", "Link to the Bundle image that is repackaged")

	// Configuration flags
	cmd.Flags().StringVarP(&configFile, "config", "c", "", "Path to configuration file (YAML)")
	cmd.Flags().StringVar(&chartName, "chart-name", "", "Override chart name")
	cmd.Flags().StringVar(&chartDescription, "chart-description", "", "Override chart description")

	err := cmd.MarkFlagRequired("olm-bundle")
	if err != nil {
		log.Fatalf("failed to mark flag as required: %v", err)
	}
	err = cmd.MarkFlagRequired("output-dir")
	if err != nil {
		log.Fatalf("failed to mark flag as required: %v", err)
	}
	err = cmd.MarkFlagRequired("config")
	if err != nil {
		log.Fatalf("failed to mark flag as required: %v", err)
	}

	err = cmd.Execute()
	if err != nil {
		log.Fatalf("failed to build chart: %v", err)
	}
}

func buildChart(outputDir, olmBundle, sourceLink, scaffoldDir, configFile, chartName, chartDescription string) error {
	ctx := context.Background()

	// Load bundle configuration
	config, err := customize.LoadFromFile(configFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %v", err)
	}

	// Apply CLI overrides
	if chartName != "" {
		config.ChartName = chartName
	}
	if chartDescription != "" {
		config.ChartDescription = chartDescription
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %v", err)
	}

	// Detect input type and load OLM bundle manifests
	var olmManifests []unstructured.Unstructured
	var reg convert.RegistryV1

	// Parse protocol prefix to determine input type
	switch {
	case strings.HasPrefix(olmBundle, "oci://"):
		// Load manifests from bundle image
		bundlePath := strings.TrimPrefix(olmBundle, "oci://")
		img, err := crane.Load(bundlePath)
		if err != nil {
			return fmt.Errorf("failed to load OLM bundle image: %v", err)
		}
		olmManifests, reg, err = olm.ExtractOLMBundleImage(ctx, img)
		if err != nil {
			return fmt.Errorf("failed to extract OLM bundle image: %v", err)
		}
	case strings.HasPrefix(olmBundle, "file://"):
		// Load manifests from directory
		manifestsDir := strings.TrimPrefix(olmBundle, "file://")
		olmManifests, reg, err = olm.ExtractOLMManifestsDirectory(ctx, manifestsDir)
		if err != nil {
			return fmt.Errorf("failed to extract OLM manifests from directory: %v", err)
		}
	default:
		return fmt.Errorf("invalid OLM bundle input: must use oci:// prefix for bundle images or file:// prefix for manifest directories")
	}

	// sanity check manifests
	err = customize.SanityCheck(olmManifests, config)
	if err != nil {
		return fmt.Errorf("failed sanity checks on manifests: %v", err)
	}

	// load scaffolding manifests
	scaffoldManifests, err := customize.LoadScaffoldTemplates(scaffoldDir)
	if err != nil {
		return fmt.Errorf("failed to load scaffold templates: %v", err)
	}

	// customize manifests
	customizedManifests, values, err := customize.CustomizeManifests(append(olmManifests, scaffoldManifests...), config)
	if err != nil {
		return fmt.Errorf("failed to customize manifests: %v", err)
	}

	// build chart
	operatorChart := &chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion:  "v2",
			Name:        config.ChartName,
			Description: config.ChartDescription,
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
	operatorChart.Templates = chartFiles

	// store chart
	err = chartutil.SaveDir(operatorChart, outputDir)
	if err != nil {
		return fmt.Errorf("failed to save chart to directory: %v", err)
	}

	return nil
}
