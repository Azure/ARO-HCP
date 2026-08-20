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

package controllerutils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

const testStampIdentifier = "eastus"

func testManagementClusterResourceID() *azcorearm.ResourceID {
	rid, err := azcorearm.ParseResourceID(
		"/providers/" + coreapi.ProviderNamespace + "/stamps/" + testStampIdentifier + "/managementclusters/" + testStampIdentifier,
	)
	if err != nil {
		panic(err)
	}
	return rid
}

func TestBuildReadDesire(t *testing.T) {
	managementCluster := testManagementClusterResourceID()
	target := kubeapplierapi.ResourceReference{
		Group:    "mgmtagent.aro-hcp.azure.com",
		Version:  "v1alpha1",
		Resource: "capacityreports",
		Name:     "cluster",
	}
	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, "capacity")

	desire := BuildReadDesire(desireIDString, managementCluster, target)

	require.NotNil(t, desire.GetResourceID(), "ResourceID must be set")
	assert.True(t, strings.HasSuffix(
		strings.ToLower(desire.GetResourceID().String()),
		"/readdesires/capacity",
	), "ResourceID must end with readdesires/capacity")
	assert.Equal(t, strings.ToLower(managementCluster.String()), desire.PartitionKey, "PartitionKey must be lowercased management cluster ID")
	assert.Equal(t, target, desire.Spec.TargetItem, "TargetItem must match input")
	assert.Equal(t, managementCluster.String(), desire.Spec.ManagementCluster.String(), "ManagementCluster must match input")
}

func TestReadDesireNeedsWork(t *testing.T) {
	managementCluster := testManagementClusterResourceID()
	otherMC := metadataapi.Must(azcorearm.ParseResourceID(
		"/providers/" + coreapi.ProviderNamespace + "/stamps/westus/managementclusters/westus",
	))

	target := kubeapplierapi.ResourceReference{
		Group:    "mgmtagent.aro-hcp.azure.com",
		Version:  "v1alpha1",
		Resource: "capacityreports",
		Name:     "cluster",
	}
	differentTarget := kubeapplierapi.ResourceReference{
		Group:    "old.group",
		Version:  "v1",
		Resource: "oldresources",
		Name:     "old",
	}

	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(testStampIdentifier, "capacity")
	desired := BuildReadDesire(desireIDString, managementCluster, target)

	tests := []struct {
		name     string
		existing *kubeapplierapi.ReadDesire
		want     bool
	}{
		{
			name:     "nil existing needs work",
			existing: nil,
			want:     true,
		},
		{
			name:     "matching existing does not need work",
			existing: BuildReadDesire(desireIDString, managementCluster, target),
			want:     false,
		},
		{
			name:     "different target needs work",
			existing: BuildReadDesire(desireIDString, managementCluster, differentTarget),
			want:     true,
		},
		{
			name:     "different management cluster needs work",
			existing: BuildReadDesire(desireIDString, otherMC, target),
			want:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ReadDesireNeedsWork(test.existing, desired)
			assert.Equal(t, test.want, got)
		})
	}
}
