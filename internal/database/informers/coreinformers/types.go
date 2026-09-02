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

package coreinformers

import (
	"context"
	"reflect"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type BackendInformers interface {
	Subscriptions() (cache.SharedIndexInformer, corelisters.SubscriptionLister)
	ActiveOperations() (cache.SharedIndexInformer, corelisters.ActiveOperationLister)
	AllOperations() cache.SharedIndexInformer
	Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister)
	NodePools() (cache.SharedIndexInformer, corelisters.NodePoolLister)
	ExternalAuths() (cache.SharedIndexInformer, corelisters.ExternalAuthLister)
	ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister)
	ServiceProviderNodePools() (cache.SharedIndexInformer, corelisters.ServiceProviderNodePoolLister)
	ServiceProviderExternalAuths() (cache.SharedIndexInformer, corelisters.ServiceProviderExternalAuthLister)
	Controllers() (cache.SharedIndexInformer, corelisters.ControllerLister)
	// ManagementClusterContents is the single shared informer for all managementClusterContents documents belonging
	// to different resource types.
	ManagementClusterContents() (cache.SharedIndexInformer, corelisters.ManagementClusterContentLister)
	SystemAdminCredentialRequests() (cache.SharedIndexInformer, corelisters.SystemAdminCredentialRequestLister)
	SystemAdminCredentialRevocations() (cache.SharedIndexInformer, corelisters.SystemAdminCredentialRevocationLister)
	BillingDocs() (cache.SharedIndexInformer, corelisters.BillingLister)

	RunWithContext(ctx context.Context)
}

type backendInformers struct {
	subscriptionInformer cache.SharedIndexInformer
	subscriptionLister   corelisters.SubscriptionLister

	activeOperationInformer cache.SharedIndexInformer
	activeOperationLister   corelisters.ActiveOperationLister

	allOperationInformer cache.SharedIndexInformer

	clusterInformer cache.SharedIndexInformer
	clusterLister   corelisters.ClusterLister

	nodePoolInformer cache.SharedIndexInformer
	nodePoolLister   corelisters.NodePoolLister

	externalAuthInformer cache.SharedIndexInformer
	externalAuthLister   corelisters.ExternalAuthLister

	serviceProviderClusterInformer cache.SharedIndexInformer
	serviceProviderClusterLister   corelisters.ServiceProviderClusterLister

	serviceProviderNodePoolInformer cache.SharedIndexInformer
	serviceProviderNodePoolLister   corelisters.ServiceProviderNodePoolLister

	serviceProviderExternalAuthInformer cache.SharedIndexInformer
	serviceProviderExternalAuthLister   corelisters.ServiceProviderExternalAuthLister

	controllerInformer               cache.SharedIndexInformer
	controllerLister                 corelisters.ControllerLister
	managementClusterContentInformer cache.SharedIndexInformer
	managementClusterContentLister   corelisters.ManagementClusterContentLister

	systemAdminCredentialRequestInformer cache.SharedIndexInformer
	systemAdminCredentialRequestLister   corelisters.SystemAdminCredentialRequestLister

	systemAdminCredentialRevocationInformer cache.SharedIndexInformer
	systemAdminCredentialRevocationLister   corelisters.SystemAdminCredentialRevocationLister

	billingInformer cache.SharedIndexInformer
	billingLister   corelisters.BillingLister
}

func (b *backendInformers) Subscriptions() (cache.SharedIndexInformer, corelisters.SubscriptionLister) {
	return b.subscriptionInformer, b.subscriptionLister
}

func (b *backendInformers) ActiveOperations() (cache.SharedIndexInformer, corelisters.ActiveOperationLister) {
	return b.activeOperationInformer, b.activeOperationLister
}

func (b *backendInformers) AllOperations() cache.SharedIndexInformer {
	return b.allOperationInformer
}

func (b *backendInformers) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
	return b.clusterInformer, b.clusterLister
}

func (b *backendInformers) NodePools() (cache.SharedIndexInformer, corelisters.NodePoolLister) {
	return b.nodePoolInformer, b.nodePoolLister
}

func (b *backendInformers) ExternalAuths() (cache.SharedIndexInformer, corelisters.ExternalAuthLister) {
	return b.externalAuthInformer, b.externalAuthLister
}

func (b *backendInformers) ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister) {
	return b.serviceProviderClusterInformer, b.serviceProviderClusterLister
}

func (b *backendInformers) ServiceProviderNodePools() (cache.SharedIndexInformer, corelisters.ServiceProviderNodePoolLister) {
	return b.serviceProviderNodePoolInformer, b.serviceProviderNodePoolLister
}

