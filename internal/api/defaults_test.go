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

package api

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// TestTypeSpecDefaultsConsistency verifies that default values declared in the
// generated OpenAPI spec (from TypeSpec) match the canonical Go constants
// defined in defaults.go and enums.go.
//
// Several fields lack "default" annotations in the OpenAPI spec due to a
// TypeSpec bug (typespec-azure#1586). These are skipped with a comment.
func TestTypeSpecDefaultsConsistency(t *testing.T) {
	specPath := "../../api/redhatopenshift/resource-manager/Microsoft.RedHatOpenShift/hcpclusters/preview/2025-12-23-preview/openapi.json"
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

	// String defaults
	stringTests := []struct {
		name       string
		definition string
		property   string
		goDefault  string
	}{
		{"NetworkType", "NetworkProfile", "networkType", string(NetworkTypeOVNKubernetes)},
		{"PodCIDR", "NetworkProfile", "podCidr", DefaultNetworkPodCIDR},
		{"ServiceCIDR", "NetworkProfile", "serviceCidr", DefaultNetworkServiceCIDR},
		{"MachineCIDR", "NetworkProfile", "machineCidr", DefaultNetworkMachineCIDR},
		{"Visibility", "ApiProfile", "visibility", string(VisibilityPublic)},
		{"OutboundType", "PlatformProfile", "outboundType", string(OutboundTypeLoadBalancer)},
		{"DiskStorageAccountType", "OsDiskProfile", "diskStorageAccountType", string(DiskStorageAccountTypePremium_LRS)},
		{"EtcdKeyManagementMode", "EtcdDataEncryptionProfile", "keyManagementMode", string(EtcdDataEncryptionKeyManagementModeTypePlatformManaged)},
		{"ClusterImageRegistryState", "ClusterImageRegistryProfile", "state", string(ClusterImageRegistryProfileStateEnabled)},
	}

	for _, tc := range stringTests {
		t.Run(tc.name, func(t *testing.T) {
			specDefault, ok := getDefault(tc.definition, tc.property)
			if !ok {
				t.Fatalf("OpenAPI spec missing default for %s.%s", tc.definition, tc.property)
			}
			specStr, ok := specDefault.(string)
			if !ok {
				t.Fatalf("expected string default for %s.%s, got %T", tc.definition, tc.property, specDefault)
			}
			if specStr != tc.goDefault {
				t.Errorf("OpenAPI default = %q, Go constant = %q", specStr, tc.goDefault)
			}
		})
	}

	// Numeric defaults (JSON numbers are float64)
	numericTests := []struct {
		name       string
		definition string
		property   string
		goDefault  int32
	}{
		{"HostPrefix", "NetworkProfile", "hostPrefix", DefaultNetworkHostPrefix},
		{"OSDiskSizeGiB", "OsDiskProfile", "sizeGiB", DefaultNodePoolOSDiskSizeGiB},
	}

	for _, tc := range numericTests {
		t.Run(tc.name, func(t *testing.T) {
			specDefault, ok := getDefault(tc.definition, tc.property)
			if !ok {
				t.Fatalf("OpenAPI spec missing default for %s.%s", tc.definition, tc.property)
			}
			specNum, ok := specDefault.(float64)
			if !ok {
				t.Fatalf("expected numeric default for %s.%s, got %T", tc.definition, tc.property, specDefault)
			}
			if int32(specNum) != tc.goDefault {
				t.Errorf("OpenAPI default = %v, Go constant = %v", int32(specNum), tc.goDefault)
			}
		})
	}

	// Boolean defaults
	boolTests := []struct {
		name       string
		definition string
		property   string
		goDefault  bool
	}{
		{"AutoRepair", "NodePoolProperties", "autoRepair", DefaultNodePoolAutoRepair},
	}

	for _, tc := range boolTests {
		t.Run(tc.name, func(t *testing.T) {
			specDefault, ok := getDefault(tc.definition, tc.property)
			if !ok {
				t.Fatalf("OpenAPI spec missing default for %s.%s", tc.definition, tc.property)
			}
			specBool, ok := specDefault.(bool)
			if !ok {
				t.Fatalf("expected bool default for %s.%s, got %T", tc.definition, tc.property, specDefault)
			}
			if specBool != tc.goDefault {
				t.Errorf("OpenAPI default = %v, Go constant = %v", specBool, tc.goDefault)
			}
		})
	}

	// Fields that SHOULD have TypeSpec defaults but don't due to
	// typespec-azure#1586. This list documents the gap explicitly.
	missingAnnotations := []struct {
		definition string
		property   string
	}{
		{"VersionProfile", "channelGroup"},
		{"ClusterAutoscalingProfile", "maxPodGracePeriodSeconds"},
		{"ClusterAutoscalingProfile", "maxNodeProvisionTimeSeconds"},
		{"ClusterAutoscalingProfile", "podPriorityThreshold"},
	}
	for _, m := range missingAnnotations {
		t.Run(fmt.Sprintf("Missing_%s_%s", m.definition, m.property), func(t *testing.T) {
			_, ok := getDefault(m.definition, m.property)
			if ok {
				t.Errorf("TypeSpec bug may be fixed: %s.%s now has a default annotation — "+
					"add a consistency check above and remove this entry", m.definition, m.property)
			}
		})
	}
}
