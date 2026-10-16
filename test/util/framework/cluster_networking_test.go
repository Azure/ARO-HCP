// Copyright 2026 Microsoft Corporation
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

package framework

import (
	"encoding/json"
	"maps"
	"reflect"
	"strings"
	"testing"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestBuildClusterNetworking(t *testing.T) {
	for _, disableSwift := range []bool{true, false} {
		for _, inputTags := range []map[string]*string{
			nil,
			{},
			{"user": to.Ptr("value")},
			{"user": to.Ptr("value"), metadataapi.TagClusterDisableSwift: to.Ptr("true")},
			{metadataapi.TagClusterDisableSwift: to.Ptr("false")},
			{metadataapi.TagClusterDisableSwift: nil},
			{strings.ToUpper(metadataapi.TagClusterDisableSwift): to.Ptr("true")},
		} {
			original := maps.Clone(inputTags)
			tags, subnet := buildClusterNetworking(disableSwift, inputTags, "integration-subnet")
			for key := range tags {
				if strings.EqualFold(key, metadataapi.TagClusterDisableSwift) && key != metadataapi.TagClusterDisableSwift {
					t.Fatalf("builder retained noncanonical networking tag %q", key)
				}
			}
			if !reflect.DeepEqual(inputTags, original) {
				t.Fatalf("DisableSwift=%t mutated input tags: got %v, want %v", disableSwift, inputTags, original)
			}
			if disableSwift {
				if subnet != nil || ptr.Deref(tags[metadataapi.TagClusterDisableSwift], "") != "true" {
					t.Fatalf("non-SWIFT requires a true tag and nil subnet: tags=%v, subnet=%v", tags, subnet)
				}
			} else {
				if _, exists := tags[metadataapi.TagClusterDisableSwift]; exists || ptr.Deref(subnet, "") != "integration-subnet" {
					t.Fatalf("SWIFT requires no disable tag and the provisioned subnet: tags=%v, subnet=%v", tags, subnet)
				}
			}
			if !reflect.DeepEqual(tags["user"], inputTags["user"]) {
				t.Fatal("builder did not preserve user tag")
			}
			if tags != nil {
				tags["user"] = to.Ptr("changed")
				if !reflect.DeepEqual(inputTags, original) {
					t.Fatal("output tag map aliases input tag map")
				}
			}
		}
	}
}

func TestBuildHCPClusterNetworking(t *testing.T) {
	// Keep the default constructors from querying the release service.
	t.Setenv("ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION", "4.21.0")
	t.Setenv("ARO_HCP_OPENSHIFT_LATEST_Z_STREAM", "false")

	builders := map[string]func(bool, map[string]*string, string, string) (any, error){
		"20251223": func(disableSwift bool, tags map[string]*string, subnet, visibility string) (any, error) {
			params := NewDefaultClusterParams20251223()
			if !params.DisableSwift {
				t.Fatal("20251223 default must disable SWIFT")
			}
			params.DisableSwift, params.Tags, params.VnetIntegrationSubnetID = disableSwift, tags, subnet
			params.APIVisibility, params.KeyVaultVisibility = visibility, visibility
			return BuildHCPClusterFromParams20251223(params, "test-location", nil)
		},
		"20260630": func(disableSwift bool, tags map[string]*string, subnet, visibility string) (any, error) {
			params := NewDefaultClusterParams20260630()
			if !params.DisableSwift {
				t.Fatal("20260630 default must disable SWIFT")
			}
			params.DisableSwift, params.Tags, params.VnetIntegrationSubnetID = disableSwift, tags, subnet
			params.APIVisibility, params.KeyVaultVisibility, params.IngressType = visibility, visibility, visibility
			return BuildHCPClusterFromParams20260630(params, "test-location", nil)
		},
		"20260901": func(disableSwift bool, tags map[string]*string, subnet, visibility string) (any, error) {
			params := NewDefaultClusterParams20260901()
			if !params.DisableSwift {
				t.Fatal("20260901 default must disable SWIFT")
			}
			params.DisableSwift, params.Tags, params.VnetIntegrationSubnetID = disableSwift, tags, subnet
			params.APIVisibility, params.KeyVaultVisibility, params.IngressType = visibility, visibility, visibility
			return BuildHCPClusterFromParams20260901(params, "test-location", nil)
		},
		"20261001": func(disableSwift bool, tags map[string]*string, subnet, visibility string) (any, error) {
			params := NewDefaultClusterParams20261001()
			if !params.DisableSwift {
				t.Fatal("20261001 default must disable SWIFT")
			}
			params.DisableSwift, params.Tags, params.VnetIntegrationSubnetID = disableSwift, tags, subnet
			params.APIVisibility, params.KeyVaultVisibility, params.IngressType = visibility, visibility, visibility
			return BuildHCPClusterFromParams20261001(params, "test-location", nil)
		},
	}
	for version, build := range builders {
		t.Run(version, func(t *testing.T) {
			for _, tc := range []struct {
				name         string
				disableSwift bool
				tags         map[string]*string
				subnet       string
				visibility   string
			}{
				{name: "non-SWIFT after infrastructure", disableSwift: true, subnet: "integration-subnet", visibility: "Public"},
				{name: "non-SWIFT without infrastructure", disableSwift: true, visibility: "Public"},
				{name: "non-SWIFT replacement tags and private visibility", disableSwift: true, tags: map[string]*string{"user": to.Ptr("value")}, subnet: "integration-subnet", visibility: "Private"},
				{name: "SWIFT public", subnet: "integration-subnet", visibility: "Public"},
				{name: "SWIFT overrides disable tag", tags: map[string]*string{"user": to.Ptr("value"), metadataapi.TagClusterDisableSwift: to.Ptr("true")}, subnet: "integration-subnet", visibility: "Private"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					cluster, err := build(tc.disableSwift, tc.tags, tc.subnet, tc.visibility)
					if err != nil {
						t.Fatal(err)
					}
					data, err := json.Marshal(cluster)
					if err != nil {
						t.Fatal(err)
					}
					var payload struct {
						Tags       map[string]*string `json:"tags"`
						Properties struct {
							Platform map[string]json.RawMessage `json:"platform"`
						} `json:"properties"`
					}
					if err := json.Unmarshal(data, &payload); err != nil {
						t.Fatal(err)
					}
					if tc.disableSwift {
						if _, exists := payload.Properties.Platform["vnetIntegrationSubnetId"]; exists {
							t.Fatal("non-SWIFT payload must omit vnetIntegrationSubnetId entirely")
						}
						if ptr.Deref(payload.Tags[metadataapi.TagClusterDisableSwift], "") != "true" {
							t.Fatal("non-SWIFT payload must include disable-swift=true")
						}
					} else {
						if string(payload.Properties.Platform["vnetIntegrationSubnetId"]) != `"integration-subnet"` {
							t.Fatal("SWIFT payload must include provisioned integration subnet")
						}
						if _, exists := payload.Tags[metadataapi.TagClusterDisableSwift]; exists {
							t.Fatal("SWIFT payload must omit disable-swift tag")
						}
					}
					if !reflect.DeepEqual(payload.Tags["user"], tc.tags["user"]) {
						t.Fatal("payload must preserve user tags")
					}
				})
			}
		})
	}
}