func (b *backendInformers) ServiceProviderExternalAuths() (cache.SharedIndexInformer, corelisters.ServiceProviderExternalAuthLister) {
	return b.serviceProviderExternalAuthInformer, b.serviceProviderExternalAuthLister
}

func (b *backendInformers) Controllers() (cache.SharedIndexInformer, corelisters.ControllerLister) {
	return b.controllerInformer, b.controllerLister
}

func (b *backendInformers) ManagementClusterContents() (cache.SharedIndexInformer, corelisters.ManagementClusterContentLister) {
	return b.managementClusterContentInformer, b.managementClusterContentLister
}

func (b *backendInformers) SystemAdminCredentialRequests() (cache.SharedIndexInformer, corelisters.SystemAdminCredentialRequestLister) {
	return b.systemAdminCredentialRequestInformer, b.systemAdminCredentialRequestLister
}

func (b *backendInformers) SystemAdminCredentialRevocations() (cache.SharedIndexInformer, corelisters.SystemAdminCredentialRevocationLister) {
	return b.systemAdminCredentialRevocationInformer, b.systemAdminCredentialRevocationLister
}

func (b *backendInformers) BillingDocs() (cache.SharedIndexInformer, corelisters.BillingLister) {
	return b.billingInformer, b.billingLister
}

func NewBackendInformers(ctx context.Context, resourcesGlobalListers corecosmosstorage.ResourcesGlobalListers, resourcesDBClient corecosmosstorage.ResourcesDBClient, billingGlobalListers billingcosmosstorage.BillingGlobalListers) BackendInformers {
	return NewBackendInformersWithRelistDuration(ctx, resourcesGlobalListers, resourcesDBClient, billingGlobalListers, nil)
}

