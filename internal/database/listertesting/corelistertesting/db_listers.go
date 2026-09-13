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

package corelistertesting

import (
	"context"
	"strings"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/listertestingutils"
)

// DBClusterLister implements corelisters.ClusterLister backed by a corecosmosstorage.ResourcesDBClient.
type DBClusterLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.ClusterLister = &DBClusterLister{}

func (l *DBClusterLister) List(ctx context.Context) ([]*coreapi.HCPOpenShiftCluster, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().Clusters().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.HCPOpenShiftCluster, error) {
	return l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, clusterName)
}

func (l *DBClusterLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*coreapi.HCPOpenShiftCluster, error) {
	iter, err := l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

// DBNodePoolLister implements corelisters.NodePoolLister backed by a corecosmosstorage.ResourcesDBClient.
type DBNodePoolLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.NodePoolLister = &DBNodePoolLister{}

func (l *DBNodePoolLister) List(ctx context.Context) ([]*coreapi.HCPOpenShiftClusterNodePool, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().NodePools().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*coreapi.HCPOpenShiftClusterNodePool, error) {
	return l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).Get(ctx, nodePoolName)
}

func (l *DBNodePoolLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*coreapi.HCPOpenShiftClusterNodePool, error) {
	// List all node pools and filter by resource group
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*coreapi.HCPOpenShiftClusterNodePool
	for _, np := range all {
		if np.ID != nil &&
			strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, np)
		}
	}
	return result, nil
}

func (l *DBNodePoolLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.HCPOpenShiftClusterNodePool, error) {
	iter, err := l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

// DBServiceProviderNodePoolLister implements corelisters.ServiceProviderNodePoolLister backed by a corecosmosstorage.ResourcesDBClient.
type DBServiceProviderNodePoolLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.ServiceProviderNodePoolLister = &DBServiceProviderNodePoolLister{}

func (l *DBServiceProviderNodePoolLister) List(ctx context.Context) ([]*coreapi.ServiceProviderNodePool, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ServiceProviderNodePools().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBServiceProviderNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*coreapi.ServiceProviderNodePool, error) {
	return l.ResourcesDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName).
		Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
}

func (l *DBServiceProviderNodePoolLister) ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*coreapi.ServiceProviderNodePool, error) {
	iter, err := l.ResourcesDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

// DBServiceProviderExternalAuthLister implements corelisters.ServiceProviderExternalAuthLister backed by a corecosmosstorage.ResourcesDBClient.
type DBServiceProviderExternalAuthLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.ServiceProviderExternalAuthLister = &DBServiceProviderExternalAuthLister{}

func (l *DBServiceProviderExternalAuthLister) List(ctx context.Context) ([]*coreapi.ServiceProviderExternalAuth, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ServiceProviderExternalAuths().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBServiceProviderExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*coreapi.ServiceProviderExternalAuth, error) {
	return l.ResourcesDBClient.ServiceProviderExternalAuths(subscriptionID, resourceGroupName, clusterName, externalAuthName).
		Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
}

func (l *DBServiceProviderExternalAuthLister) ListForExternalAuth(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) ([]*coreapi.ServiceProviderExternalAuth, error) {
	iter, err := l.ResourcesDBClient.ServiceProviderExternalAuths(subscriptionID, resourceGroupName, clusterName, externalAuthName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

// DBActiveOperationLister implements corelisters.ActiveOperationLister backed by a corecosmosstorage.ResourcesDBClient.
type DBActiveOperationLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.ActiveOperationLister = &DBActiveOperationLister{}

func (l *DBActiveOperationLister) List(ctx context.Context) ([]*coreapi.Operation, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ActiveOperations().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBActiveOperationLister) Get(ctx context.Context, subscriptionID, name string) (*coreapi.Operation, error) {
	return l.ResourcesDBClient.Operations(subscriptionID).Get(ctx, name)
}

// ListActiveOperationsForCluster returns active operations for the cluster and its
// child resources (node pools, external auths), matching production lister semantics.
func (l *DBActiveOperationLister) ListActiveOperationsForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.Operation, error) {
	clusterKey := coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return l.listByPrefix(ctx, clusterKey)
}

func (l *DBActiveOperationLister) ListActiveOperationsForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*coreapi.Operation, error) {
	nodePoolKey := coreapi.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return l.listByPrefix(ctx, nodePoolKey)
}

func (l *DBActiveOperationLister) ListActiveOperationsForExternalAuth(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) ([]*coreapi.Operation, error) {
	externalAuthKey := coreapi.ToExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return l.listByPrefix(ctx, externalAuthKey)
}

func (l *DBActiveOperationLister) listByPrefix(ctx context.Context, prefix string) ([]*coreapi.Operation, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*coreapi.Operation
	for _, op := range all {
		if op.ExternalID != nil && strings.HasPrefix(strings.ToLower(op.ExternalID.String()), strings.ToLower(prefix)) {
			result = append(result, op)
		}
	}
	return result, nil
}

// DBExternalAuthLister implements corelisters.ExternalAuthLister backed by a corecosmosstorage.ResourcesDBClient.
type DBExternalAuthLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.ExternalAuthLister = &DBExternalAuthLister{}

func (l *DBExternalAuthLister) List(ctx context.Context) ([]*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ExternalAuths().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	return l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName).Get(ctx, externalAuthName)
}

