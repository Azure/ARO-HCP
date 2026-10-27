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

package serverutils

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
)

type unavailableDumpClients struct{ err error }

func (clients unavailableDumpClients) For(context.Context, *azcorearm.ResourceID) (kubeappliercosmosstorage.KubeApplierDBClient, error) {
	return nil, clients.err
}

func TestDumpKubeApplierInventoryUnavailable(t *testing.T) {
	mcID := metadataapi.Must(azcorearm.ParseResourceID("/providers/Microsoft.RedHatOpenShift/stamps/1/managementClusters/default"))
	resourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"))
	lister := &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: mcID}}}}
	injected := errors.New("management cluster lookup failed")
	for _, failure := range []error{nil, injected} {
		require.NotPanics(t, func() {
			err := dumpKubeApplierData(t.Context(), unavailableDumpClients{err: failure}, lister, resourceID)
			if failure != nil {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorContains(t, err, "inventory unavailable")
			}
		})
	}
}