func NewBackendInformersWithRelistDuration(ctx context.Context, resourcesGlobalListers corecosmosstorage.ResourcesGlobalListers, resourcesDBClient corecosmosstorage.ResourcesDBClient, billingGlobalListers billingcosmosstorage.BillingGlobalListers, relistDuration *time.Duration) BackendInformers {
	subscriptionRelistDuration := SubscriptionRelistDuration
	clusterRelistDuration := ClusterRelistDuration
	nodePoolRelistDuration := NodePoolRelistDuration
	externalAuthRelistDuration := ExternalAuthRelistDuration
	serviceProviderClusterRelistDuration := ServiceProviderClusterRelistDuration
	serviceProviderNodePoolRelistDuration := ServiceProviderNodePoolRelistDuration
	serviceProviderExternalAuthRelistDuration := ServiceProviderExternalAuthRelistDuration
	controllerRelistDuration := ControllerRelistDuration
	managementClusterContentRelistDuration := ManagementClusterContentRelistDuration
	systemAdminCredentialRequestRelistDuration := SystemAdminCredentialRequestRelistDuration
	systemAdminCredentialRevocationRelistDuration := SystemAdminCredentialRevocationRelistDuration
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
		serviceProviderExternalAuthRelistDuration = *relistDuration
		controllerRelistDuration = *relistDuration
		managementClusterContentRelistDuration = *relistDuration
		systemAdminCredentialRequestRelistDuration = *relistDuration
		systemAdminCredentialRevocationRelistDuration = *relistDuration
		allOperationsRelistDuration = *relistDuration
		activeOperationsRelistDuration = *relistDuration
		billingRelistDuration = *relistDuration
	}

	ret := &backendInformers{}
	ret.subscriptionInformer = NewSubscriptionInformerWithRelistDuration(resourcesGlobalListers.Subscriptions(), resourcesDBClient, subscriptionRelistDuration)
	ret.activeOperationInformer = NewActiveOperationInformerWithRelistDuration(resourcesGlobalListers.ActiveOperations(), resourcesDBClient, activeOperationsRelistDuration)
	ret.allOperationInformer = NewOperationInformerWithRelistDuration(resourcesGlobalListers.Operations(), resourcesDBClient, allOperationsRelistDuration)
	ret.clusterInformer = NewClusterInformerWithRelistDuration(resourcesGlobalListers.Clusters(), resourcesDBClient, clusterRelistDuration)
	ret.nodePoolInformer = NewNodePoolInformerWithRelistDuration(resourcesGlobalListers.NodePools(), resourcesDBClient, nodePoolRelistDuration)
	ret.externalAuthInformer = NewExternalAuthInformerWithRelistDuration(resourcesGlobalListers.ExternalAuths(), resourcesDBClient, externalAuthRelistDuration)
	ret.serviceProviderClusterInformer = NewServiceProviderClusterInformerWithRelistDuration(resourcesGlobalListers.ServiceProviderClusters(), resourcesDBClient, serviceProviderClusterRelistDuration)
	ret.serviceProviderNodePoolInformer = NewServiceProviderNodePoolInformerWithRelistDuration(resourcesGlobalListers.ServiceProviderNodePools(), resourcesDBClient, serviceProviderNodePoolRelistDuration)
	ret.serviceProviderExternalAuthInformer = NewServiceProviderExternalAuthInformerWithRelistDuration(resourcesGlobalListers.ServiceProviderExternalAuths(), resourcesDBClient, serviceProviderExternalAuthRelistDuration)
	ret.controllerInformer = NewControllerInformerWithRelistDuration(resourcesGlobalListers.Controllers(), resourcesDBClient, controllerRelistDuration)
	ret.managementClusterContentInformer = NewManagementClusterContentInformerWithRelistDuration(resourcesGlobalListers.ManagementClusterContents(), managementClusterContentRelistDuration)
	ret.systemAdminCredentialRequestInformer = NewSystemAdminCredentialRequestInformerWithRelistDuration(resourcesGlobalListers.SystemAdminCredentialRequests(), resourcesDBClient, systemAdminCredentialRequestRelistDuration)
	ret.systemAdminCredentialRevocationInformer = NewSystemAdminCredentialRevocationInformerWithRelistDuration(resourcesGlobalListers.SystemAdminCredentialRevocations(), resourcesDBClient, systemAdminCredentialRevocationRelistDuration)
	ret.billingInformer = NewBillingInformerWithRelistDuration(billingGlobalListers.BillingDocs(), billingRelistDuration)

	ret.subscriptionLister = corelisters.NewSubscriptionLister(ret.subscriptionInformer.GetIndexer())
	ret.activeOperationLister = corelisters.NewActiveOperationLister(ret.activeOperationInformer.GetIndexer())
	ret.clusterLister = corelisters.NewClusterLister(ret.clusterInformer.GetIndexer())
	ret.nodePoolLister = corelisters.NewNodePoolLister(ret.nodePoolInformer.GetIndexer())
	ret.externalAuthLister = corelisters.NewExternalAuthLister(ret.externalAuthInformer.GetIndexer())
	ret.serviceProviderClusterLister = corelisters.NewServiceProviderClusterLister(ret.serviceProviderClusterInformer.GetIndexer())
	ret.serviceProviderNodePoolLister = corelisters.NewServiceProviderNodePoolLister(ret.serviceProviderNodePoolInformer.GetIndexer())
	ret.serviceProviderExternalAuthLister = corelisters.NewServiceProviderExternalAuthLister(ret.serviceProviderExternalAuthInformer.GetIndexer())
	ret.controllerLister = corelisters.NewControllerLister(ret.controllerInformer.GetIndexer())
	ret.managementClusterContentLister = corelisters.NewManagementClusterContentLister(ret.managementClusterContentInformer.GetIndexer())
	ret.systemAdminCredentialRequestLister = corelisters.NewSystemAdminCredentialRequestLister(ret.systemAdminCredentialRequestInformer.GetIndexer())
	ret.systemAdminCredentialRevocationLister = corelisters.NewSystemAdminCredentialRevocationLister(ret.systemAdminCredentialRevocationInformer.GetIndexer())
	ret.billingLister = corelisters.NewBillingLister(ret.billingInformer.GetIndexer())

	return ret
}

func (b *backendInformers) RunWithContext(ctx context.Context) {
	defer utilruntime.HandleCrash()
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting informers")
	defer logger.Info("stopped informers")

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.Subscription{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.subscriptionInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.OperationStatus{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.activeOperationInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.OperationStatus{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.allOperationInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.HCPOpenShiftCluster{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.clusterInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.HCPOpenShiftClusterNodePool{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.nodePoolInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.HCPOpenShiftClusterExternalAuth{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.externalAuthInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.ServiceProviderCluster{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.serviceProviderClusterInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.ServiceProviderNodePool{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.serviceProviderNodePoolInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.ServiceProviderExternalAuth{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.serviceProviderExternalAuthInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.Controller{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.controllerInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&fleetapi.ManagementCluster{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.managementClusterContentInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.SystemAdminCredentialRequest{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.systemAdminCredentialRequestInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.SystemAdminCredentialRevocation{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)

		b.systemAdminCredentialRevocationInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		b.billingInformer.RunWithContext(ctx)
	}()

	<-ctx.Done()
	wg.Wait()
}
