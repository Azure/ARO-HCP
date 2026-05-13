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

package operationcontrollers

import (
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// mustParseTime parses a time string in RFC3339 format and panics on error.
// Use for test constants to make date values more readable.
func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// Common test constants
const (
	testSubscriptionID            = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName         = "test-rg"
	testClusterName               = "test-cluster"
	testNodePoolName              = "test-nodepool"
	testExternalAuthName          = "test-external-auth"
	testClusterServiceIDStr       = "/api/clusters_mgmt/v1/clusters/abc123"
	testNodePoolIDStr             = "/api/clusters_mgmt/v1/clusters/abc123/node_pools/np123"
	testExternalAuthIDStr         = "/api/clusters_mgmt/v1/clusters/abc123/external_auth_config/external_auths/ea123"
	testBreakGlassCredentialIDStr = "/api/clusters_mgmt/v1/clusters/abc123/break_glass_credentials/bgc123"
	testOperationName             = "test-operation-id"
	testTenantID                  = "11111111-1111-1111-1111-111111111111"
	testAzureLocation             = "eastus"
	testClusterUID                = "00000000-0000-0000-0000-000000000000"
)

// clusterTestFixture contains common test objects for cluster operations
type clusterTestFixture struct {
	clusterResourceID         *azcorearm.ResourceID
	operationID               *azcorearm.ResourceID
	cosmosOperationResourceID *azcorearm.ResourceID
	clusterInternalID         resourcesapi.InternalID
}

func newClusterTestFixture() *clusterTestFixture {
	return &clusterTestFixture{
		clusterResourceID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
		)),
		operationID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/" + testAzureLocation +
				"/operationstatuses/" + testOperationName,
		)),
		cosmosOperationResourceID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + testOperationName,
		)),
		clusterInternalID: resourcesapi.Must(resourcesapi.NewInternalID(testClusterServiceIDStr)),
	}
}

func (f *clusterTestFixture) newCluster(createdAt *time.Time) *resourcesapi.HCPOpenShiftCluster {
	return &resourcesapi.HCPOpenShiftCluster{
		TrackedResource: armresourcesapi.TrackedResource{
			Resource: armresourcesapi.Resource{
				ID:   f.clusterResourceID,
				Name: testClusterName,
				Type: f.clusterResourceID.ResourceType.String(),
				SystemData: &armresourcesapi.SystemData{
					CreatedAt: createdAt,
				},
			},
		},
		ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID:  &f.clusterInternalID,
			ActiveOperationID: testOperationName,
			ClusterUID:        testClusterUID,
		},
	}
}

func (f *clusterTestFixture) newOperation(request database.OperationRequest) *resourcesapi.Operation {
	return &resourcesapi.Operation{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: f.cosmosOperationResourceID,
		},
		TenantID:    testTenantID,
		Status:      armresourcesapi.ProvisioningStateAccepted,
		Request:     request,
		ExternalID:  f.clusterResourceID,
		InternalID:  f.clusterInternalID,
		OperationID: f.operationID,
	}
}

func (f *clusterTestFixture) operationKey() controllerutils.OperationKey {
	return controllerutils.OperationKey{
		SubscriptionID:   testSubscriptionID,
		OperationName:    testOperationName,
		ParentResourceID: f.clusterResourceID.String(),
	}
}

// nodePoolTestFixture contains common test objects for node pool operations
type nodePoolTestFixture struct {
	clusterResourceID         *azcorearm.ResourceID
	nodePoolResourceID        *azcorearm.ResourceID
	operationID               *azcorearm.ResourceID
	cosmosOperationResourceID *azcorearm.ResourceID
	clusterInternalID         resourcesapi.InternalID
	nodePoolInternalID        resourcesapi.InternalID
}

func newNodePoolTestFixture() *nodePoolTestFixture {
	clusterResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	return &nodePoolTestFixture{
		clusterResourceID: clusterResourceID,
		nodePoolResourceID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/nodePools/" + testNodePoolName,
		)),
		operationID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/" + testAzureLocation +
				"/operationstatuses/" + testOperationName,
		)),
		cosmosOperationResourceID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + testOperationName,
		)),
		clusterInternalID:  resourcesapi.Must(resourcesapi.NewInternalID(testClusterServiceIDStr)),
		nodePoolInternalID: resourcesapi.Must(resourcesapi.NewInternalID(testNodePoolIDStr)),
	}
}

