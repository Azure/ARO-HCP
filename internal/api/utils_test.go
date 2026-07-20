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

func TestIsEmptyValue(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		expect bool
	}{
		// string
		{name: "empty string", value: "", expect: true},
		{name: "non-empty string", value: "hello", expect: false},
		// int32
		{name: "zero int32", value: int32(0), expect: true},
		{name: "non-zero int32", value: int32(1), expect: false},
		// bool
		{name: "false bool", value: false, expect: true},
		{name: "true bool", value: true, expect: false},
		// pointer
		{name: "nil pointer", value: (*string)(nil), expect: true},
		{name: "non-nil pointer", value: Ptr("x"), expect: false},
		// slice
		{name: "nil slice", value: ([]string)(nil), expect: true},
		{name: "empty slice", value: []string{}, expect: true},
		{name: "non-empty slice", value: []string{"a"}, expect: false},
		// map
		{name: "nil map", value: (map[string]string)(nil), expect: true},
		{name: "empty map", value: map[string]string{}, expect: true},
		{name: "non-empty map", value: map[string]string{"k": "v"}, expect: false},
		// struct
		{name: "zero struct", value: struct{ X int }{}, expect: true},
		{name: "non-zero struct", value: struct{ X int }{X: 1}, expect: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyValue(reflect.ValueOf(tt.value))
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestPtrOrNil(t *testing.T) {
	tests := []struct {
		name      string
		run       func() bool // returns true if result is nil
		expectNil bool
	}{
		{name: "empty string → nil", run: func() bool { return PtrOrNil("") == nil }, expectNil: true},
		{name: "non-empty string → ptr", run: func() bool { return PtrOrNil("x") == nil }, expectNil: false},
		{name: "zero int32 → nil", run: func() bool { return PtrOrNil(int32(0)) == nil }, expectNil: true},
		{name: "non-zero int32 → ptr", run: func() bool { return PtrOrNil(int32(42)) == nil }, expectNil: false},
		{name: "false bool → nil", run: func() bool { return PtrOrNil(false) == nil }, expectNil: true},
		{name: "true bool → ptr", run: func() bool { return PtrOrNil(true) == nil }, expectNil: false},
		{name: "nil *string → nil", run: func() bool { return PtrOrNil((*string)(nil)) == nil }, expectNil: true},
		{name: "non-nil *string → ptr", run: func() bool { return PtrOrNil(Ptr("y")) == nil }, expectNil: false},
		{name: "nil []string → nil", run: func() bool { return PtrOrNil(([]string)(nil)) == nil }, expectNil: true},
		{name: "empty []string → nil", run: func() bool { return PtrOrNil([]string{}) == nil }, expectNil: true},
		{name: "non-empty []string → ptr", run: func() bool { return PtrOrNil([]string{"a"}) == nil }, expectNil: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectNil, tt.run())
		})
	}
}

// TestPtrOrNil_Value verifies PtrOrNil returns a pointer to the original value when non-empty.
func TestPtrOrNil_Value(t *testing.T) {
	s := PtrOrNil("hello")
	require.NotNil(t, s)
	assert.Equal(t, "hello", *s)

	n := PtrOrNil(int32(7))
	require.NotNil(t, n)
	assert.Equal(t, int32(7), *n)
}

func TestDeref(t *testing.T) {
	t.Run("non-nil string pointer", func(t *testing.T) {
		assert.Equal(t, "hello", Deref(Ptr("hello")))
	})
	t.Run("nil string pointer returns zero value", func(t *testing.T) {
		assert.Equal(t, "", Deref((*string)(nil)))
	})
	t.Run("non-nil int32 pointer", func(t *testing.T) {
		assert.Equal(t, int32(42), Deref(Ptr(int32(42))))
	})
	t.Run("nil int32 pointer returns zero value", func(t *testing.T) {
		assert.Equal(t, int32(0), Deref((*int32)(nil)))
	})
	t.Run("non-nil bool pointer", func(t *testing.T) {
		assert.Equal(t, true, Deref(Ptr(true)))
	})
	t.Run("nil bool pointer returns false", func(t *testing.T) {
		assert.Equal(t, false, Deref((*bool)(nil)))
	})
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
