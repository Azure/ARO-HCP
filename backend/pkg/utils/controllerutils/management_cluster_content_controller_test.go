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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
)

func TestManagementClusterContentResourceIDFromClusterResourceID(t *testing.T) {
	clusterRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster"))

	got := ManagementClusterContentResourceIDFromParentResourceID(clusterRID, api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster)
	require.NotNil(t, got)
	assert.Equal(t, got.ResourceType.Type, api.ClusterScopedManagementClusterContentResourceType.Type)
	// Name is the last segment of the resource ID (the management cluster content name)
	assert.Equal(t, got.Name, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
}

func TestManagementClusterContentResourceIDFromNodePoolResourceID(t *testing.T) {
	nodePoolRID := api.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/mycluster/nodePools/mynodepool"))

	got := ManagementClusterContentResourceIDFromParentResourceID(nodePoolRID, api.MaestroBundleInternalNameReadonlyHypershiftNodePool)
	require.NotNil(t, got)
	assert.Equal(t, got.ResourceType.Type, api.NodePoolScopedManagementClusterContentResourceType.Type)
	// Name is the last segment of the resource ID (the management cluster content name)
	assert.Equal(t, got.Name, string(api.MaestroBundleInternalNameReadonlyHypershiftNodePool))
}
