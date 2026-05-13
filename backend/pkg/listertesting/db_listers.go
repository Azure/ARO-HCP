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

package listertesting

import (
	"context"
	"strings"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
	armresourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// DBClusterLister implements listers.ClusterLister backed by a database.ResourcesDBClient.
type DBClusterLister struct {
	ResourcesDBClient database.ResourcesDBClient
}

var _ listers.ClusterLister = &DBClusterLister{}

func (l *DBClusterLister) List(ctx context.Context) ([]*resourcesapi.HCPOpenShiftCluster, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().Clusters().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*resourcesapi.HCPOpenShiftCluster, error) {
	return l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, clusterName)
}

func (l *DBClusterLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*resourcesapi.HCPOpenShiftCluster, error) {
	iter, err := l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

// DBNodePoolLister implements listers.NodePoolLister backed by a database.ResourcesDBClient.
type DBNodePoolLister struct {
	ResourcesDBClient database.ResourcesDBClient
}

var _ listers.NodePoolLister = &DBNodePoolLister{}

func (l *DBNodePoolLister) List(ctx context.Context) ([]*resourcesapi.HCPOpenShiftClusterNodePool, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().NodePools().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*resourcesapi.HCPOpenShiftClusterNodePool, error) {
	return l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).Get(ctx, nodePoolName)
}

func (l *DBNodePoolLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*resourcesapi.HCPOpenShiftClusterNodePool, error) {
	// List all node pools and filter by resource group
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*resourcesapi.HCPOpenShiftClusterNodePool
	for _, np := range all {
		if np.ID != nil &&
			strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, np)
		}
	}
	return result, nil
}

func (l *DBNodePoolLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*resourcesapi.HCPOpenShiftClusterNodePool, error) {
	iter, err := l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

// DBActiveOperationLister implements listers.ActiveOperationLister backed by a database.ResourcesDBClient.
type DBActiveOperationLister struct {
	ResourcesDBClient database.ResourcesDBClient
}

var _ listers.ActiveOperationLister = &DBActiveOperationLister{}

func (l *DBActiveOperationLister) List(ctx context.Context) ([]*resourcesapi.Operation, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ActiveOperations().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBActiveOperationLister) Get(ctx context.Context, subscriptionID, name string) (*resourcesapi.Operation, error) {
	return l.ResourcesDBClient.Operations(subscriptionID).Get(ctx, name)
}

func (l *DBActiveOperationLister) ListActiveOperationsForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*resourcesapi.Operation, error) {
	clusterKey := resourcesapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*resourcesapi.Operation
	for _, op := range all {
		if op.ExternalID != nil && strings.HasPrefix(strings.ToLower(op.ExternalID.String()), strings.ToLower(clusterKey)) {
			result = append(result, op)
		}
	}
	return result, nil
}

// DBExternalAuthLister implements listers.ExternalAuthLister backed by a database.ResourcesDBClient.
type DBExternalAuthLister struct {
	ResourcesDBClient database.ResourcesDBClient
}

var _ listers.ExternalAuthLister = &DBExternalAuthLister{}

func (l *DBExternalAuthLister) List(ctx context.Context) ([]*resourcesapi.HCPOpenShiftClusterExternalAuth, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ExternalAuths().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*resourcesapi.HCPOpenShiftClusterExternalAuth, error) {
	return l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName).Get(ctx, externalAuthName)
}

func (l *DBExternalAuthLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*resourcesapi.HCPOpenShiftClusterExternalAuth, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*resourcesapi.HCPOpenShiftClusterExternalAuth
	for _, ea := range all {
		if ea.ID != nil &&
			strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, ea)
		}
	}
	return result, nil
}