func (l *DBExternalAuthLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*coreapi.HCPOpenShiftClusterExternalAuth
	for _, ea := range all {
		if ea.ID != nil &&
			strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, ea)
		}
	}
	return result, nil
}

func (l *DBExternalAuthLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.HCPOpenShiftClusterExternalAuth, error) {
	iter, err := l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

// DBServiceProviderClusterLister implements corelisters.ServiceProviderClusterLister backed by a corecosmosstorage.ResourcesDBClient.
type DBServiceProviderClusterLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.ServiceProviderClusterLister = &DBServiceProviderClusterLister{}

func (l *DBServiceProviderClusterLister) List(ctx context.Context) ([]*coreapi.ServiceProviderCluster, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ServiceProviderClusters().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBServiceProviderClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*coreapi.ServiceProviderCluster, error) {
	return l.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
}

func (l *DBServiceProviderClusterLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.ServiceProviderCluster, error) {
	iter, err := l.ResourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

// DBControllerLister implements corelisters.ControllerLister backed by a corecosmosstorage.ResourcesDBClient.
type DBControllerLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.ControllerLister = &DBControllerLister{}

func (l *DBControllerLister) List(ctx context.Context) ([]*coreapi.Controller, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().Controllers().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBControllerLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*coreapi.Controller, error) {
	prefix := coreapi.ToResourceGroupResourceIDString(subscriptionID, resourceGroupName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.Controller, error) {
	prefix := coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*coreapi.Controller, error) {
	prefix := coreapi.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForExternalAuth(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) ([]*coreapi.Controller, error) {
	prefix := coreapi.ToExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) listWithPrefix(ctx context.Context, prefix string) ([]*coreapi.Controller, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*coreapi.Controller
	for _, c := range all {
		if c.ResourceID != nil && strings.HasPrefix(strings.ToLower(c.ResourceID.String()), strings.ToLower(prefix)) {
			result = append(result, c)
		}
	}
	return result, nil
}

// DBManagementClusterContentLister implements corelisters.ManagementClusterContentLister backed by a corecosmosstorage.ResourcesDBClient.
type DBManagementClusterContentLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.ManagementClusterContentLister = &DBManagementClusterContentLister{}

func (l *DBManagementClusterContentLister) List(ctx context.Context) ([]*coreapi.ManagementClusterContent, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().ManagementClusterContents().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBManagementClusterContentLister) GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, managementClusterContentName string) (*coreapi.ManagementClusterContent, error) {
	return l.ResourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).ManagementClusterContents(clusterName).Get(ctx, managementClusterContentName)
}

func (l *DBManagementClusterContentLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*coreapi.ManagementClusterContent, error) {
	prefix := coreapi.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return l.listMCCWithPrefix(ctx, prefix)
}

func (l *DBManagementClusterContentLister) ListForNodePool(ctx context.Context, subscriptionName, resourceGroupName, clusterName, nodePoolName string) ([]*coreapi.ManagementClusterContent, error) {
	prefix := coreapi.ToNodePoolResourceIDString(subscriptionName, resourceGroupName, clusterName, nodePoolName)
	return l.listMCCWithPrefix(ctx, prefix)
}

func (l *DBManagementClusterContentLister) listMCCWithPrefix(ctx context.Context, prefix string) ([]*coreapi.ManagementClusterContent, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*coreapi.ManagementClusterContent
	for _, mcc := range all {
		rid := mcc.GetResourceID()
		if rid != nil && strings.HasPrefix(strings.ToLower(rid.String()), strings.ToLower(prefix)) {
			result = append(result, mcc)
		}
	}
	return result, nil
}

// DBSubscriptionLister implements corelisters.SubscriptionLister backed by a corecosmosstorage.ResourcesDBClient.
type DBSubscriptionLister struct {
	ResourcesDBClient corecosmosstorage.ResourcesDBClient
}

var _ corelisters.SubscriptionLister = &DBSubscriptionLister{}

func (l *DBSubscriptionLister) List(ctx context.Context) ([]*coreapi.Subscription, error) {
	iter, err := l.ResourcesDBClient.ResourcesGlobalListers().Subscriptions().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return listertestingutils.CollectFromIterator(ctx, iter)
}

func (l *DBSubscriptionLister) Get(ctx context.Context, subscriptionID string) (*coreapi.Subscription, error) {
	return l.ResourcesDBClient.Subscriptions().Get(ctx, subscriptionID)
}
