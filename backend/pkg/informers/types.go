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

package informers

import (
	"context"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type BackendInformers interface {
	Subscriptions() (cache.SharedIndexInformer, listers.SubscriptionLister)
	ActiveOperations() (cache.SharedIndexInformer, listers.ActiveOperationLister)
	Clusters() (cache.SharedIndexInformer, listers.ClusterLister)
	NodePools() (cache.SharedIndexInformer, listers.NodePoolLister)
	ExternalAuths() (cache.SharedIndexInformer, listers.ExternalAuthLister)
	ServiceProviderClusters() (cache.SharedIndexInformer, listers.ServiceProviderClusterLister)

	RunWithContext(ctx context.Context)
}

type backendInformers struct {
	subscriptionInformer cache.SharedIndexInformer
	subscriptionLister   listers.SubscriptionLister

	activeOperationInformer cache.SharedIndexInformer
	activeOperationLister   listers.ActiveOperationLister

	clusterInformer cache.SharedIndexInformer
	clusterLister   listers.ClusterLister

	nodePoolInformer cache.SharedIndexInformer
	nodePoolLister   listers.NodePoolLister

	externalAuthInformer cache.SharedIndexInformer
	externalAuthLister   listers.ExternalAuthLister

	serviceProviderClusterInformer cache.SharedIndexInformer
	serviceProviderClusterLister   listers.ServiceProviderClusterLister
}

func (b *backendInformers) Subscriptions() (cache.SharedIndexInformer, listers.SubscriptionLister) {
	return b.subscriptionInformer, b.subscriptionLister
}

func (b *backendInformers) ActiveOperations() (cache.SharedIndexInformer, listers.ActiveOperationLister) {
	return b.activeOperationInformer, b.activeOperationLister
}

func (b *backendInformers) Clusters() (cache.SharedIndexInformer, listers.ClusterLister) {
	return b.clusterInformer, b.clusterLister
}

func (b *backendInformers) NodePools() (cache.SharedIndexInformer, listers.NodePoolLister) {
	return b.nodePoolInformer, b.nodePoolLister
}

func (b *backendInformers) ExternalAuths() (cache.SharedIndexInformer, listers.ExternalAuthLister) {
	return b.externalAuthInformer, b.externalAuthLister
}

func (b *backendInformers) ServiceProviderClusters() (cache.SharedIndexInformer, listers.ServiceProviderClusterLister) {
	return b.serviceProviderClusterInformer, b.serviceProviderClusterLister
}

func NewBackendInformers(ctx context.Context, globalListers database.GlobalListers) BackendInformers {
	ret := &backendInformers{}
	ret.subscriptionInformer = NewSubscriptionInformer(globalListers.Subscriptions())
	ret.subscriptionLister = listers.NewSubscriptionLister(ret.subscriptionInformer.GetIndexer())

	ret.activeOperationInformer = NewActiveOperationInformer(globalListers.ActiveOperations())
	ret.activeOperationLister = listers.NewActiveOperationLister(ret.activeOperationInformer.GetIndexer())

	ret.clusterInformer = NewClusterInformer(globalListers.Clusters())
	ret.clusterLister = listers.NewClusterLister(ret.clusterInformer.GetIndexer())

	ret.nodePoolInformer = NewNodePoolInformer(globalListers.NodePools())
	ret.nodePoolLister = listers.NewNodePoolLister(ret.nodePoolInformer.GetIndexer())

	ret.externalAuthInformer = NewExternalAuthInformer(globalListers.ExternalAuths())
	ret.externalAuthLister = listers.NewExternalAuthLister(ret.externalAuthInformer.GetIndexer())

	ret.serviceProviderClusterInformer = NewServiceProviderClusterInformer(globalListers.ServiceProviderClusters())
	ret.serviceProviderClusterLister = listers.NewServiceProviderClusterLister(ret.serviceProviderClusterInformer.GetIndexer())

	return ret
}

func (b *backendInformers) RunWithContext(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting informers")
	defer logger.Info("stopped informers")

	go b.subscriptionInformer.RunWithContext(ctx)
	go b.activeOperationInformer.RunWithContext(ctx)
	go b.clusterInformer.RunWithContext(ctx)
	go b.nodePoolInformer.RunWithContext(ctx)
	go b.externalAuthInformer.RunWithContext(ctx)
	go b.serviceProviderClusterInformer.RunWithContext(ctx)

	<-ctx.Done()
}
