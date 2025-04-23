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

package ev2

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/Azure/ARO-Tools/pkg/config"
)

func TestScopeBindingVariables(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	vars, err := ScopeBindingVariables(configProvider, "public", "int")
	if err != nil {
		t.Fatalf("ScopeBindingVariables failed: %v", err)
	}
	expectedVars := map[string]string{
		"__aksName__":                       "$config(aksName)",
		"__childZone__":                     "$config(childZone)",
		"__globalRG__":                      "$config(globalRG)",
		"__imageSyncRG__":                   "$config(imageSyncRG)",
		"__maestro_helm_chart__":            "$config(maestro_helm_chart)",
		"__maestro_image__":                 "$config(maestro_image)",
		"__managementClusterRG__":           "$config(managementClusterRG)",
		"__managementClusterSubscription__": "$config(managementClusterSubscription)",
		"__parentZone__":                    "$config(parentZone)",
		"__provider__":                      "$config(provider)",
		"__region__":                        "$config(region)",
		"__regionRG__":                      "$config(regionRG)",
		"__serviceClusterRG__":              "$config(serviceClusterRG)",
		"__serviceClusterSubscription__":    "$config(serviceClusterSubscription)",
		"__vaultBaseUrl__":                  "$config(vaultBaseUrl)",
		"__clusterService.imageTag__":       "$config(clusterService.imageTag)",
		"__clusterService.replicas__":       "$config(clusterService.replicas)",
		"__enableOptionalStep__":            "$config(enableOptionalStep)",
	}

	if diff := cmp.Diff(expectedVars, vars); diff != "" {
		t.Errorf("got incorrect vars: %v", diff)
	}
}