func (f *nodePoolTestFixture) newCluster() *resourcesapi.HCPOpenShiftCluster {
	return &resourcesapi.HCPOpenShiftCluster{
		TrackedResource: armresourcesapi.TrackedResource{
			Resource: armresourcesapi.Resource{
				ID:   f.clusterResourceID,
				Name: testClusterName,
				Type: f.clusterResourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &f.clusterInternalID,
		},
	}
}

func (f *nodePoolTestFixture) newNodePool() *resourcesapi.HCPOpenShiftClusterNodePool {
	return &resourcesapi.HCPOpenShiftClusterNodePool{
		TrackedResource: armresourcesapi.TrackedResource{
			Resource: armresourcesapi.Resource{
				ID:   f.nodePoolResourceID,
				Name: testNodePoolName,
				Type: f.nodePoolResourceID.ResourceType.String(),
			},
		},
		Properties: resourcesapi.HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: armresourcesapi.ProvisioningStateAccepted,
		},
		ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID:  &f.nodePoolInternalID,
			ActiveOperationID: testOperationName,
		},
	}
}

func (f *nodePoolTestFixture) newOperation(request database.OperationRequest) *resourcesapi.Operation {
	return &resourcesapi.Operation{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: f.cosmosOperationResourceID,
		},
		TenantID:    testTenantID,
		Status:      armresourcesapi.ProvisioningStateAccepted,
		Request:     request,
		ExternalID:  f.nodePoolResourceID,
		InternalID:  f.nodePoolInternalID,
		OperationID: f.operationID,
	}
}

func (f *nodePoolTestFixture) operationKey() controllerutils.OperationKey {
	return controllerutils.OperationKey{
		SubscriptionID:   testSubscriptionID,
		OperationName:    testOperationName,
		ParentResourceID: f.nodePoolResourceID.String(),
	}
}

// externalAuthTestFixture contains common test objects for external auth operations
type externalAuthTestFixture struct {
	clusterResourceID         *azcorearm.ResourceID
	externalAuthResourceID    *azcorearm.ResourceID
	operationID               *azcorearm.ResourceID
	cosmosOperationResourceID *azcorearm.ResourceID
	clusterInternalID         resourcesapi.InternalID
	externalAuthInternalID    resourcesapi.InternalID
}

func newExternalAuthTestFixture() *externalAuthTestFixture {
	clusterResourceID := resourcesapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	return &externalAuthTestFixture{
		clusterResourceID: clusterResourceID,
		externalAuthResourceID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/externalAuths/" + testExternalAuthName,
		)),
		operationID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/locations/" + testAzureLocation +
				"/operationstatuses/" + testOperationName,
		)),
		cosmosOperationResourceID: resourcesapi.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/" + testOperationName,
		)),
		clusterInternalID:      resourcesapi.Must(resourcesapi.NewInternalID(testClusterServiceIDStr)),
		externalAuthInternalID: resourcesapi.Must(resourcesapi.NewInternalID(testExternalAuthIDStr)),
	}
}

func (f *externalAuthTestFixture) newCluster() *resourcesapi.HCPOpenShiftCluster {
	return &resourcesapi.HCPOpenShiftCluster{
		TrackedResource: armresourcesapi.TrackedResource{
			Resource: armresourcesapi.Resource{
				ID:   f.clusterResourceID,
				Name: testClusterName,
				Type: f.clusterResourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: &f.clusterInternalID,
		},
	}
}

func (f *externalAuthTestFixture) newExternalAuth() *resourcesapi.HCPOpenShiftClusterExternalAuth {
	return &resourcesapi.HCPOpenShiftClusterExternalAuth{
		ProxyResource: armresourcesapi.ProxyResource{
			Resource: armresourcesapi.Resource{
				ID:   f.externalAuthResourceID,
				Name: testExternalAuthName,
				Type: f.externalAuthResourceID.ResourceType.String(),
			},
		},
		Properties: resourcesapi.HCPOpenShiftClusterExternalAuthProperties{
			ProvisioningState: armresourcesapi.ProvisioningStateAccepted,
		},
		ServiceProviderProperties: resourcesapi.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID:  &f.externalAuthInternalID,
			ActiveOperationID: testOperationName,
		},
	}
}

func (f *externalAuthTestFixture) newOperation(request database.OperationRequest) *resourcesapi.Operation {
	return &resourcesapi.Operation{
		CosmosMetadata: resourcesapi.CosmosMetadata{
			ResourceID: f.cosmosOperationResourceID,
		},
		TenantID:    testTenantID,
		Status:      armresourcesapi.ProvisioningStateAccepted,
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
