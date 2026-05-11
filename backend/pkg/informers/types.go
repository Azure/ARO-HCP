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
	"sync"
	"time"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type BackendInformers interface {
	Subscriptions() (cache.SharedIndexInformer, listers.SubscriptionLister)
	ActiveOperations() (cache.SharedIndexInformer, listers.ActiveOperationLister)
	AllOperations() cache.SharedIndexInformer
	Clusters() (cache.SharedIndexInformer, listers.ClusterLister)
	NodePools() (cache.SharedIndexInformer, listers.NodePoolLister)
	ExternalAuths() (cache.SharedIndexInformer, listers.ExternalAuthLister)
	ServiceProviderClusters() (cache.SharedIndexInformer, listers.ServiceProviderClusterLister)
	ServiceProviderNodePools() (cache.SharedIndexInformer, listers.ServiceProviderNodePoolLister)
	Controllers() (cache.SharedIndexInformer, listers.ControllerLister)
	// ManagementClusterContents is the single shared informer for all managementClusterContents documents belonging
	// to different resource types.
	ManagementClusterContents() (cache.SharedIndexInformer, listers.ManagementClusterContentLister)
	BillingDocs() (cache.SharedIndexInformer, listers.BillingLister)

	RunWithContext(ctx context.Context)
}

type backendInformers struct {
	subscriptionInformer cache.SharedIndexInformer
	subscriptionLister   listers.SubscriptionLister

	activeOperationInformer cache.SharedIndexInformer
	activeOperationLister   listers.ActiveOperationLister

	allOperationInformer cache.SharedIndexInformer

	clusterInformer cache.SharedIndexInformer
	clusterLister   listers.ClusterLister

	nodePoolInformer cache.SharedIndexInformer
	nodePoolLister   listers.NodePoolLister

	externalAuthInformer cache.SharedIndexInformer
	externalAuthLister   listers.ExternalAuthLister

	serviceProviderClusterInformer cache.SharedIndexInformer
	serviceProviderClusterLister   listers.ServiceProviderClusterLister

	serviceProviderNodePoolInformer cache.SharedIndexInformer
	serviceProviderNodePoolLister   listers.ServiceProviderNodePoolLister

	controllerInformer               cache.SharedIndexInformer
	controllerLister                 listers.ControllerLister
	managementClusterContentInformer cache.SharedIndexInformer
	managementClusterContentLister   listers.ManagementClusterContentLister

	billingInformer cache.SharedIndexInformer
	billingLister   listers.BillingLister
}

func (b *backendInformers) Subscriptions() (cache.SharedIndexInformer, listers.SubscriptionLister) {
	return b.subscriptionInformer, b.subscriptionLister
}

func (b *backendInformers) ActiveOperations() (cache.SharedIndexInformer, listers.ActiveOperationLister) {
	return b.activeOperationInformer, b.activeOperationLister
}

