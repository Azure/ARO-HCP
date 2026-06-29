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

package operations

import (
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	sharedops "github.com/Azure/ARO-HCP/backend/pkg/controllers/shared/operations"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// Common test constants
const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testExternalAuthName    = "test-external-auth"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"
	testExternalAuthIDStr   = "/api/clusters_mgmt/v1/clusters/abc123/external_auth_config/external_auths/ea123"
	testOperationName       = "test-operation-id"
	testTenantID            = "11111111-1111-1111-1111-111111111111"
	testAzureLocation       = "eastus"
)

// externalAuthTestFixture contains common test objects for external auth operations
type externalAuthTestFixture struct {
	clusterResourceID         *azcorearm.ResourceID
	externalAuthResourceID    *azcorearm.ResourceID
	operationID               *azcorearm.ResourceID
	cosmosOperationResourceID *azcorearm.ResourceID
	clusterInternalID         api.InternalID
	externalAuthInternalID    api.InternalID
}

func newExternalAuthTestFixture() *externalAuthTestFixture {
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	return &externalAuthTestFixture{
		clusterResourceID: clusterResourceID,
		externalAuthResourceID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/externalAuths/" + testExternalAuthName,
		)),
		operationID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/" + testAzureLocation +
				"/operationstatuses/" + testOperationName,
		)),
		cosmosOperationResourceID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + testOperationName,
		)),
		clusterInternalID:      api.Must(api.NewInternalID(testClusterServiceIDStr)),
		externalAuthInternalID: api.Must(api.NewInternalID(testExternalAuthIDStr)),
	}
}

func (f *externalAuthTestFixture) newCluster() *api.HCPOpenShiftCluster {
	return &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   f.clusterResourceID,
			PartitionKey: strings.ToLower(f.clusterResourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   f.clusterResourceID,
				Name: testClusterName,
				Type: f.clusterResourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &f.clusterInternalID,
		},
	}
}

func (f *externalAuthTestFixture) newExternalAuth() *api.HCPOpenShiftClusterExternalAuth {
	return &api.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: f.externalAuthResourceID, PartitionKey: strings.ToLower(f.externalAuthResourceID.SubscriptionID)},
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   f.externalAuthResourceID,
				Name: testExternalAuthName,
				Type: f.externalAuthResourceID.ResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			ProvisioningState: arm.ProvisioningStateAccepted,
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID:  &f.externalAuthInternalID,
			ActiveOperationID: testOperationName,
		},
	}
}

func (f *externalAuthTestFixture) newOperation(request database.OperationRequest) *api.Operation {
	return &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   f.cosmosOperationResourceID,
			PartitionKey: strings.ToLower(f.cosmosOperationResourceID.SubscriptionID),
		},
		TenantID:    testTenantID,
		Status:      arm.ProvisioningStateAccepted,
		Request:     request,
		ExternalID:  f.externalAuthResourceID,
		InternalID:  f.externalAuthInternalID,
		OperationID: f.operationID,
	}
}

func (f *externalAuthTestFixture) operationKey() controllerutils.OperationKey {
	return controllerutils.OperationKey{
		SubscriptionID:   testSubscriptionID,
		OperationName:    testOperationName,
		ParentResourceID: f.externalAuthResourceID.String(),
	}
}

// ExternalAuthStateValue and related constants are copied from
// shared/operations/utils.go so they are accessible to tests in this package.
type ExternalAuthStateValue = sharedops.ExternalAuthStateValue

const (
	ExternalAuthStateReady        = sharedops.ExternalAuthStateReady
	ExternalAuthStateUninstalling = sharedops.ExternalAuthStateUninstalling
	ExternalAuthStateError        = sharedops.ExternalAuthStateError
)
