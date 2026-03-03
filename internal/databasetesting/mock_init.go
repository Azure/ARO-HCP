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

package databasetesting

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// NewMockDBClientWithResources creates a new MockDBClient and populates it with the given resources.
// Resources can be of the following types:
//   - *api.HCPOpenShiftCluster
//   - *api.HCPOpenShiftClusterNodePool
//   - *api.Operation
//   - *api.HCPOpenShiftClusterExternalAuth
//   - *api.ServiceProviderCluster
//   - *arm.Subscription
//
// Returns an error if any resource cannot be created or if an unsupported type is encountered.
func NewMockDBClientWithResources(ctx context.Context, resources []any) (*MockDBClient, error) {
	mockDB := NewMockDBClient()

	for i, resource := range resources {
		if err := mockDB.addResource(ctx, resource); err != nil {
			return nil, fmt.Errorf("failed to add resource at index %d: %w", i, err)
		}
	}

	return mockDB, nil
}

// addResource adds a single resource to the MockDBClient.
func (m *MockDBClient) addResource(ctx context.Context, resource any) error {
	switch r := resource.(type) {
	case *api.HCPOpenShiftCluster:
		return m.addCluster(ctx, r)
	case *api.HCPOpenShiftClusterNodePool:
		return m.addNodePool(ctx, r)
	case *api.Operation:
		return m.addOperation(ctx, r)
	case *api.HCPOpenShiftClusterExternalAuth:
		return m.addExternalAuth(ctx, r)
	case *api.ServiceProviderCluster:
		return m.addServiceProviderCluster(ctx, r)
	case *arm.Subscription:
		return m.addSubscription(ctx, r)
	default:
		return fmt.Errorf("unsupported resource type: %T", resource)
	}
}

func (m *MockDBClient) addCluster(ctx context.Context, cluster *api.HCPOpenShiftCluster) error {
	if cluster.ID == nil {
		return fmt.Errorf("cluster is missing resource ID")
	}
	clusterCRUD := m.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName)
	_, err := clusterCRUD.Create(ctx, cluster, nil)
	return err
}

func (m *MockDBClient) addNodePool(ctx context.Context, nodePool *api.HCPOpenShiftClusterNodePool) error {
	if nodePool.ID == nil {
		return fmt.Errorf("node pool is missing resource ID")
	}
	if nodePool.ID.Parent == nil {
		return fmt.Errorf("node pool is missing parent cluster ID")
	}
	clusterName := nodePool.ID.Parent.Name
	nodePoolCRUD := m.HCPClusters(nodePool.ID.SubscriptionID, nodePool.ID.ResourceGroupName).NodePools(clusterName)
	_, err := nodePoolCRUD.Create(ctx, nodePool, nil)
	return err
}

func (m *MockDBClient) addOperation(ctx context.Context, operation *api.Operation) error {
	if operation.OperationID == nil {
		return fmt.Errorf("operation is missing operation ID")
	}
	opCRUD := m.Operations(operation.OperationID.SubscriptionID)
	_, err := opCRUD.Create(ctx, operation, nil)
	return err
}

func (m *MockDBClient) addExternalAuth(ctx context.Context, externalAuth *api.HCPOpenShiftClusterExternalAuth) error {
	if externalAuth.ID == nil {
		return fmt.Errorf("external auth is missing resource ID")
	}
	if externalAuth.ID.Parent == nil {
		return fmt.Errorf("external auth is missing parent cluster ID")
	}
	clusterName := externalAuth.ID.Parent.Name
	externalAuthCRUD := m.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).ExternalAuth(clusterName)
	_, err := externalAuthCRUD.Create(ctx, externalAuth, nil)
	return err
}

func (m *MockDBClient) addServiceProviderCluster(ctx context.Context, spc *api.ServiceProviderCluster) error {
	resourceID := spc.GetResourceID()
	if resourceID == nil {
		return fmt.Errorf("service provider cluster is missing resource ID")
	}
	if resourceID.Parent == nil {
		return fmt.Errorf("service provider cluster is missing parent cluster ID")
	}
	clusterName := resourceID.Parent.Name
	spcCRUD := m.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, clusterName)
	_, err := spcCRUD.Create(ctx, spc, nil)
	return err
}

func (m *MockDBClient) addSubscription(ctx context.Context, subscription *arm.Subscription) error {
	resourceID := subscription.GetResourceID()
	if resourceID == nil {
		return fmt.Errorf("subscription is missing resource ID")
	}
	subCRUD := m.Subscriptions()
	_, err := subCRUD.Create(ctx, subscription, nil)
	return err
}
