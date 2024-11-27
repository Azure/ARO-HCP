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
		"__aksName__":                       "$config(aksName)",
		"__globalRG__":                      "$config(globalRG)",
		"__imageSyncRG__":                   "$config(imageSyncRG)",
		"__maestro_helm_chart__":            "$config(maestro_helm_chart)",
		"__maestro_image__":                 "$config(maestro_image)",
		"__managementClusterRG__":           "$config(managementClusterRG)",
		"__managementClusterSubscription__": "$config(managementClusterSubscription)",
		"__region__":                        "$config(region)",
		"__regionRG__":                      "$config(regionRG)",
		"__serviceClusterRG__":              "$config(serviceClusterRG)",
		"__serviceClusterSubscription__":    "$config(serviceClusterSubscription)",
		"__clusterService_imageTag__":       "$config(clusterService.imageTag)",
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
