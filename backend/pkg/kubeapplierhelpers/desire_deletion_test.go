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

package kubeapplierhelpers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
)

// TestPurgeApplyDesire covers the one-shot retirement path used by desires
// whose target must outlive the desire document (on-demand backups, or a
// desire that owns only a field on a foreign-owned object): PurgeApplyDesire
// must delete the ApplyDesire document directly, never converting it to
// Type=Delete first, which would instead make the kube-applier delete the
// applied target.
func TestPurgeApplyDesire(t *testing.T) {
	makeApplyDesire := func(name string) *kubeapplierapi.ApplyDesire {
		resourceIDStr := kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, name)
		resourceID := metadataapi.Must(azcorearm.ParseResourceID(resourceIDStr))
		return &kubeapplierapi.ApplyDesire{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(testMCResourceID.String())},
			Spec: kubeapplierapi.ApplyDesireSpec{
				ManagementCluster: testMCResourceID,
				Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
				TargetItem:        testTarget(),
				ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{"kind":"Backup"}`)},
				},
			},
		}
	}

	t.Run("removes the document instead of converting it to Delete", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
		desire := makeApplyDesire("ondemand-desire")
		_, err := crud.Create(context.Background(), desire, nil)
		require.NoError(t, err)

		err = PurgeApplyDesire(context.Background(), desire.ResourceID.Name, crud)
		require.NoError(t, err)

		_, err = crud.Get(context.Background(), "ondemand-desire")
		assert.True(t, cosmosstorageutils.IsNotFoundError(err),
			"PurgeApplyDesire should delete the ApplyDesire document, never leave a Type=Delete desire that would delete the target")
	})

	t.Run("is idempotent when the document is already gone", func(t *testing.T) {
		mockKubeApplier := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
		crud, _ := mockKubeApplier.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)

		// The document was never created; purge must tolerate NotFound and stay a no-op.
		err := PurgeApplyDesire(context.Background(), "ondemand-desire", crud)
		require.NoError(t, err, "purging an already-absent ApplyDesire should not error")
	})
}
