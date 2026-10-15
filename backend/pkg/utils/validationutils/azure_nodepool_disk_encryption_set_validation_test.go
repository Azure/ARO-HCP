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

package validationutils

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

const testDiskEncryptionSetName = "test-des"

func testDiskEncryptionSetResourceID(t *testing.T) *azcorearm.ResourceID {
	t.Helper()
	return metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + coreapitesting.TestSubscriptionID +
			"/resourceGroups/" + coreapitesting.TestResourceGroupName +
			"/providers/Microsoft.Compute/diskEncryptionSets/" + testDiskEncryptionSetName,
	))
}

func newDiskEncryptionSetTestNodePool(t *testing.T, encryptionSetID *azcorearm.ResourceID) *coreapi.NodePool {
	t.Helper()
	nodePool := testNodePool()
	nodePool.Properties.Platform.OSDisk.EncryptionSetID = encryptionSetID
	return nodePool
}

func makeTestDiskEncryptionSet(provisioningState, location string) armcompute.DiskEncryptionSetsClientGetResponse {
	return armcompute.DiskEncryptionSetsClientGetResponse{
		DiskEncryptionSet: armcompute.DiskEncryptionSet{
			Location: ptr.To(location),
			Properties: &armcompute.EncryptionSetProperties{
				ProvisioningState: ptr.To(provisioningState),
			},
		},
	}
}

func TestAzureNodePoolDiskEncryptionSetValidation_Validate(t *testing.T) {
	ctx := context.Background()
	cluster := testCluster()
	subscription := testNSGSubscription()

	tests := []struct {
		name                      string
		nodePool                  *coreapi.NodePool
		setupMockSMIClientBuilder func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder)
		wantOutcome               OutcomeType
		wantInternalMessage       string
	}{
		{
			name:        "no disk encryption set configured is skipped",
			nodePool:    newDiskEncryptionSetTestNodePool(t, nil),
			wantOutcome: OutcomeTypeSkipped,
		},
		{
			name:     "succeeds when provisioned and in the same region",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				desClient := azureclient.NewMockDiskEncryptionSetsClient(ctrl)
				desClient.EXPECT().
					Get(gomock.Any(), coreapitesting.TestResourceGroupName, testDiskEncryptionSetName, nil).
					Return(makeTestDiskEncryptionSet(diskEncryptionSetProvisioningStateSucceeded, coreapitesting.TestLocation), nil)
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(desClient, nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:     "fails when provisioning state is not Succeeded",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				desClient := azureclient.NewMockDiskEncryptionSetsClient(ctrl)
				desClient.EXPECT().
					Get(gomock.Any(), coreapitesting.TestResourceGroupName, testDiskEncryptionSetName, nil).
					Return(makeTestDiskEncryptionSet("Updating", coreapitesting.TestLocation), nil)
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(desClient, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `is not in a "Succeeded" provisioning state`,
		},
		{
			name:     "fails when disk encryption set is in a different region",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				desClient := azureclient.NewMockDiskEncryptionSetsClient(ctrl)
				desClient.EXPECT().
					Get(gomock.Any(), coreapitesting.TestResourceGroupName, testDiskEncryptionSetName, nil).
					Return(makeTestDiskEncryptionSet(diskEncryptionSetProvisioningStateSucceeded, "eastus"), nil)
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(desClient, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `is in location "eastus" but the node pool is in location "westus3"`,
		},
		{
			name:     "fails with combined message when provisioning state and region are both invalid",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				desClient := azureclient.NewMockDiskEncryptionSetsClient(ctrl)
				desClient.EXPECT().
					Get(gomock.Any(), coreapitesting.TestResourceGroupName, testDiskEncryptionSetName, nil).
					Return(makeTestDiskEncryptionSet("Failed", "eastus"), nil)
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(desClient, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: `is not in a "Succeeded" provisioning state; disk encryption set "`,
		},
		{
			name:     "unknown when properties are missing",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				desClient := azureclient.NewMockDiskEncryptionSetsClient(ctrl)
				desClient.EXPECT().
					Get(gomock.Any(), coreapitesting.TestResourceGroupName, testDiskEncryptionSetName, nil).
					Return(armcompute.DiskEncryptionSetsClientGetResponse{
						DiskEncryptionSet: armcompute.DiskEncryptionSet{Location: ptr.To(coreapitesting.TestLocation)},
					}, nil)
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(desClient, nil)
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: "has no properties",
		},
		{
			name:     "unknown when location is missing",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				desClient := azureclient.NewMockDiskEncryptionSetsClient(ctrl)
				desClient.EXPECT().
					Get(gomock.Any(), coreapitesting.TestResourceGroupName, testDiskEncryptionSetName, nil).
					Return(armcompute.DiskEncryptionSetsClientGetResponse{
						DiskEncryptionSet: armcompute.DiskEncryptionSet{
							Properties: &armcompute.EncryptionSetProperties{ProvisioningState: ptr.To(diskEncryptionSetProvisioningStateSucceeded)},
						},
					}, nil)
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(desClient, nil)
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: "has no location",
		},
		{
			name:     "fails when disk encryption set is not found",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				desClient := azureclient.NewMockDiskEncryptionSetsClient(ctrl)
				desClient.EXPECT().
					Get(gomock.Any(), coreapitesting.TestResourceGroupName, testDiskEncryptionSetName, nil).
					Return(armcompute.DiskEncryptionSetsClientGetResponse{}, &azcore.ResponseError{StatusCode: http.StatusNotFound})
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(desClient, nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantInternalMessage: "was not found",
		},
		{
			name:     "unknown when Get fails",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				desClient := azureclient.NewMockDiskEncryptionSetsClient(ctrl)
				desClient.EXPECT().
					Get(gomock.Any(), coreapitesting.TestResourceGroupName, testDiskEncryptionSetName, nil).
					Return(armcompute.DiskEncryptionSetsClientGetResponse{}, errors.New("disk encryption set not found"))
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(desClient, nil)
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: "failed to get disk encryption set",
		},
		{
			name:     "unknown when client builder fails",
			nodePool: newDiskEncryptionSetTestNodePool(t, testDiskEncryptionSetResourceID(t)),
			setupMockSMIClientBuilder: func(ctrl *gomock.Controller, smiBuilder *azureclient.MockServiceManagedIdentityClientBuilder) {
				smiBuilder.EXPECT().
					DiskEncryptionSetsClient(gomock.Any(), gomock.Any(), gomock.Any(), coreapitesting.TestSubscriptionID).
					Return(nil, errors.New("failed to build client"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantInternalMessage: "failed to get disk encryption sets client as service managed identity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			smiBuilder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			if tt.setupMockSMIClientBuilder != nil {
				tt.setupMockSMIClientBuilder(ctrl, smiBuilder)
			}

			validation := NewAzureNodePoolDiskEncryptionSetValidation(smiBuilder)
			result := validation.Validate(ctx, cluster, subscription, tt.nodePool)
			require.NoError(t, result.Validate())
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			if tt.wantInternalMessage != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantInternalMessage)
			}
		})
	}
}