func (l *DBExternalAuthLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*resourcesapi.HCPOpenShiftClusterExternalAuth, error) {
	iter, err := l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

// DBServiceProviderClusterLister implements listers.ServiceProviderClusterLister backed by a database.ResourcesDBClient.
type DBServiceProviderClusterLister struct {
	ResourcesDBClient database.ResourcesDBClient
}

var _ listers.ServiceProviderClusterLister = &DBServiceProviderClusterLister{}

func (l *DBServiceProviderClusterLister) List(ctx context.Context) ([]*resourcesapi.ServiceProviderCluster, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ServiceProviderClusters().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBServiceProviderClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*resourcesapi.ServiceProviderCluster, error) {
	return l.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(ctx, "default")
}

func (l *DBServiceProviderClusterLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*resourcesapi.ServiceProviderCluster, error) {
	iter, err := l.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

// DBControllerLister implements listers.ControllerLister backed by a database.ResourcesDBClient.
type DBControllerLister struct {
	ResourcesDBClient database.ResourcesDBClient
}

var _ listers.ControllerLister = &DBControllerLister{}

func (l *DBControllerLister) List(ctx context.Context) ([]*resourcesapi.Controller, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().Controllers().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBControllerLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*resourcesapi.Controller, error) {
	prefix := resourcesapi.ToResourceGroupResourceIDString(subscriptionID, resourceGroupName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*resourcesapi.Controller, error) {
	prefix := resourcesapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*resourcesapi.Controller, error) {
	prefix := resourcesapi.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForExternalAuth(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) ([]*resourcesapi.Controller, error) {
	prefix := resourcesapi.ToExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) listWithPrefix(ctx context.Context, prefix string) ([]*resourcesapi.Controller, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*resourcesapi.Controller
	for _, c := range all {
		if c.ResourceID != nil && strings.HasPrefix(strings.ToLower(c.ResourceID.String()), strings.ToLower(prefix)) {
			result = append(result, c)
		}
	}
	return result, nil
}

// DBManagementClusterContentLister implements listers.ManagementClusterContentLister backed by a database.ResourcesDBClient.
type DBManagementClusterContentLister struct {
	ResourcesDBClient database.ResourcesDBClient
}

var _ listers.ManagementClusterContentLister = &DBManagementClusterContentLister{}

func (l *DBManagementClusterContentLister) List(ctx context.Context) ([]*resourcesapi.ManagementClusterContent, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ManagementClusterContents().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBManagementClusterContentLister) GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, managementClusterContentName string) (*resourcesapi.ManagementClusterContent, error) {
	return l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).ManagementClusterContents(clusterName).Get(ctx, managementClusterContentName)
}

func (l *DBManagementClusterContentLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*resourcesapi.ManagementClusterContent, error) {
	prefix := resourcesapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return l.listMCCWithPrefix(ctx, prefix)
}

func (l *DBManagementClusterContentLister) ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*resourcesapi.ManagementClusterContent, error) {
	prefix := resourcesapi.ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName)
	return l.listMCCWithPrefix(ctx, prefix)
}

func (l *DBManagementClusterContentLister) listMCCWithPrefix(ctx context.Context, prefix string) ([]*resourcesapi.ManagementClusterContent, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*resourcesapi.ManagementClusterContent
	for _, mcc := range all {
		rid := mcc.GetResourceID()
		if rid != nil && strings.HasPrefix(strings.ToLower(rid.String()), strings.ToLower(prefix)) {
			result = append(result, mcc)
		}
	}
	return result, nil
}

// DBSubscriptionLister implements listers.SubscriptionLister backed by a database.ResourcesDBClient.
type DBSubscriptionLister struct {
	ResourcesDBClient database.ResourcesDBClient
}

var _ listers.SubscriptionLister = &DBSubscriptionLister{}

func (l *DBSubscriptionLister) List(ctx context.Context) ([]*armresourcesapi.Subscription, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().Subscriptions().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBSubscriptionLister) Get(ctx context.Context, subscriptionID string) (*armresourcesapi.Subscription, error) {
	return l.ResourcesDBClient.Subscriptions().Get(ctx, subscriptionID)
}

// collectFromIterator collects all items from a database iterator into a slice.
func collectFromIterator[T any](ctx context.Context, iter database.DBClientIterator[T]) ([]*T, error) {
	if err := iter.GetError(); err != nil {
		return nil, err
	}
	var result []*T
	for _, item := range iter.Items(ctx) {
		result = append(result, item)
	}
	return result, iter.GetError()
}
