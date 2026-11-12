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
	"maps"
	"reflect"
	"testing"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestTagsForPatch(t *testing.T) {
	sizeOverride := map[string]*string{
		metadataapi.TagClusterSizeOverride: to.Ptr(string(coreapi.MinimalControlPlanePodSizing)),
	}

	for _, tc := range []struct {
		name      string
		base      map[string]*string
		overrides map[string]*string
		want      map[string]*string
	}{
		{
			name: "no overrides preserves the base",
			base: sizeOverride,
			want: sizeOverride,
		},
		{
			name:      "override adds a key alongside the base",
			base:      sizeOverride,
			overrides: map[string]*string{"test": to.Ptr("value")},
			want: map[string]*string{
				metadataapi.TagClusterSizeOverride: to.Ptr(string(coreapi.MinimalControlPlanePodSizing)),
				"test":                             to.Ptr("value"),
			},
		},
		{
			name:      "override replaces an existing value",
			base:      map[string]*string{"test": to.Ptr("old")},
			overrides: map[string]*string{"test": to.Ptr("new")},
			want:      map[string]*string{"test": to.Ptr("new")},
		},
		{
			name:      "nil override value removes the key",
			base:      map[string]*string{"test": to.Ptr("value"), "keep": to.Ptr("kept")},
			overrides: map[string]*string{"test": nil},
			want:      map[string]*string{"keep": to.Ptr("kept")},
		},
		{
			name:      "nil override value for an absent key is a no-op",
			base:      sizeOverride,
			overrides: map[string]*string{"test": nil},
			want:      sizeOverride,
		},
		{
			name:      "nil base yields just the overrides",
			overrides: map[string]*string{"test": to.Ptr("value")},
			want:      map[string]*string{"test": to.Ptr("value")},
		},
		{
			// A PATCH body must always carry an explicit tag set, so the result is
			// never nil: a nil Tags map would be omitted from the body and leave the
			// resource's existing tags in place.
			name: "nil base and nil overrides yield an empty, non-nil map",
			want: map[string]*string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := maps.Clone(tc.base)

			got := TagsForPatch(tc.base, tc.overrides)

			if got == nil {
				t.Fatal("TagsForPatch returned a nil map, which would omit tags from the PATCH body")
			}
			if !reflect.DeepEqual(derefTags(got), derefTags(tc.want)) {
				t.Fatalf("TagsForPatch = %v, want %v", derefTags(got), derefTags(tc.want))
			}
			if !reflect.DeepEqual(tc.base, original) {
				t.Fatalf("TagsForPatch mutated the base map: got %v, want %v", derefTags(tc.base), derefTags(original))
			}
		})
	}
}

func derefTags(tags map[string]*string) map[string]string {
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = ptr.Deref(value, "<nil>")
	}
	return out
}
