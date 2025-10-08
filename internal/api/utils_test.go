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
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
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

func TestMergeStringPtrMap(t *testing.T) {
	tests := []struct {
		name   string
		src    map[string]*string
		dst    map[string]string
		expect map[string]string
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
			dst: map[string]string{
				"Blinky": "Shadow",
			},
			expect: map[string]string{
				"Blinky": "Shadow",
			},
		},
		{
			name: "Add entry to a new map",
			src: map[string]*string{
				"Blinky": Ptr("Shadow"),
			},
			dst: nil,
			expect: map[string]string{
				"Blinky": "Shadow",
			},
		},
		{
			name: "Add entry to an existing map",
			src: map[string]*string{
				"Blinky": Ptr("Shadow"),
			},
			dst: map[string]string{
				"Pinky": "Speedy",
				"Inky":  "Bashful",
				"Clyde": "Pokey",
			},
			expect: map[string]string{
				"Blinky": "Shadow",
				"Pinky":  "Speedy",
				"Inky":   "Bashful",
				"Clyde":  "Pokey",
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
			dst: map[string]string{
				"Blinky": "Shadow",
				"Pinky":  "Speedy",
				"Inky":   "Bashful",
				"Clyde":  "Pokey",
			},
			expect: map[string]string{
				"Pinky": "Speedy",
				"Inky":  "Bashful",
				"Clyde": "Pokey",
			},
		},
		{
			name: "Both add and delete entries from an existing map",
			src: map[string]*string{
				"Blinky": nil,
				"Pinky":  nil,
				"Inky":   Ptr("Bashful"),
				"Clyde":  Ptr("Pokey"),
			},
			dst: map[string]string{
				"Blinky": "Shadow",
				"Inky":   "Bashful",
			},
			expect: map[string]string{
				"Inky":  "Bashful",
				"Clyde": "Pokey",
			},
		},
		{
			name: "Modify an entry in an existing map",
			src: map[string]*string{
				"Blinky": Ptr("Oikake"),
			},
			dst: map[string]string{
				"Blinky": "Shadow",
				"Inky":   "Bashful",
			},
			expect: map[string]string{
				"Blinky": "Oikake",
				"Inky":   "Bashful",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			MergeStringPtrMap(tt.src, &tt.dst)
			if !reflect.DeepEqual(tt.expect, tt.dst) {
				t.Error(cmp.Diff(tt.expect, tt.dst))
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

func TestCopyReadOnlyValues(t *testing.T) {
	const (
		testClientID    = "33333333-3333-3333-3333-333333333333"
		testPrincipalID = "44444444-4444-4444-4444-444444444444"
	)

	now := time.Now()

	userAssignedIdentity1 := NewTestUserAssignedIdentity("userAssignedIdentity1")
	userAssignedIdentity2 := NewTestUserAssignedIdentity("userAssignedIdentity2")
	userAssignedIdentity3 := NewTestUserAssignedIdentity("userAssignedIdentity3")
	userAssignedIdentity4 := NewTestUserAssignedIdentity("userAssignedIdentity4")

	src := &ExternalTestResource{
		ID:       Ptr(TestClusterResourceID),
		Name:     Ptr(TestClusterName),
		Type:     Ptr(ClusterResourceType.String()),
		Location: Ptr("this should be overridden"),
		SystemData: &generated.SystemData{
			CreatedAt:     &now,
			CreatedBy:     Ptr("me"),
			CreatedByType: Ptr(generated.CreatedByTypeUser),
		},
		Identity: &generated.ManagedServiceIdentity{
			PrincipalID: Ptr(testPrincipalID),
			TenantID:    Ptr(TestTenantID),
			UserAssignedIdentities: map[string]*generated.UserAssignedIdentity{
				userAssignedIdentity2: {
					ClientID:    Ptr(testClientID),
					PrincipalID: Ptr(testPrincipalID),
				},
				userAssignedIdentity3: {
					ClientID:    Ptr(testClientID),
					PrincipalID: Ptr(testPrincipalID),
				},
				userAssignedIdentity4: {
					ClientID:    Ptr(testClientID),
					PrincipalID: Ptr(testPrincipalID),
				},
			},
		},
	}

	dst := &ExternalTestResource{
		ID:       Ptr("wrong read-only value"),
		Location: Ptr(TestLocation),
		Identity: &generated.ManagedServiceIdentity{
			Type: Ptr(generated.ManagedServiceIdentityTypeSystemAssignedUserAssigned),
			UserAssignedIdentities: map[string]*generated.UserAssignedIdentity{
				userAssignedIdentity1: nil,
				userAssignedIdentity2: nil,
				userAssignedIdentity3: {},
				userAssignedIdentity4: {
					ClientID:    nil,
					PrincipalID: nil,
				},
			},
		},
	}

	expect := &ExternalTestResource{
		ID:       Ptr("wrong read-only value"), // this would fail validation
		Name:     Ptr(TestClusterName),
		Type:     Ptr(ClusterResourceType.String()),
		Location: Ptr(TestLocation),
		SystemData: &generated.SystemData{
			CreatedAt:     &now,
			CreatedBy:     Ptr("me"),
			CreatedByType: Ptr(generated.CreatedByTypeUser),
		},
		Identity: &generated.ManagedServiceIdentity{
			PrincipalID: Ptr(testPrincipalID),
			TenantID:    Ptr(TestTenantID),
			Type:        Ptr(generated.ManagedServiceIdentityTypeSystemAssignedUserAssigned),
			UserAssignedIdentities: map[string]*generated.UserAssignedIdentity{
				userAssignedIdentity1: nil,
				userAssignedIdentity2: {
					ClientID:    Ptr(testClientID),
					PrincipalID: Ptr(testPrincipalID),
				},
				userAssignedIdentity3: {
					ClientID:    Ptr(testClientID),
					PrincipalID: Ptr(testPrincipalID),
				},
				userAssignedIdentity4: {
					ClientID:    Ptr(testClientID),
					PrincipalID: Ptr(testPrincipalID),
				},
			},
		},
	}

	CopyReadOnlyValues(src, dst)

	assert.Equal(t, expect, dst)

	// Slightly outside the intended scope of this
	// test but make sure it validates as expected.

	expectErrors := []arm.CloudErrorBody{
		{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: "Field 'id' cannot be updated",
			Target:  "id",
		},
		{
			Code:    arm.CloudErrorCodeInvalidRequestContent,
			Message: "Field 'location' cannot be updated",
			Target:  "location",
		},
	}

	structTagMap := GetStructTagMap[InternalTestResource]()
	assert.Equal(t, expectErrors, ValidateVisibility(dst, src, testResourceVisibilityMap, structTagMap, true))
}
