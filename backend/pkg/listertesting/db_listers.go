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
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

// DBClusterLister implements listers.ClusterLister backed by a database.DBClient.
type DBClusterLister struct {
	DBClient database.DBClient
}

var _ listers.ClusterLister = &DBClusterLister{}

func (l *DBClusterLister) List(ctx context.Context) ([]*api.HCPOpenShiftCluster, error) {
	iter, err := l.DBClient.GlobalListers().Clusters().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.HCPOpenShiftCluster, error) {
	return l.DBClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, clusterName)
}

func (l *DBClusterLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*api.HCPOpenShiftCluster, error) {
	iter, err := l.DBClient.HCPClusters(subscriptionID, resourceGroupName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

// DBNodePoolLister implements listers.NodePoolLister backed by a database.DBClient.
type DBNodePoolLister struct {
	DBClient database.DBClient
}

var _ listers.NodePoolLister = &DBNodePoolLister{}

func (l *DBNodePoolLister) List(ctx context.Context) ([]*api.HCPOpenShiftClusterNodePool, error) {
	iter, err := l.DBClient.GlobalListers().NodePools().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*api.HCPOpenShiftClusterNodePool, error) {
	return l.DBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).Get(ctx, nodePoolName)
}

func (l *DBNodePoolLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*api.HCPOpenShiftClusterNodePool, error) {
	// List all node pools and filter by resource group
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*api.HCPOpenShiftClusterNodePool
	for _, np := range all {
		if np.ID != nil &&
			strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, np)
		}
	}
	return result, nil
}

func (l *DBNodePoolLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterNodePool, error) {
	iter, err := l.DBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

// DBActiveOperationLister implements listers.ActiveOperationLister backed by a database.DBClient.
type DBActiveOperationLister struct {
	DBClient database.DBClient
}

var _ listers.ActiveOperationLister = &DBActiveOperationLister{}

func (l *DBActiveOperationLister) List(ctx context.Context) ([]*api.Operation, error) {
	iter, err := l.DBClient.GlobalListers().ActiveOperations().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBActiveOperationLister) Get(ctx context.Context, subscriptionID, name string) (*api.Operation, error) {
	return l.DBClient.Operations(subscriptionID).Get(ctx, name)
}

func (l *DBActiveOperationLister) ListActiveOperationsForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.Operation, error) {
	clusterKey := api.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*api.Operation
	for _, op := range all {
		if op.ExternalID != nil && strings.HasPrefix(strings.ToLower(op.ExternalID.String()), strings.ToLower(clusterKey)) {
			result = append(result, op)
		}
	}
	return result, nil
}

// DBExternalAuthLister implements listers.ExternalAuthLister backed by a database.DBClient.
type DBExternalAuthLister struct {
	DBClient database.DBClient
}

var _ listers.ExternalAuthLister = &DBExternalAuthLister{}

func (l *DBExternalAuthLister) List(ctx context.Context) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	iter, err := l.DBClient.GlobalListers().ExternalAuths().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*api.HCPOpenShiftClusterExternalAuth, error) {
	return l.DBClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName).Get(ctx, externalAuthName)
}

func (l *DBExternalAuthLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*api.HCPOpenShiftClusterExternalAuth
	for _, ea := range all {
		if ea.ID != nil &&
			strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, ea)
		}
	}
	return result, nil
}

func (l *DBExternalAuthLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	iter, err := l.DBClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

// DBServiceProviderClusterLister implements listers.ServiceProviderClusterLister backed by a database.DBClient.
type DBServiceProviderClusterLister struct {
	DBClient database.DBClient
}

var _ listers.ServiceProviderClusterLister = &DBServiceProviderClusterLister{}

func (l *DBServiceProviderClusterLister) List(ctx context.Context) ([]*api.ServiceProviderCluster, error) {
	iter, err := l.DBClient.GlobalListers().ServiceProviderClusters().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBServiceProviderClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.ServiceProviderCluster, error) {
	return l.DBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(ctx, "default")
}

func (l *DBServiceProviderClusterLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.ServiceProviderCluster, error) {
	iter, err := l.DBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

// DBControllerLister implements listers.ControllerLister backed by a database.DBClient.
type DBControllerLister struct {
	DBClient database.DBClient
}

var _ listers.ControllerLister = &DBControllerLister{}

func (l *DBControllerLister) List(ctx context.Context) ([]*api.Controller, error) {
	iter, err := l.DBClient.GlobalListers().Controllers().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBControllerLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*api.Controller, error) {
	prefix := api.ToResourceGroupResourceIDString(subscriptionID, resourceGroupName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.Controller, error) {
	prefix := api.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*api.Controller, error) {
	prefix := api.ToNodePoolResourceIDString(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) ListForExternalAuth(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) ([]*api.Controller, error) {
	prefix := api.ToExternalAuthResourceIDString(subscriptionID, resourceGroupName, clusterName, externalAuthName)
	return l.listWithPrefix(ctx, prefix)
}

func (l *DBControllerLister) listWithPrefix(ctx context.Context, prefix string) ([]*api.Controller, error) {
	all, err := l.List(ctx)
	if err != nil {
		return nil, err
	}
	var result []*api.Controller
	for _, c := range all {
		if c.ResourceID != nil && strings.HasPrefix(strings.ToLower(c.ResourceID.String()), strings.ToLower(prefix)) {
			result = append(result, c)
		}
	}
	return result, nil
}

// DBSubscriptionLister implements listers.SubscriptionLister backed by a database.DBClient.
type DBSubscriptionLister struct {
	DBClient database.DBClient
}

var _ listers.SubscriptionLister = &DBSubscriptionLister{}

func (l *DBSubscriptionLister) List(ctx context.Context) ([]*arm.Subscription, error) {
	iter, err := l.DBClient.GlobalListers().Subscriptions().List(ctx, nil)
	if err != nil {
		return nil, err
	}
	return collectFromIterator(ctx, iter)
}

func (l *DBSubscriptionLister) Get(ctx context.Context, subscriptionID string) (*arm.Subscription, error) {
	return l.DBClient.Subscriptions().Get(ctx, subscriptionID)
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
