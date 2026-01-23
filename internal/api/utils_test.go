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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/validation/field"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func TestTrimStringSlice(t *testing.T) {
	tests := []struct {
		name   string
		in     []string
		expect []string
	}{
		{
			name:   "nil input",
			in:     nil,
			expect: nil,
		},
		{
			name: "Slice with white space",
			in: []string{
				"   leading-white-space",
				"trailing-white-space   ",
				// Based on asciiSpace in strings.go
				"\t\n\v\f\r ",
				"no-white-space",
			},
			expect: []string{
				"leading-white-space",
				"trailing-white-space",
				"no-white-space",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, TrimStringSlice(tt.in))
		})
	}
}

func TestMergeStringPtrMapIntoResourceIDMap(t *testing.T) {
	// Helper to create valid resource IDs for testing
	makeResourceID := func(name string) string {
		return "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + name
	}
	parseResourceID := func(name string) *azcorearm.ResourceID {
		return Must(azcorearm.ParseResourceID(makeResourceID(name)))
	}

	tests := []struct {
		name   string
		src    map[string]*string
		dst    map[string]*azcorearm.ResourceID
		expect map[string]*azcorearm.ResourceID
	}{
		{
			name:   "No source map and no destination map",
			src:    nil,
			dst:    nil,
			expect: nil,
		},
		{
			name: "No source map but existing destination map",
			src:  nil,
			dst: map[string]*azcorearm.ResourceID{
				"Blinky": parseResourceID("Shadow"),
			},
			expect: map[string]*azcorearm.ResourceID{
				"Blinky": parseResourceID("Shadow"),
			},
		},
		{
			name: "Add entry to a new map",
			src: map[string]*string{
				"Blinky": Ptr(makeResourceID("Shadow")),
			},
			dst: nil,
			expect: map[string]*azcorearm.ResourceID{
				"Blinky": parseResourceID("Shadow"),
			},
		},
		{
			name: "Add entry to an existing map",
			src: map[string]*string{
				"Blinky": Ptr(makeResourceID("Shadow")),
			},
			dst: map[string]*azcorearm.ResourceID{
				"Pinky": parseResourceID("Speedy"),
				"Inky":  parseResourceID("Bashful"),
				"Clyde": parseResourceID("Pokey"),
			},
			expect: map[string]*azcorearm.ResourceID{
				"Blinky": parseResourceID("Shadow"),
				"Pinky":  parseResourceID("Speedy"),
				"Inky":   parseResourceID("Bashful"),
				"Clyde":  parseResourceID("Pokey"),
			},
		},
		{
			name: "Delete entry from a non-existent map",
			src: map[string]*string{
				"Blinky": nil,
			},
			dst:    nil,
			expect: nil,
		},
		{
			name: "Delete entry from an existing map",
			src: map[string]*string{
				"Blinky": nil,
			},
			dst: map[string]*azcorearm.ResourceID{
				"Blinky": parseResourceID("Shadow"),
				"Pinky":  parseResourceID("Speedy"),
				"Inky":   parseResourceID("Bashful"),
				"Clyde":  parseResourceID("Pokey"),
			},
			expect: map[string]*azcorearm.ResourceID{
				"Pinky": parseResourceID("Speedy"),
				"Inky":  parseResourceID("Bashful"),
				"Clyde": parseResourceID("Pokey"),
			},
		},
		{
			name: "Both add and delete entries from an existing map",
			src: map[string]*string{
				"Blinky": nil,
				"Pinky":  nil,
				"Inky":   Ptr(makeResourceID("Bashful")),
				"Clyde":  Ptr(makeResourceID("Pokey")),
			},
			dst: map[string]*azcorearm.ResourceID{
				"Blinky": parseResourceID("Shadow"),
				"Inky":   parseResourceID("Bashful"),
			},
			expect: map[string]*azcorearm.ResourceID{
				"Inky":  parseResourceID("Bashful"),
				"Clyde": parseResourceID("Pokey"),
			},
		},
		{
			name: "Modify an entry in an existing map",
			src: map[string]*string{
				"Blinky": Ptr(makeResourceID("Oikake")),
			},
			dst: map[string]*azcorearm.ResourceID{
				"Blinky": parseResourceID("Shadow"),
				"Inky":   parseResourceID("Bashful"),
			},
			expect: map[string]*azcorearm.ResourceID{
				"Blinky": parseResourceID("Oikake"),
				"Inky":   parseResourceID("Bashful"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errList := MergeStringPtrMapIntoResourceIDMap(field.NewPath("new"), tt.src, &tt.dst)
			require.Len(t, errList, 0, "unexpected errors: %v", errList)

			// Compare the maps by converting to string representation
			if tt.expect == nil {
				assert.Nil(t, tt.dst)
			} else {
				require.NotNil(t, tt.dst)
				assert.Equal(t, len(tt.expect), len(tt.dst))
				for key, expectedVal := range tt.expect {
					actualVal, exists := tt.dst[key]
					assert.True(t, exists, "key %s should exist in dst", key)
					if exists {
						assert.Equal(t, expectedVal.String(), actualVal.String(), "value mismatch for key %s", key)
					}
				}
			}
		})
	}
}

func TestNonNilSliceValues(t *testing.T) {
	a, b, c := Ptr("A"), Ptr("B"), Ptr("C")
	testCases := []struct {
		name string
		s    []*string
		want []*string
	}{
		{name: "nil slice", s: nil, want: nil},
		{name: "empty slice", s: []*string{}, want: nil},
		{name: "no nil", s: []*string{a, b, c}, want: []*string{a, b, c}},
		{name: "nil start", s: []*string{nil, a, b, c}, want: []*string{a, b, c}},
		{name: "nil end", s: []*string{a, b, c, nil}, want: []*string{a, b, c}},
		{name: "nil mid", s: []*string{a, b, nil, c}, want: []*string{a, b, c}},
		{name: "all nil", s: []*string{nil, nil, nil, nil}, want: nil},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var got []*string
			for _, x := range NonNilSliceValues(tc.s) {
				got = append(got, x)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("NonNilSliceValues() = %v, want %v", got, tc.want)
			}
		})
	}
}
