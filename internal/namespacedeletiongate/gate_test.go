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

package namespacedeletiongate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

type staticClusterLister struct {
	items []*coreapi.ServiceProviderCluster
}

func (l staticClusterLister) List(_ context.Context, _ *cosmosstorageutils.DBClientListResourceDocsOptions) (cosmosstorageutils.DBClientIterator[coreapi.ServiceProviderCluster], error) {
	ids := make([]string, len(l.items))
	return corecosmosstoragetesting.NewMockIterator(ids, l.items), nil
}

func TestMatchingNamespaceWithMissingPlacementIsIndeterminate(t *testing.T) {
	target := Target{
		ManagementClusterResourceID: "/providers/Microsoft.RedHatOpenShift/stamps/stamp/managementClusters/default",
		HostedClusterNamespace:      "ocm-arohcpprod-clusterid",
	}
	matching := &coreapi.ServiceProviderCluster{}
	matching.Status.HostedClusterNamespace = target.HostedClusterNamespace

	result := evaluateClusterRecords(context.Background(), staticClusterLister{items: []*coreapi.ServiceProviderCluster{matching}}, target)
	require.Equal(t, VerdictIndeterminate, result.Verdict)
	require.Contains(t, result.Detail, "no management-cluster placement")

	placement, err := azcorearm.ParseResourceID(target.ManagementClusterResourceID)
	require.NoError(t, err)
	matching.Spec.ManagementClusterResourceID = placement
	result = evaluateClusterRecords(context.Background(), staticClusterLister{items: []*coreapi.ServiceProviderCluster{matching}}, target)
	require.Equal(t, VerdictOwned, result.Verdict)

	unrelated := &coreapi.ServiceProviderCluster{}
	unrelated.Status.HostedClusterNamespace = "another-namespace"
	result = evaluateClusterRecords(context.Background(), staticClusterLister{items: []*coreapi.ServiceProviderCluster{unrelated}}, target)
	require.Equal(t, VerdictUnowned, result.Verdict)
}
