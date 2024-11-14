package ev2

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-HCP/tooling/templatize/internal/testutil"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func TestScopeBindingVariables(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	vars, err := ScopeBindingVariables(configProvider, "public", "int")
	if err != nil {
		t.Fatalf("ScopeBindingVariables failed: %v", err)
	}
	expectedVars := map[string]string{
		"__AKSNAME__":                       "$config(aksName)",
		"__GLOBALRG__":                      "$config(globalRG)",
		"__IMAGESYNCRG__":                   "$config(imageSyncRG)",
		"__MAESTRO_HELM_CHART__":            "$config(maestro_helm_chart)",
		"__MAESTRO_IMAGE__":                 "$config(maestro_image)",
		"__MANAGEMENTCLUSTERRG__":           "$config(managementClusterRG)",
		"__MANAGEMENTCLUSTERSUBSCRIPTION__": "$config(managementClusterSubscription)",
		"__REGION__":                        "$config(region)",
		"__REGIONRG__":                      "$config(regionRG)",
		"__SERVICECLUSTERRG__":              "$config(serviceClusterRG)",
		"__SERVICECLUSTERSUBSCRIPTION__":    "$config(serviceClusterSubscription)",
		"__CLUSTERSERVICE_IMAGETAG__":       "$config(clusterService.imageTag)",
	}

	if diff := cmp.Diff(expectedVars, vars); diff != "" {
		t.Errorf("got incorrect vars: %v", diff)
	}
}

func TestPreprocessFileForEV2SystemVars(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	content, err := PreprocessFileForEV2SystemVars(configProvider, "public", "int", "../../testdata/pipeline.yaml")
	if err != nil {
		t.Fatalf("PreprocessFileForEV2SystemVars failed: %v", err)
	}
	testutil.CompareWithFixture(t, content, testutil.WithExtension(".yaml"))
}

func TestPreprocessFileForEV2ScopeBinding(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	content, err := PreprocessFileForEV2ScopeBinding(configProvider, "public", "int", "../../testdata/test.bicepparam")
	if err != nil {
		t.Fatalf("PreprocessFileForEV2ScopeBinding failed: %v", err)
	}
	testutil.CompareWithFixture(t, content, testutil.WithExtension(".bicepparam"))
}
