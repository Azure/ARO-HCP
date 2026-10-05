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

package v20270330preview

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20270330preview/generated"
)

func TestNewActiveVersions(t *testing.T) {
	tests := []struct {
		name     string
		input    []coreapi.HCPClusterActiveVersion
		expected []*generated.ClusterActiveVersion
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty input returns nil",
			input:    []coreapi.HCPClusterActiveVersion{},
			expected: nil,
		},
		{
			name: "single version is passed through",
			input: []coreapi.HCPClusterActiveVersion{
				{Version: "4.19"},
			},
			expected: []*generated.ClusterActiveVersion{
				{Version: ptr.To("4.19")},
			},
		},
		{
			name: "multiple versions are passed through",
			input: []coreapi.HCPClusterActiveVersion{
				{Version: "4.20"},
				{Version: "4.19"},
			},
			expected: []*generated.ClusterActiveVersion{
				{Version: ptr.To("4.20")},
				{Version: ptr.To("4.19")},
			},
		},
		{
			name: "empty version entry is skipped",
			input: []coreapi.HCPClusterActiveVersion{
				{Version: "4.19"},
				{Version: ""},
				{Version: "4.20"},
			},
			expected: []*generated.ClusterActiveVersion{
				{Version: ptr.To("4.19")},
				{Version: ptr.To("4.20")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := newActiveVersions(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewClusterResourceStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    *coreapi.ClusterStatus
		expected *generated.ClusterResourceStatus
	}{
		{
			name:     "nil input returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty status returns nil",
			input:    &coreapi.ClusterStatus{},
			expected: nil,
		},
		{
			name: "status with active versions only",
			input: &coreapi.ClusterStatus{
				ActiveVersions: []coreapi.HCPClusterActiveVersion{
					{Version: "4.20"},
				},
			},
			expected: &generated.ClusterResourceStatus{
				Conditions: nil,
				ActiveVersions: []*generated.ClusterActiveVersion{
					{Version: ptr.To("4.20")},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := newClusterResourceStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeContainerRegistry(t *testing.T) {
	validMIResourceID := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/mi"
	fldPath := field.NewPath("properties", "platform", "containerRegistry")

	tests := []struct {
		name      string
		input     *generated.ContainerRegistryProfile
		wantNil   bool
		wantError string
	}{
		{
			name:    "nil profile clears output",
			input:   nil,
			wantNil: true,
		},
		{
			name:    "nil managedIdentity clears output",
			input:   &generated.ContainerRegistryProfile{ManagedIdentity: nil},
			wantNil: true,
		},
		{
			name:      "empty string rejected",
			input:     &generated.ContainerRegistryProfile{ManagedIdentity: ptr.To("")},
			wantError: "must be a non-empty resource ID or null to clear",
		},
		{
			name:      "whitespace-only string rejected",
			input:     &generated.ContainerRegistryProfile{ManagedIdentity: ptr.To("   ")},
			wantError: "must be a non-empty resource ID or null to clear",
		},
		{
			name:    "valid resource ID accepted",
			input:   &generated.ContainerRegistryProfile{ManagedIdentity: ptr.To(validMIResourceID)},
			wantNil: false,
		},
		{
			name:      "invalid resource ID returns parse error",
			input:     &generated.ContainerRegistryProfile{ManagedIdentity: ptr.To("not-a-resource-id")},
			wantError: "not-a-resource-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out *azcorearm.ResourceID
			errs := normalizeContainerRegistry(fldPath, tt.input, &out)

			if tt.wantError != "" {
				if len(errs) == 0 {
					t.Fatalf("expected error containing %q, got none", tt.wantError)
				}
				for _, e := range errs {
					if strings.Contains(e.Error(), tt.wantError) {
						return
					}
				}
				t.Fatalf("expected error containing %q, got: %v", tt.wantError, errs)
			}

			if tt.input != nil && tt.input.ManagedIdentity != nil && !tt.wantNil && tt.wantError == "" {
				// valid resource ID case — parse error test skips out check
				if len(errs) == 0 && out == nil {
					t.Error("expected out to be set for valid resource ID, got nil")
				}
				return
			}

			if tt.wantNil && out != nil {
				t.Errorf("expected out to be nil, got %v", out)
			}
			if len(errs) != 0 {
				t.Errorf("expected no errors, got: %v", errs)
			}
		})
	}
}

func TestNewAutoNodeProfile(t *testing.T) {
	tests := []struct {
		name     string
		autoNode coreapi.AutoNodeMode
		want     *generated.AutoNodeProfile
	}{
		{
			name:     "enabled",
			autoNode: coreapi.AutoNode,
			want:     &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeEnabled)},
		},
		{
			name:     "default (disabled-by-absence) omits the field entirely",
			autoNode: coreapi.DefaultAutoNodeMode,
			want:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, newAutoNodeProfile(tt.autoNode))
		})
	}
}

func TestNormalizeAutoNode(t *testing.T) {
	enabledCluster := &coreapi.Cluster{}
	enabledCluster.ServiceProviderProperties.ExperimentalFeatures.AutoNode = coreapi.AutoNode

	tests := []struct {
		name      string
		profile   *generated.AutoNodeProfile
		existing  *coreapi.Cluster // nil means CREATE
		inTags    map[string]string
		wantTags  map[string]string
		wantError string
	}{
		{
			name:     "nil mode on CREATE touches nothing",
			profile:  &generated.AutoNodeProfile{},
			wantTags: nil,
		},
		{
			name:     "Enabled on CREATE sets the tag",
			profile:  &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeEnabled)},
			wantTags: map[string]string{metadataapi.TagClusterAutoNode: string(coreapi.AutoNode)},
		},
		{
			name:     "Disabled on CREATE matches the default and touches nothing",
			profile:  &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeDisabled)},
			wantTags: nil,
		},
		{
			name:      "invalid value",
			profile:   &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeMode("bogus"))},
			wantError: "must be",
		},
		{
			name:     "Enabled on an already-enabled cluster with an explicit tags map re-stamps the tag",
			profile:  &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeEnabled)},
			existing: enabledCluster,
			inTags:   map[string]string{"foo": "bar"},
			wantTags: map[string]string{"foo": "bar", metadataapi.TagClusterAutoNode: string(coreapi.AutoNode)},
		},
		{
			name:     "Enabled on an already-enabled cluster with no tags specified leaves tags nil",
			profile:  &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeEnabled)},
			existing: enabledCluster,
			wantTags: nil,
		},
		{
			name:      "Disabled attempting to change an enabled cluster is rejected and leaves tags untouched",
			profile:   &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeDisabled)},
			existing:  enabledCluster,
			inTags:    map[string]string{"foo": "bar"},
			wantError: "cannot be changed",
		},
		{
			name:     "Disabled matching an already-disabled cluster touches nothing",
			profile:  &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeDisabled)},
			existing: &coreapi.Cluster{},
			inTags:   map[string]string{"foo": "bar"},
			wantTags: map[string]string{"foo": "bar"},
		},
		{
			name:     "Enabled attempting to change a disabled cluster writes the tag so admission rejects it",
			profile:  &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeEnabled)},
			existing: &coreapi.Cluster{},
			wantTags: map[string]string{metadataapi.TagClusterAutoNode: string(coreapi.AutoNode)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &coreapi.Cluster{}
			out.Tags = tt.inTags
			errs := normalizeAutoNode(tt.profile, tt.existing, out)

			if tt.wantError != "" {
				if len(errs) == 0 {
					t.Fatalf("expected error containing %q, got none", tt.wantError)
				}
				for _, e := range errs {
					if strings.Contains(e.Error(), tt.wantError) {
						assert.Equal(t, tt.inTags, out.Tags, "tags must be untouched on rejection")
						return
					}
				}
				t.Fatalf("expected error containing %q, got: %v", tt.wantError, errs)
			}

			if len(errs) != 0 {
				t.Fatalf("expected no errors, got: %v", errs)
			}
			assert.Equal(t, tt.wantTags, out.Tags)
		})
	}
}
