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
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type FrontendInformers interface {
	Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister)
	NodePools() (cache.SharedIndexInformer, corelisters.NodePoolLister)
	ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister)
	ServiceProviderNodePools() (cache.SharedIndexInformer, corelisters.ServiceProviderNodePoolLister)

	HasSynced() bool
	RunWithContext(ctx context.Context)
}

type frontendInformers struct {
	clusterInformer cache.SharedIndexInformer
	clusterLister   corelisters.ClusterLister

	nodePoolInformer cache.SharedIndexInformer
	nodePoolLister   corelisters.NodePoolLister

	serviceProviderClusterInformer cache.SharedIndexInformer
	serviceProviderClusterLister   corelisters.ServiceProviderClusterLister

	serviceProviderNodePoolInformer cache.SharedIndexInformer
	serviceProviderNodePoolLister   corelisters.ServiceProviderNodePoolLister
}

func (f *frontendInformers) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
	return f.clusterInformer, f.clusterLister
}

func (f *frontendInformers) NodePools() (cache.SharedIndexInformer, corelisters.NodePoolLister) {
	return f.nodePoolInformer, f.nodePoolLister
}

func (f *frontendInformers) ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister) {
	return f.serviceProviderClusterInformer, f.serviceProviderClusterLister
}

func (f *frontendInformers) ServiceProviderNodePools() (cache.SharedIndexInformer, corelisters.ServiceProviderNodePoolLister) {
	return f.serviceProviderNodePoolInformer, f.serviceProviderNodePoolLister
}

func NewFrontendInformers(ctx context.Context, globalListers corecosmosstorage.ResourcesGlobalListers, resourcesDBClient corecosmosstorage.ResourcesDBClient) FrontendInformers {
	return NewFrontendInformersWithRelistDuration(ctx, globalListers, resourcesDBClient, nil)
}

func NewFrontendInformersWithRelistDuration(ctx context.Context, globalListers corecosmosstorage.ResourcesGlobalListers, resourcesDBClient corecosmosstorage.ResourcesDBClient, relistDuration *time.Duration) FrontendInformers {
	clusterRelistDuration := ClusterRelistDuration
	nodePoolRelistDuration := NodePoolRelistDuration
	serviceProviderClusterRelistDuration := ServiceProviderClusterRelistDuration
	serviceProviderNodePoolRelistDuration := ServiceProviderNodePoolRelistDuration
	if relistDuration != nil {
		clusterRelistDuration = *relistDuration
		nodePoolRelistDuration = *relistDuration
		serviceProviderClusterRelistDuration = *relistDuration
		serviceProviderNodePoolRelistDuration = *relistDuration
	}

	ret := &frontendInformers{}
	ret.clusterInformer = NewClusterInformerWithRelistDuration(globalListers.Clusters(), resourcesDBClient, clusterRelistDuration)
	ret.nodePoolInformer = NewNodePoolInformerWithRelistDuration(globalListers.NodePools(), resourcesDBClient, nodePoolRelistDuration)
	ret.serviceProviderClusterInformer = NewServiceProviderClusterInformerWithRelistDuration(globalListers.ServiceProviderClusters(), resourcesDBClient, serviceProviderClusterRelistDuration)
	ret.serviceProviderNodePoolInformer = NewServiceProviderNodePoolInformerWithRelistDuration(globalListers.ServiceProviderNodePools(), resourcesDBClient, serviceProviderNodePoolRelistDuration)

	ret.clusterLister = corelisters.NewClusterLister(ret.clusterInformer.GetIndexer())
	ret.nodePoolLister = corelisters.NewNodePoolLister(ret.nodePoolInformer.GetIndexer())
	ret.serviceProviderClusterLister = corelisters.NewServiceProviderClusterLister(ret.serviceProviderClusterInformer.GetIndexer())
	ret.serviceProviderNodePoolLister = corelisters.NewServiceProviderNodePoolLister(ret.serviceProviderNodePoolInformer.GetIndexer())

	return ret
}

func (f *frontendInformers) HasSynced() bool {
	return f.clusterInformer.HasSynced() &&
		f.nodePoolInformer.HasSynced() &&
		f.serviceProviderClusterInformer.HasSynced() &&
		f.serviceProviderNodePoolInformer.HasSynced()
}

func (f *frontendInformers) RunWithContext(ctx context.Context) {
	defer utilruntime.HandleCrash()
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting informers")
	defer logger.Info("stopped informers")

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.Cluster{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)
		f.clusterInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.NodePool{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)
		f.nodePoolInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.ServiceProviderCluster{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)
		f.serviceProviderClusterInformer.RunWithContext(localCtx)
	}()
	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		localLogger := logger.WithValues("type", reflect.TypeOf(&coreapi.ServiceProviderNodePool{}).String())
		localCtx := utils.ContextWithLogger(ctx, localLogger)
		f.serviceProviderNodePoolInformer.RunWithContext(localCtx)
	}()

	<-ctx.Done()
	wg.Wait()
}
