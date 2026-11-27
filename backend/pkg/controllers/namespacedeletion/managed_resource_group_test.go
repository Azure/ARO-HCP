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

package namespacedeletion

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

func TestManagedResourceGroupMustBeAbsent(t *testing.T) {
	ctx := context.Background()
	const subscriptionID = "11111111-1111-1111-1111-111111111111"
	resourceID, err := azcorearm.ParseResourceID("/subscriptions/" + subscriptionID)
	require.NoError(t, err)
	tenantID := "test-tenant"
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	_, err = db.Subscriptions().Create(ctx, &coreapi.Subscription{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: subscriptionID},
		Properties:     &coreapi.SubscriptionProperties{TenantId: &tenantID},
	}, nil)
	require.NoError(t, err)
	ctrl := gomock.NewController(t)
	builder := azureclient.NewMockFirstPartyApplicationClientBuilder(ctrl)
	rgClient := azureclient.NewMockResourceGroupsClient(ctrl)
	builder.EXPECT().ResourceGroupsClient(tenantID, subscriptionID).Return(rgClient, nil).Times(6)
	stable := "Succeeded"
	updating := "Updating"
	deleting := "Deleting"
	failed := "Failed"
	canceled := "Canceled"
	rgClient.EXPECT().Get(ctx, "managed-rg", nil).Return(armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{Properties: &armresources.ResourceGroupProperties{ProvisioningState: &stable}},
	}, nil)
	rgClient.EXPECT().Get(ctx, "managed-rg", nil).Return(armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{Properties: &armresources.ResourceGroupProperties{ProvisioningState: &updating}},
	}, nil)
	rgClient.EXPECT().Get(ctx, "managed-rg", nil).Return(armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{Properties: &armresources.ResourceGroupProperties{ProvisioningState: &deleting}},
	}, nil)
	rgClient.EXPECT().Get(ctx, "managed-rg", nil).Return(armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{Properties: &armresources.ResourceGroupProperties{ProvisioningState: &failed}},
	}, nil)
	rgClient.EXPECT().Get(ctx, "managed-rg", nil).Return(armresources.ResourceGroupsClientGetResponse{
		ResourceGroup: armresources.ResourceGroup{Properties: &armresources.ResourceGroupProperties{ProvisioningState: &canceled}},
	}, nil)
	rgClient.EXPECT().Get(ctx, "managed-rg", nil).Return(armresources.ResourceGroupsClientGetResponse{}, &azcore.ResponseError{StatusCode: 404})
	syncer := &namespaceDeletionSyncer{resourcesDBClient: db, azureFPAClientBuilder: builder}
	hc := hostedClusterObservation{azureSubscriptionID: subscriptionID, azureResourceGroup: "managed-rg"}

	state, err := syncer.readManagedResourceGroupState(ctx, hc)
	require.NoError(t, err)
	require.Equal(t, managedResourceGroupStable, state, "a stable resource group must block finalizer removal")
	state, err = syncer.readManagedResourceGroupState(ctx, hc)
	require.NoError(t, err)
	require.Equal(t, managedResourceGroupInProgress, state, "an updating resource group must not latch a stop")
	state, err = syncer.readManagedResourceGroupState(ctx, hc)
	require.NoError(t, err)
	require.Equal(t, managedResourceGroupDeleting, state, "a deleting resource group should cause a retryable wait")
	state, err = syncer.readManagedResourceGroupState(ctx, hc)
	require.NoError(t, err)
	require.Equal(t, managedResourceGroupDeletionFailed, state, "a failed deletion must not be reported as a stable resource group")
	state, err = syncer.readManagedResourceGroupState(ctx, hc)
	require.NoError(t, err)
	require.Equal(t, managedResourceGroupDeletionFailed, state, "a canceled deletion must not be reported as a stable resource group")
	state, err = syncer.readManagedResourceGroupState(ctx, hc)
	require.NoError(t, err)
	require.Equal(t, managedResourceGroupAbsent, state, "only an Azure 404 authorizes finalizer removal")
}
