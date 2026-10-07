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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestAzureClusterVnetIntegrationSubnetSizeValidation_Validate(t *testing.T) {
	const (
		testSubscriptionID = "00000000-0000-0000-0000-000000000000"
		testIdentityURL    = "https://identity.example.com"
	)

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	smiResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test-smi"))
	vnetIntegrationSubnetID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID + "/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-vnet-integration"))

	newCluster := func(subnetID *azcorearm.ResourceID) *coreapi.Cluster {
		return &coreapi.Cluster{
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{ID: clusterResourceID},
			},
			ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
				ManagedIdentitiesDataPlaneIdentityURL: testIdentityURL,
			},
			CustomerProperties: coreapi.ClusterCustomerProperties{
				Platform: coreapi.CustomerPlatformProfile{
					VnetIntegrationSubnetID: subnetID,
					OperatorsAuthentication: coreapi.OperatorsAuthenticationProfile{
						UserAssignedIdentities: coreapi.UserAssignedIdentitiesProfile{
							ServiceManagedIdentity: smiResourceID,
						},
					},
				},
			},
		}
	}

	subnetResponse := func(prefix *string, prefixes ...string) armnetwork.SubnetsClientGetResponse {
		props := &armnetwork.SubnetPropertiesFormat{AddressPrefix: prefix}
		for _, p := range prefixes {
			props.AddressPrefixes = append(props.AddressPrefixes, ptr.To(p))
		}
		return armnetwork.SubnetsClientGetResponse{
			Subnet: armnetwork.Subnet{
				ID:         ptr.To(vnetIntegrationSubnetID.String()),
				Properties: props,
			},
		}
	}

	expectGet := func(c *azureclient.MockSubnetsClient, resp armnetwork.SubnetsClientGetResponse, err error) {
		c.EXPECT().Get(gomock.Any(), vnetIntegrationSubnetID.ResourceGroupName, vnetIntegrationSubnetID.Parent.Name, vnetIntegrationSubnetID.Name, nil).Return(resp, err)
	}

	tests := []struct {
		name                string
		subnetID            *azcorearm.ResourceID
		setupMocks          func(builder *azureclient.MockServiceManagedIdentityClientBuilder, subnets *azureclient.MockSubnetsClient)
		wantOutcome         OutcomeType
		wantReason          string
		wantMessageContains string
	}{
		{
			name:        "no vnet integration subnet is skipped",
			subnetID:    nil,
			setupMocks:  func(_ *azureclient.MockServiceManagedIdentityClientBuilder, _ *azureclient.MockSubnetsClient) {},
			wantOutcome: OutcomeTypeSkipped,
			wantReason:  "NotApplicable",
		},
		{
			name:     "subnet exactly /28 passes",
			subnetID: vnetIntegrationSubnetID,
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, subnets *azureclient.MockSubnetsClient) {
				builder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(subnets, nil)
				expectGet(subnets, subnetResponse(ptr.To("10.0.1.0/28")), nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:     "subnet larger than /28 passes",
			subnetID: vnetIntegrationSubnetID,
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, subnets *azureclient.MockSubnetsClient) {
				builder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(subnets, nil)
				expectGet(subnets, subnetResponse(ptr.To("10.0.0.0/24")), nil)
			},
			wantOutcome: OutcomeTypePassed,
		},
		{
			name:     "subnet smaller than /28 fails",
			subnetID: vnetIntegrationSubnetID,
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, subnets *azureclient.MockSubnetsClient) {
				builder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(subnets, nil)
				expectGet(subnets, subnetResponse(ptr.To("10.0.1.0/29")), nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantReason:          "AzureVnetIntegrationSubnetTooSmall",
			wantMessageContains: "is /29 (8 IP addresses), but must have a minimum size of /28 (16 IP addresses)",
		},
		{
			name:     "subnet smaller than /28 via plural AddressPrefixes fails",
			subnetID: vnetIntegrationSubnetID,
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, subnets *azureclient.MockSubnetsClient) {
				builder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(subnets, nil)
				expectGet(subnets, subnetResponse(nil, "10.0.1.0/30"), nil)
			},
			wantOutcome:         OutcomeTypeFailed,
			wantReason:          "AzureVnetIntegrationSubnetTooSmall",
			wantMessageContains: "is /30 (4 IP addresses)",
		},
		{
			name:     "subnet get error is unknown",
			subnetID: vnetIntegrationSubnetID,
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, subnets *azureclient.MockSubnetsClient) {
				builder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(subnets, nil)
				expectGet(subnets, armnetwork.SubnetsClientGetResponse{}, errors.New("boom"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantReason:          "InternalError",
			wantMessageContains: "failed to get vnet integration subnet",
		},
		{
			name:     "subnets client build error is unknown",
			subnetID: vnetIntegrationSubnetID,
			setupMocks: func(builder *azureclient.MockServiceManagedIdentityClientBuilder, _ *azureclient.MockSubnetsClient) {
				builder.EXPECT().SubnetsClient(gomock.Any(), testIdentityURL, smiResourceID, testSubscriptionID).Return(nil, errors.New("no creds"))
			},
			wantOutcome:         OutcomeTypeUnknown,
			wantReason:          "InternalError",
			wantMessageContains: "failed to get subnets client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			builder := azureclient.NewMockServiceManagedIdentityClientBuilder(ctrl)
			subnets := azureclient.NewMockSubnetsClient(ctrl)
			tt.setupMocks(builder, subnets)

			v := NewAzureClusterVnetIntegrationSubnetSizeValidation(builder)
			result := v.Validate(context.Background(), &coreapi.Subscription{}, newCluster(tt.subnetID))

			require.NoError(t, result.Validate())
			assert.Equal(t, tt.wantOutcome, result.Outcome.Type)
			if tt.wantReason != "" {
				assert.Equal(t, tt.wantReason, result.Reason())
			}
			if tt.wantMessageContains != "" {
				assert.Contains(t, result.InternalMessage(), tt.wantMessageContains)
			}
		})
	}
}
