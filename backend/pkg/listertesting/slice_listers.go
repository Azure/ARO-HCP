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

// SliceClusterLister implements listers.ClusterLister backed by a slice.
type SliceClusterLister struct {
	Clusters []*api.HCPOpenShiftCluster
}

var _ listers.ClusterLister = &SliceClusterLister{}

func (l *SliceClusterLister) List(ctx context.Context) ([]*api.HCPOpenShiftCluster, error) {
	return l.Clusters, nil
}

func (l *SliceClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.HCPOpenShiftCluster, error) {
	for _, c := range l.Clusters {
		if c.ID == nil {
			continue
		}
		if strings.EqualFold(c.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(c.ID.ResourceGroupName, resourceGroupName) &&
			strings.EqualFold(c.ID.Name, clusterName) {
			return c, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceClusterLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*api.HCPOpenShiftCluster, error) {
	var result []*api.HCPOpenShiftCluster
	for _, c := range l.Clusters {
		if c.ID == nil {
			continue
		}
		if strings.EqualFold(c.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(c.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, c)
		}
	}
	return result, nil
}

// SliceNodePoolLister implements listers.NodePoolLister backed by a slice.
type SliceNodePoolLister struct {
	NodePools []*api.HCPOpenShiftClusterNodePool
}

var _ listers.NodePoolLister = &SliceNodePoolLister{}

func (l *SliceNodePoolLister) List(ctx context.Context) ([]*api.HCPOpenShiftClusterNodePool, error) {
	return l.NodePools, nil
}

func (l *SliceNodePoolLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) (*api.HCPOpenShiftClusterNodePool, error) {
	for _, np := range l.NodePools {
		if np.ID == nil {
			continue
		}
		if strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) &&
			nodePoolMatchesCluster(np.ID, clusterName) &&
			strings.EqualFold(np.ID.Name, nodePoolName) {
			return np, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceNodePoolLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*api.HCPOpenShiftClusterNodePool, error) {
	var result []*api.HCPOpenShiftClusterNodePool
	for _, np := range l.NodePools {
		if np.ID == nil {
			continue
		}
		if strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, np)
		}
	}
	return result, nil
}

func (l *SliceNodePoolLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterNodePool, error) {
	var result []*api.HCPOpenShiftClusterNodePool
	for _, np := range l.NodePools {
		if np.ID == nil {
			continue
		}
		if strings.EqualFold(np.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(np.ID.ResourceGroupName, resourceGroupName) &&
			nodePoolMatchesCluster(np.ID, clusterName) {
			result = append(result, np)
		}
	}
	return result, nil
}

// SliceActiveOperationLister implements listers.ActiveOperationLister backed by a slice.
type SliceActiveOperationLister struct {
	Operations []*api.Operation
}

var _ listers.ActiveOperationLister = &SliceActiveOperationLister{}

func (l *SliceActiveOperationLister) List(ctx context.Context) ([]*api.Operation, error) {
	return l.Operations, nil
}

func (l *SliceActiveOperationLister) Get(ctx context.Context, subscriptionID, name string) (*api.Operation, error) {
	for _, op := range l.Operations {
		if op.OperationID == nil {
			continue
		}
		if strings.EqualFold(op.OperationID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(op.OperationID.Name, name) {
			return op, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceActiveOperationLister) ListActiveOperationsForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.Operation, error) {
	clusterKey := api.ToClusterResourceIDString(subscriptionID, resourceGroupName, clusterName)
	var result []*api.Operation
	for _, op := range l.Operations {
		if op.ExternalID == nil {
			continue
		}
		if strings.HasPrefix(strings.ToLower(op.ExternalID.String()), strings.ToLower(clusterKey)) {
			result = append(result, op)
		}
	}
	return result, nil
}

// SliceExternalAuthLister implements listers.ExternalAuthLister backed by a slice.
type SliceExternalAuthLister struct {
	ExternalAuths []*api.HCPOpenShiftClusterExternalAuth
}

var _ listers.ExternalAuthLister = &SliceExternalAuthLister{}

func (l *SliceExternalAuthLister) List(ctx context.Context) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	return l.ExternalAuths, nil
}

func (l *SliceExternalAuthLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, externalAuthName string) (*api.HCPOpenShiftClusterExternalAuth, error) {
	for _, ea := range l.ExternalAuths {
		if ea.ID == nil {
			continue
		}
		if strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) &&
			externalAuthMatchesCluster(ea.ID, clusterName) &&
			strings.EqualFold(ea.ID.Name, externalAuthName) {
			return ea, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceExternalAuthLister) ListForResourceGroup(ctx context.Context, subscriptionID, resourceGroupName string) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	var result []*api.HCPOpenShiftClusterExternalAuth
	for _, ea := range l.ExternalAuths {
		if ea.ID == nil {
			continue
		}
		if strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) {
			result = append(result, ea)
		}
	}
	return result, nil
}

func (l *SliceExternalAuthLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.HCPOpenShiftClusterExternalAuth, error) {
	var result []*api.HCPOpenShiftClusterExternalAuth
	for _, ea := range l.ExternalAuths {
		if ea.ID == nil {
			continue
		}
		if strings.EqualFold(ea.ID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(ea.ID.ResourceGroupName, resourceGroupName) &&
			externalAuthMatchesCluster(ea.ID, clusterName) {
			result = append(result, ea)
		}
	}
	return result, nil
}

// SliceServiceProviderClusterLister implements listers.ServiceProviderClusterLister backed by a slice.
type SliceServiceProviderClusterLister struct {
	ServiceProviderClusters []*api.ServiceProviderCluster
}

var _ listers.ServiceProviderClusterLister = &SliceServiceProviderClusterLister{}

func (l *SliceServiceProviderClusterLister) List(ctx context.Context) ([]*api.ServiceProviderCluster, error) {
	return l.ServiceProviderClusters, nil
}

func (l *SliceServiceProviderClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.ServiceProviderCluster, error) {
	for _, spc := range l.ServiceProviderClusters {
		resourceID := spc.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			serviceProviderClusterMatchesCluster(resourceID, clusterName) {
			return spc, nil
		}
	}
	return nil, database.NewNotFoundError()
}

func (l *SliceServiceProviderClusterLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*api.ServiceProviderCluster, error) {
	var result []*api.ServiceProviderCluster
	for _, spc := range l.ServiceProviderClusters {
		resourceID := spc.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) &&
			strings.EqualFold(resourceID.ResourceGroupName, resourceGroupName) &&
			serviceProviderClusterMatchesCluster(resourceID, clusterName) {
			result = append(result, spc)
		}
	}
	return result, nil
}

// SliceSubscriptionLister implements listers.SubscriptionLister backed by a slice.
type SliceSubscriptionLister struct {
	Subscriptions []*arm.Subscription
}

var _ listers.SubscriptionLister = &SliceSubscriptionLister{}

func (l *SliceSubscriptionLister) List(ctx context.Context) ([]*arm.Subscription, error) {
	return l.Subscriptions, nil
}

func (l *SliceSubscriptionLister) Get(ctx context.Context, subscriptionID string) (*arm.Subscription, error) {
	for _, s := range l.Subscriptions {
		resourceID := s.GetResourceID()
		if resourceID == nil {
			continue
		}
		if strings.EqualFold(resourceID.SubscriptionID, subscriptionID) {
			return s, nil
		}
	}
	return nil, database.NewNotFoundError()
}
