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

package azureapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
)

// TestTypeSpecDefaultsConsistency verifies that default values declared in the
// generated OpenAPI spec (from TypeSpec) match the canonical Go constants
// defined in coreapi's defaults.go and metadataapi's enums.go.
func TestTypeSpecDefaultsConsistency(t *testing.T) {
	specPath := "../../api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters/stable/2026-10-01/openapi.json"
	specData, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("failed to read OpenAPI spec: %v", err)
	}

	var spec struct {
		Definitions map[string]struct {
			Properties map[string]struct {
				Default any `json:"default"`
			} `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(specData, &spec); err != nil {
		t.Fatalf("failed to parse OpenAPI spec: %v", err)
	}

	getDefault := func(definition, property string) (any, bool) {
		def, ok := spec.Definitions[definition]
		if !ok {
			return nil, false
		}
		prop, ok := def.Properties[property]
		if !ok {
			return nil, false
		}
		return prop.Default, prop.Default != nil
	}

	// All defaults - using generic type to test string, numeric, and boolean values
	tests := []struct {
		name       string
		definition string
		property   string
		goDefault  any
	}{
		// String defaults
		{"NetworkType", "NetworkProfile", "networkType", string(metadataapi.NetworkTypeOVNKubernetes)},
		{"PodCIDR", "NetworkProfile", "podCidr", coreapi.DefaultClusterNetworkPodCIDR},
		{"ServiceCIDR", "NetworkProfile", "serviceCidr", coreapi.DefaultClusterNetworkServiceCIDR},
		{"MachineCIDR", "NetworkProfile", "machineCidr", coreapi.DefaultClusterNetworkMachineCIDR},
		{"Visibility", "ApiProfile", "visibility", string(metadataapi.VisibilityPublic)},
		{"OutboundType", "PlatformProfile", "outboundType", string(metadataapi.OutboundTypeLoadBalancer)},
		{"DiskStorageAccountType", "OsDiskProfile", "diskStorageAccountType", string(metadataapi.DiskStorageAccountTypePremium_LRS)},
		{"ClusterImageRegistryState", "ClusterImageRegistryProfile", "state", string(metadataapi.ClusterImageRegistryStateEnabled)},
		{"ClusterChannelGroup", "VersionProfile", "channelGroup", coreapi.DefaultClusterVersionChannelGroup},
		{"NodePoolChannelGroup", "NodePoolVersionProfile", "channelGroup", coreapi.DefaultNodePoolVersionChannelGroup},
		{"UsernameClaimPrefixPolicy", "UsernameClaimProfile", "prefixPolicy", string(metadataapi.UsernameClaimPrefixPolicyNone)},
		// Numeric defaults
		{"HostPrefix", "NetworkProfile", "hostPrefix", coreapi.DefaultClusterNetworkHostPrefix},
		{"OSDiskSizeGiB", "OsDiskProfile", "sizeGiB", coreapi.DefaultNodePoolOSDiskSizeGiB},
		{"MaxPodGracePeriodSeconds", "ClusterAutoscalingProfile", "maxPodGracePeriodSeconds", coreapi.DefaultClusterMaxPodGracePeriodSeconds},
		{"MaxNodeProvisionTimeSeconds", "ClusterAutoscalingProfile", "maxNodeProvisionTimeSeconds", coreapi.DefaultClusterMaxNodeProvisionTimeSeconds},
		{"PodPriorityThreshold", "ClusterAutoscalingProfile", "podPriorityThreshold", coreapi.DefaultClusterPodPriorityThreshold},
		// Boolean defaults
		{"AutoRepair", "NodePoolProperties", "autoRepair", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			specDefault, ok := getDefault(tc.definition, tc.property)
			if !ok {
				t.Fatalf("OpenAPI spec missing default for %s.%s", tc.definition, tc.property)
			}

			// Type assertion and comparison based on goDefault type
			switch expected := tc.goDefault.(type) {
			case string:
				specStr, ok := specDefault.(string)
				if !ok {
					t.Fatalf("expected string default for %s.%s, got %T", tc.definition, tc.property, specDefault)
				}
				if specStr != expected {
					t.Errorf("OpenAPI default = %q, Go constant = %q", specStr, expected)
				}
			case int32:
				specNum, ok := specDefault.(float64)
				if !ok {
					t.Fatalf("expected numeric default for %s.%s, got %T", tc.definition, tc.property, specDefault)
				}
				if int32(specNum) != expected {
					t.Errorf("OpenAPI default = %v, Go constant = %v", int32(specNum), expected)
				}
			case bool:
				specBool, ok := specDefault.(bool)
				if !ok {
					t.Fatalf("expected bool default for %s.%s, got %T", tc.definition, tc.property, specDefault)
				}
				if specBool != expected {
					t.Errorf("OpenAPI default = %v, Go constant = %v", specBool, expected)
				}
			default:
				t.Fatalf("unsupported goDefault type %T for %s", tc.goDefault, tc.name)
			}
		})
	}
}

// TestPatchSchemasExcludeDefaults follows references from each PATCH body so
// defaults cannot silently reappear in nested models shared with PUT requests.
func TestPatchSchemasExcludeDefaults(t *testing.T) {
	const specDir = "../../api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpopenshiftclusters"
	for _, status := range []string{"preview", "stable"} {
		specPaths, err := filepath.Glob(filepath.Join(specDir, status, "*", "openapi.json"))
		if err != nil || len(specPaths) == 0 {
			t.Fatalf("finding %s API specs: paths=%v, err=%v", status, specPaths, err)
		}
		for _, specPath := range specPaths {
			t.Run(filepath.Base(filepath.Dir(specPath)), func(t *testing.T) {
				data, err := os.ReadFile(specPath)
				if err != nil {
					t.Fatal(err)
				}
				var spec struct {
					Definitions map[string]any `json:"definitions"`
					Paths       map[string]map[string]struct {
						Parameters []struct {
							In     string `json:"in"`
							Schema any    `json:"schema"`
						} `json:"parameters"`
					} `json:"paths"`
				}
				if err := json.Unmarshal(data, &spec); err != nil {
					t.Fatal(err)
				}
				patchBodies := 0
				for path, operations := range spec.Paths {
					operation, ok := operations["patch"]
					if !ok {
						continue
					}
					for _, parameter := range operation.Parameters {
						if parameter.In != "body" {
							continue
						}
						patchBodies++
						seen := map[string]bool{}
						var check func(any, string)
						check = func(value any, location string) {
							switch value := value.(type) {
							case map[string]any:
								if defaultValue, ok := value["default"]; ok {
									t.Errorf("PATCH %s schema %s contains default %v", path, location, defaultValue)
								}
								if ref, ok := value["$ref"].(string); ok && strings.HasPrefix(ref, "#/definitions/") && !seen[ref] {
									seen[ref] = true
									definition, ok := spec.Definitions[strings.TrimPrefix(ref, "#/definitions/")]
									if !ok {
										t.Fatalf("unresolved schema reference %s", ref)
									}
									check(definition, ref)
								}
								for key, child := range value {
									check(child, location+"/"+key)
								}
							case []any:
								for _, child := range value {
									check(child, location)
								}
							}
						}
						check(parameter.Schema, "body")
					}
				}
				// Avoid passing without inspecting any PATCH request bodies.
				if patchBodies == 0 {
					t.Error("no PATCH request bodies checked")
				}
			})
		}
	}
}
