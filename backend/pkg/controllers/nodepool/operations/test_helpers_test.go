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
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	sharedops "github.com/Azure/ARO-HCP/backend/pkg/controllers/shared/operations"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
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
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testNodePoolName        = "test-nodepool"
	testClusterServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123"
	testNodePoolIDStr       = "/api/clusters_mgmt/v1/clusters/abc123/node_pools/np123"
	testOperationName       = "test-operation-id"
	testTenantID            = "11111111-1111-1111-1111-111111111111"
	testAzureLocation       = "eastus"
)

// nodePoolTestFixture contains common test objects for node pool operations
type nodePoolTestFixture struct {
	clusterResourceID         *azcorearm.ResourceID
	nodePoolResourceID        *azcorearm.ResourceID
	operationID               *azcorearm.ResourceID
	cosmosOperationResourceID *azcorearm.ResourceID
	clusterInternalID         api.InternalID
	nodePoolInternalID        api.InternalID
}

func newNodePoolTestFixture() *nodePoolTestFixture {
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	return &nodePoolTestFixture{
		clusterResourceID: clusterResourceID,
		nodePoolResourceID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/nodePools/" + testNodePoolName,
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
		clusterInternalID:  api.Must(api.NewInternalID(testClusterServiceIDStr)),
		nodePoolInternalID: api.Must(api.NewInternalID(testNodePoolIDStr)),
	}
}

func (f *nodePoolTestFixture) newCluster() *api.HCPOpenShiftCluster {
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

func (f *nodePoolTestFixture) newNodePool() *api.HCPOpenShiftClusterNodePool {
	return &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{ResourceID: f.nodePoolResourceID, PartitionKey: strings.ToLower(f.nodePoolResourceID.SubscriptionID)},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   f.nodePoolResourceID,
				Name: testNodePoolName,
				Type: f.nodePoolResourceID.ResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: arm.ProvisioningStateAccepted,
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID:  &f.nodePoolInternalID,
			ActiveOperationID: testOperationName,
		},
	}
}

func (f *nodePoolTestFixture) newServiceProviderNodePool() *api.ServiceProviderNodePool {
	resourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
		f.nodePoolResourceID.String(),
		api.ServiceProviderNodePoolResourceTypeName,
		api.ServiceProviderNodePoolResourceName,
	)))
	return &api.ServiceProviderNodePool{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

func (f *nodePoolTestFixture) newNodePoolVersionController(conditions []metav1.Condition) *api.Controller {
	resourceID := api.Must(azcorearm.ParseResourceID(
		f.nodePoolResourceID.String() + "/hcpOpenShiftControllers/NodePoolVersion",
	))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ExternalID: f.nodePoolResourceID,
		Status: api.ControllerStatus{
			Conditions: conditions,
		},
	}
}

func (f *nodePoolTestFixture) newOperation(request database.OperationRequest) *api.Operation {
	return &api.Operation{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   f.cosmosOperationResourceID,
			PartitionKey: strings.ToLower(f.cosmosOperationResourceID.SubscriptionID),
		},
		TenantID:    testTenantID,
		Status:      arm.ProvisioningStateAccepted,
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

// NodePoolStateValue and related constants are copied from
// shared/operations/utils.go so they are accessible to tests in this package.
type NodePoolStateValue = sharedops.NodePoolStateValue

const (
	NodePoolStateValidating       = sharedops.NodePoolStateValidating
	NodePoolStatePending          = sharedops.NodePoolStatePending
	NodePoolStateInstalling       = sharedops.NodePoolStateInstalling
	NodePoolStateReady            = sharedops.NodePoolStateReady
	NodePoolStateUpdating         = sharedops.NodePoolStateUpdating
	NodePoolStateValidatingUpdate = sharedops.NodePoolStateValidatingUpdate
	NodePoolStatePendingUpdate    = sharedops.NodePoolStatePendingUpdate
	NodePoolStateUninstalling     = sharedops.NodePoolStateUninstalling
	NodePoolStateRecoverableError = sharedops.NodePoolStateRecoverableError
	NodePoolStateError            = sharedops.NodePoolStateError
)
