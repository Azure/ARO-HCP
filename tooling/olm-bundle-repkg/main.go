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

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	yaml "sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/customize"
	"github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/olm"
	"github.com/Azure/ARO-HCP/tooling/olm-bundle-repkg/internal/rukpak/convert"
)

var (
	cmd = &cobra.Command{
		Use:   "olm-bundle-repkg",
		Short: "olm-bundle-repkg",
		Long:  "olm-bundle-repkg",
		RunE: func(cmd *cobra.Command, args []string) error {
			return buildChart(
				outputDir, olmBundle, sourceLink, scaffoldDir,
				configFile, chartName, chartDescription, crdChartName, valuesFileName,
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
	crdChartName     string
	valuesFileName   string
)

func main() {
	// Original flags
	cmd.Flags().StringVarP(&olmBundle, "olm-bundle", "b", "", "OLM bundle input with protocol prefix: oci:// for bundle images, file:// for manifest directories")
	cmd.Flags().StringVarP(&scaffoldDir, "scaffold-dir", "s", "", "Directory containing additional templates to be added to the generated Helm Chart")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory for the generated Helm Charts")
	cmd.Flags().StringVarP(&sourceLink, "source-link", "l", "", "Link to the Bundle image that is repackaged")

	// Configuration flags
	cmd.Flags().StringVarP(&configFile, "config", "c", "", "Path to configuration file (YAML)")
	cmd.Flags().StringVar(&chartName, "chart-name", "", "Override chart name")
	cmd.Flags().StringVar(&chartDescription, "chart-description", "", "Override chart description")
	cmd.Flags().StringVar(&crdChartName, "crd-chart-name", "", "Name for a separate CRD chart (if not specified, CRDs will be included in the main chart)")
	cmd.Flags().StringVar(&valuesFileName, "values-file-name", "values.yaml", "Name for the generated values file (default: values.yaml)")

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

func buildChart(outputDir, olmBundle, sourceLink, scaffoldDir, configFile, chartName, chartDescription, crdChartName,
	valuesFileName string) error {
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
	customizedManifests, autoGeneratedValues, err := customize.CustomizeManifests(append(olmManifests, scaffoldManifests...), config)
	if err != nil {
		return fmt.Errorf("failed to customize manifests: %v", err)
	}

	// Load values from scaffold directory if present
	scaffoldValues, err := customize.LoadScaffoldValues(scaffoldDir, valuesFileName)
	if err != nil {
		return fmt.Errorf("failed to load scaffold values: %v", err)
	}

	// Merge values in order of precedence: auto-generated < scaffold
	// Scaffold values take precedence and provide the complete set of chart values
	mergedValues := autoGeneratedValues
	if scaffoldValues != nil {
		mergedValues = customize.MergeMaps(autoGeneratedValues, scaffoldValues)
	}

	// Load scaffold template files (raw Helm templates that should be copied verbatim)
	scaffoldTemplateFiles, err := customize.GetScaffoldTemplateFiles(scaffoldDir)
	if err != nil {
		return fmt.Errorf("failed to load scaffold template files: %v", err)
	}

	// separate CRDs from other manifests
	var crdManifests []unstructured.Unstructured
	var nonCRDManifests []unstructured.Unstructured

	for _, manifest := range customizedManifests {
		if manifest.GetKind() == "CustomResourceDefinition" {
			crdManifests = append(crdManifests, manifest)
		} else {
			nonCRDManifests = append(nonCRDManifests, manifest)
		}
	}

	if crdChartName != "" && len(crdManifests) > 0 {
		// Create dedicated CRD chart (no scaffold templates for CRD chart)
		err = createCRDChart(outputDir, config, reg, sourceLink, crdManifests, crdChartName)
		if err != nil {
			return fmt.Errorf("failed to create CRD chart: %v", err)
		}
	}

	// Create main chart with appropriate manifests
	var mainChartManifests []unstructured.Unstructured
	if crdChartName != "" {
		// Only include non-CRD manifests if using dedicated CRD chart
		mainChartManifests = nonCRDManifests
	} else {
		// Include all manifests if not using dedicated CRD chart
		mainChartManifests = customizedManifests
	}

	err = createChart(
		config.ChartName,
		config.ChartDescription,
		reg.CSV.Spec.Version.String(),
		sourceLink,
		reg.CSV.Spec.Keywords,
		mainChartManifests,
		mergedValues,
		outputDir,
		false, // don't add CRDs as templates
		valuesFileName,
		scaffoldTemplateFiles,
	)
	if err != nil {
		return fmt.Errorf("failed to create main chart: %v", err)
	}

	return nil
}

// createChart creates a Helm chart with the given parameters
func createChart(name, description, version, sourceLink string, keywords []string, manifests []unstructured.Unstructured,
	values interface{}, outputDir string, crdsAsTemplates bool, valuesFileName string, scaffoldTemplates map[string][]byte) error {
	// Create chart metadata
	chartMeta := &chart.Metadata{
		APIVersion:  "v2",
		Name:        name,
		Description: description,
		Version:     version,
		AppVersion:  version,
		Type:        "application",
		Sources:     []string{sourceLink},
		Keywords:    keywords,
	}

	helmChart := &chart.Chart{
		Metadata: chartMeta,
	}

	var chartFiles []*chart.File

	// Add values file
	valuesData, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("failed to marshal values to YAML: %v", err)
	}
	chartFiles = append(chartFiles, &chart.File{
		Name: valuesFileName,
		Data: valuesData,
	})

	// Add manifests
	for _, manifest := range manifests {
		yamlData, err := yaml.Marshal(manifest.Object)
		if err != nil {
			return fmt.Errorf("failed to marshal manifest to YAML: %v", err)
		}

		var path string
		if crdsAsTemplates || manifest.GetKind() != "CustomResourceDefinition" {
			// For CRD chart, all manifests go to templates/
			// For regular chart, non-CRD manifests go to templates/
			path = fmt.Sprintf("templates/%s.%s.yaml", manifest.GetName(), strings.ToLower(manifest.GetKind()))
		} else {
			// For regular chart, CRDs go to crds/
			path = fmt.Sprintf("crds/%s.yaml", manifest.GetName())
		}

		chartFiles = append(chartFiles, &chart.File{
			Name: path,
			Data: yamlData,
		})
	}

	// Add scaffold template files (raw Helm templates)
	for relPath, content := range scaffoldTemplates {
		chartFiles = append(chartFiles, &chart.File{
			Name: fmt.Sprintf("templates/%s", relPath),
			Data: content,
		})
	}

	helmChart.Templates = chartFiles

	// Save chart
	err = chartutil.SaveDir(helmChart, outputDir)
	if err != nil {
		return fmt.Errorf("failed to save chart to directory: %v", err)
	}

	return nil
}

func createCRDChart(outputDir string, config *customize.BundleConfig, reg convert.RegistryV1, sourceLink string,
	crdManifests []unstructured.Unstructured, crdChartName string) error {
	crdChartDescription := config.ChartDescription + " - CRDs"
	crdValues := map[string]interface{}{}

	return createChart(
		crdChartName,
		crdChartDescription,
		reg.CSV.Spec.Version.String(),
		sourceLink,
		reg.CSV.Spec.Keywords,
		crdManifests,
		crdValues,
		outputDir,
		true,                // add CRDs as templates
		"values.yaml",       // CRD charts always use default values.yaml name
		map[string][]byte{}, // no scaffold templates for CRD chart
	)
}
