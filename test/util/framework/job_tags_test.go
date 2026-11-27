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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
)

func TestWithJobTags(t *testing.T) {
	for _, value := range []string{"job-123", "", " \t\n"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(JobNameEnvvar, value)
			t.Setenv(JobIDEnvvar, value)
			input := map[string]*string{
				"user": to.Ptr("keep"), metadataapi.TagClusterSizeOverride: to.Ptr("Minimal"),
				strings.ToUpper(ProwJobIDTag): to.Ptr("stale"), ProwJobNameTag: to.Ptr("stale"),
			}
			original := maps.Clone(input)
			got := WithJobTags(input)
			want := map[string]string{"user": "keep", metadataapi.TagClusterSizeOverride: "Minimal"}
			if strings.TrimSpace(value) != "" {
				want[ProwJobNameTag], want[ProwJobIDTag] = value, value
			}
			if !reflect.DeepEqual(derefTags(got), want) {
				t.Fatalf("tags = %v, want %v", derefTags(got), want)
			}
			got["user"] = to.Ptr("changed")
			if !reflect.DeepEqual(input, original) {
				t.Fatal("job tags mutated the caller's map")
			}
		})
	}
}

func TestResourceBuildersJobTags(t *testing.T) {
	t.Setenv(JobNameEnvvar, "job")
	t.Setenv(JobIDEnvvar, "123")
	t.Setenv("ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION", "4.21.0")
	t.Setenv("ARO_HCP_OPENSHIFT_NODEPOOL_VERSION", "4.21.0")
	t.Setenv("ARO_HCP_OPENSHIFT_LATEST_Z_STREAM", "false")
	// Exercise both defaults (used by PATCH callers) and manually built params.
	resources := []any{
		NewDefaultClusterParams20240610(), NewDefaultClusterParams20251223(), NewDefaultClusterParams20260630(), NewDefaultClusterParams20260901(), NewDefaultClusterParams20261001(), NewDefaultClusterParams20270201(),
		NewDefaultNodePoolParams20240610(), NewDefaultNodePoolParams20251223(), NewDefaultNodePoolParams20260630(), NewDefaultNodePoolParams20260901(), NewDefaultNodePoolParams20261001(), NewDefaultNodePoolParams20270201(),
		BuildHCPClusterFromParams20240610(ClusterParams20240610{}, "location"),
		BuildNodePoolFromParams20240610(NodePoolParams20240610{}, "location"),
		BuildNodePoolFromParams20251223(NodePoolParams20251223{}, "location"),
		BuildNodePoolFromParams20260630(NodePoolParams20260630{}, "location"),
		BuildNodePoolFromParams20260901(NodePoolParams20260901{}, "location"),
		BuildNodePoolFromParams20261001(NodePoolParams20261001{}, "location"),
		BuildNodePoolFromParams20270201(NodePoolParams20270201{}, "location"),
	}
	for _, build := range []func() (any, error){
		func() (any, error) {
			return BuildHCPClusterFromParams20251223(ClusterParams20251223{}, "location", nil)
		},
		func() (any, error) {
			return BuildHCPClusterFromParams20260630(ClusterParams20260630{}, "location", nil)
		},
		func() (any, error) {
			return BuildHCPClusterFromParams20260901(ClusterParams20260901{}, "location", nil)
		},
		func() (any, error) {
			return BuildHCPClusterFromParams20261001(ClusterParams20261001{}, "location", nil)
		},
		func() (any, error) {
			return BuildHCPClusterFromParams20270201(ClusterParams20270201{}, "location", nil)
		},
	} {
		resource, err := build()
		if err != nil {
			t.Fatal(err)
		}
		resources = append(resources, resource)
	}
	for _, resource := range resources {
		data, err := json.Marshal(resource)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct{ Tags map[string]string }
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Tags[ProwJobNameTag] != "job" || payload.Tags[ProwJobIDTag] != "123" {
			t.Fatalf("%T lost job attribution: %s", resource, data)
		}
	}
}
