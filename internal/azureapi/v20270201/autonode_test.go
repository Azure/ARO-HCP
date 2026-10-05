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

package v20270201

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20270201/generated"
)

func TestNewAutoNodeProfile(t *testing.T) {
	require.Nil(t, newAutoNodeProfile(coreapi.DefaultAutoNodeMode))
	require.Equal(t, &generated.AutoNodeProfile{Mode: ptr.To(generated.AutoNodeModeEnabled)}, newAutoNodeProfile(coreapi.AutoNode))
	cluster := &HcpOpenShiftCluster{}
	SetDefaultValuesCluster(cluster)
	require.Nil(t, cluster.Properties.AutoNode, "defaults must not add an AutoNode profile")
}

func TestNormalizeAutoNode(t *testing.T) {
	enabledCluster := &coreapi.Cluster{}
	enabledCluster.ServiceProviderProperties.ExperimentalFeatures.AutoNode = coreapi.AutoNode
	tests := []struct {
		name      string
		mode      *generated.AutoNodeMode
		existing  *coreapi.Cluster
		tags      map[string]string
		wantTags  map[string]string
		wantError string
	}{
		{name: "omitted mode"},
		{name: "create enabled", mode: ptr.To(generated.AutoNodeModeEnabled), wantTags: map[string]string{metadataapi.TagClusterAutoNode: string(coreapi.AutoNode)}},
		{name: "create disabled", mode: ptr.To(generated.AutoNodeModeDisabled)},
		{name: "invalid mode", mode: ptr.To(generated.AutoNodeMode("invalid")), wantError: "must be"},
		{name: "replace tags on enabled cluster", mode: ptr.To(generated.AutoNodeModeEnabled), existing: enabledCluster, tags: map[string]string{"foo": "bar"}, wantTags: map[string]string{"foo": "bar", metadataapi.TagClusterAutoNode: string(coreapi.AutoNode)}},
		{name: "omit tags on enabled cluster", mode: ptr.To(generated.AutoNodeModeEnabled), existing: enabledCluster},
		{name: "reject disabling enabled cluster", mode: ptr.To(generated.AutoNodeModeDisabled), existing: enabledCluster, tags: map[string]string{"foo": "bar"}, wantError: "cannot be changed"},
		{name: "disabled remains disabled", mode: ptr.To(generated.AutoNodeModeDisabled), existing: &coreapi.Cluster{}, tags: map[string]string{"foo": "bar"}, wantTags: map[string]string{"foo": "bar"}},
		{name: "enable on update reaches admission", mode: ptr.To(generated.AutoNodeModeEnabled), existing: &coreapi.Cluster{}, wantTags: map[string]string{metadataapi.TagClusterAutoNode: string(coreapi.AutoNode)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &coreapi.Cluster{}
			out.Tags = tt.tags
			errs := normalizeAutoNode(&generated.AutoNodeProfile{Mode: tt.mode}, tt.existing, out)
			if tt.wantError != "" {
				require.NotEmpty(t, errs)
				require.Contains(t, errs.ToAggregate().Error(), tt.wantError)
				require.Equal(t, tt.tags, out.Tags, "rejected requests must not change tags")
				return
			}
			require.Empty(t, errs)
			require.Equal(t, tt.wantTags, out.Tags)
		})
	}
}

func TestAutoNodeConversion(t *testing.T) {
	for _, payload := range []string{
		`{"properties":{}}`,
		`{"properties":{"autoNode":null}}`,
		`{"properties":{"autoNode":{}}}`,
		`{"properties":{"autoNode":{"mode":null}}}`,
		`{"properties":{"autoNode":{"mode":"Disabled"}}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			external := versionedInterface.NewCluster(nil).(*HcpOpenShiftCluster)
			require.NoError(t, json.Unmarshal([]byte(payload), external))
			internal, err := external.ConvertToInternal(nil)
			require.NoError(t, err)
			require.NotContains(t, internal.Tags, metadataapi.TagClusterAutoNode)
			response := versionedInterface.NewCluster(internal).(*HcpOpenShiftCluster)
			require.Nil(t, response.Properties.AutoNode)
		})
	}
	internal := &coreapi.Cluster{}
	internal.ServiceProviderProperties.ExperimentalFeatures.AutoNode = coreapi.AutoNode
	internal.Tags = map[string]string{metadataapi.TagClusterAutoNode: string(coreapi.AutoNode)}
	external := versionedInterface.NewCluster(internal).(*HcpOpenShiftCluster)
	require.Equal(t, generated.AutoNodeModeEnabled, *external.Properties.AutoNode.Mode)
	converted, err := external.ConvertToInternal(internal)
	require.NoError(t, err)
	require.Equal(t, string(coreapi.AutoNode), converted.Tags[metadataapi.TagClusterAutoNode])
}
