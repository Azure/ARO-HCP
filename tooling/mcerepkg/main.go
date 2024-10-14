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
				outputDir, mceBundle, scaffoldDir,
			)
		},
	}
	mceBundle   string
	outputDir   string
	scaffoldDir string
)

func main() {
	cmd.Flags().StringVarP(&mceBundle, "mce-bundle", "b", "", "MCE OLM bundle image tgz")
	cmd.Flags().StringVarP(&scaffoldDir, "scaffold-dir", "s", "", "Directory containing additional templates to be added to the generated Helm Chart")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Output directory for the generated Helm Chart")
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

func buildChart(outputDir string, mceOlmBundle string, scaffoldDir string) error {
	ctx := context.Background()

	// load OLM bundle manifests
	img, err := crane.Load(mceOlmBundle)
	if err != nil {
		return err
	}
	olmManifests, reg, err := olm.ExtractOLMBundleImage(ctx, img)
	if err != nil {
		return err
	}

	// sanity check manifests
	err = customize.SanityCheck(olmManifests)
	if err != nil {
		return fmt.Errorf("failed sanity checks on manifests: %v", err)
	}

	// load scaffolding manifests
	scaffoldManifests, err := customize.LoadScaffoldTemplates(scaffoldDir)
	if err != nil {
		return err
	}

	// customize manifests
	customizedManifests, values, err := customize.CustomizeManifests(append(olmManifests, scaffoldManifests...))
	if err != nil {
		return err
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
			Sources:     []string{mceOlmBundle},
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
		return err
	}

	return nil
}