func (b *backendInformers) AllOperations() cache.SharedIndexInformer {
	return b.allOperationInformer
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

func (b *backendInformers) ServiceProviderNodePools() (cache.SharedIndexInformer, listers.ServiceProviderNodePoolLister) {
	return b.serviceProviderNodePoolInformer, b.serviceProviderNodePoolLister
}

func (b *backendInformers) Controllers() (cache.SharedIndexInformer, listers.ControllerLister) {
	return b.controllerInformer, b.controllerLister
}

func (b *backendInformers) ManagementClusterContents() (cache.SharedIndexInformer, listers.ManagementClusterContentLister) {
	return b.managementClusterContentInformer, b.managementClusterContentLister
}

func (b *backendInformers) BillingDocs() (cache.SharedIndexInformer, listers.BillingLister) {
	return b.billingInformer, b.billingLister
}

func NewBackendInformers(ctx context.Context, resourcesGlobalListers database.ResourcesGlobalListers, billingGlobalListers database.BillingGlobalListers) BackendInformers {
	return NewBackendInformersWithRelistDuration(ctx, resourcesGlobalListers, billingGlobalListers, nil)
}

func NewBackendInformersWithRelistDuration(ctx context.Context, resourcesGlobalListers database.ResourcesGlobalListers, billingGlobalListers database.BillingGlobalListers, relistDuration *time.Duration) BackendInformers {
	subscriptionRelistDuration := SubscriptionRelistDuration
	clusterRelistDuration := ClusterRelistDuration
	nodePoolRelistDuration := NodePoolRelistDuration
	externalAuthRelistDuration := ExternalAuthRelistDuration
	serviceProviderClusterRelistDuration := ServiceProviderClusterRelistDuration
	serviceProviderNodePoolRelistDuration := ServiceProviderNodePoolRelistDuration
	controllerRelistDuration := ControllerRelistDuration
	managementClusterContentRelistDuration := ManagementClusterContentRelistDuration
	allOperationsRelistDuration := AllOperationsRelistDuration
	activeOperationsRelistDuration := ActiveOperationsRelistDuration
	billingRelistDuration := BillingRelistDuration
	if relistDuration != nil {
		subscriptionRelistDuration = *relistDuration
		clusterRelistDuration = *relistDuration
		nodePoolRelistDuration = *relistDuration
		externalAuthRelistDuration = *relistDuration
		serviceProviderClusterRelistDuration = *relistDuration
		serviceProviderNodePoolRelistDuration = *relistDuration
		controllerRelistDuration = *relistDuration
		managementClusterContentRelistDuration = *relistDuration
		allOperationsRelistDuration = *relistDuration
		activeOperationsRelistDuration = *relistDuration
		billingRelistDuration = *relistDuration
	}

	ret := &backendInformers{}
	ret.subscriptionInformer = NewSubscriptionInformerWithRelistDuration(resourcesGlobalListers.Subscriptions(), subscriptionRelistDuration)
	ret.activeOperationInformer = NewActiveOperationInformerWithRelistDuration(resourcesGlobalListers.ActiveOperations(), activeOperationsRelistDuration)
	ret.allOperationInformer = NewOperationInformerWithRelistDuration(resourcesGlobalListers.Operations(), allOperationsRelistDuration)
	ret.clusterInformer = NewClusterInformerWithRelistDuration(resourcesGlobalListers.Clusters(), clusterRelistDuration)
	ret.nodePoolInformer = NewNodePoolInformerWithRelistDuration(resourcesGlobalListers.NodePools(), nodePoolRelistDuration)
	ret.externalAuthInformer = NewExternalAuthInformerWithRelistDuration(resourcesGlobalListers.ExternalAuths(), externalAuthRelistDuration)
	ret.serviceProviderClusterInformer = NewServiceProviderClusterInformerWithRelistDuration(resourcesGlobalListers.ServiceProviderClusters(), serviceProviderClusterRelistDuration)
	ret.serviceProviderNodePoolInformer = NewServiceProviderNodePoolInformerWithRelistDuration(resourcesGlobalListers.ServiceProviderNodePools(), serviceProviderNodePoolRelistDuration)
	ret.controllerInformer = NewControllerInformerWithRelistDuration(resourcesGlobalListers.Controllers(), controllerRelistDuration)
	ret.managementClusterContentInformer = NewManagementClusterContentInformerWithRelistDuration(resourcesGlobalListers.ManagementClusterContents(), managementClusterContentRelistDuration)
	ret.billingInformer = NewBillingInformerWithRelistDuration(billingGlobalListers.BillingDocs(), billingRelistDuration)

	ret.subscriptionLister = listers.NewSubscriptionLister(ret.subscriptionInformer.GetIndexer())
	ret.activeOperationLister = listers.NewActiveOperationLister(ret.activeOperationInformer.GetIndexer())
	ret.clusterLister = listers.NewClusterLister(ret.clusterInformer.GetIndexer())
	ret.nodePoolLister = listers.NewNodePoolLister(ret.nodePoolInformer.GetIndexer())
	ret.externalAuthLister = listers.NewExternalAuthLister(ret.externalAuthInformer.GetIndexer())
	ret.serviceProviderClusterLister = listers.NewServiceProviderClusterLister(ret.serviceProviderClusterInformer.GetIndexer())
	ret.serviceProviderNodePoolLister = listers.NewServiceProviderNodePoolLister(ret.serviceProviderNodePoolInformer.GetIndexer())
	ret.controllerLister = listers.NewControllerLister(ret.controllerInformer.GetIndexer())
	ret.managementClusterContentLister = listers.NewManagementClusterContentLister(ret.managementClusterContentInformer.GetIndexer())
	ret.billingLister = listers.NewBillingLister(ret.billingInformer.GetIndexer())

	return ret
}

func (b *backendInformers) RunWithContext(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting informers")
	defer logger.Info("stopped informers")

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		b.subscriptionInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.activeOperationInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.allOperationInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.clusterInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.nodePoolInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.externalAuthInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.serviceProviderClusterInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.serviceProviderNodePoolInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.controllerInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.managementClusterContentInformer.RunWithContext(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.billingInformer.RunWithContext(ctx)
	}()

	<-ctx.Done()
	wg.Wait()
}
