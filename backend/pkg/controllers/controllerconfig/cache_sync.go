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

package controllerconfig

import (
	"fmt"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
)

func WithCacheSyncs(instantiate func(ControllerContext) (Runnable, error), usesUnion bool) func(ControllerContext) (Runnable, error) {
	return func(context ControllerContext) (Runnable, error) {
		tracking := &cacheTracking{collecting: true}
		context.BackendInformers = &trackingBackendInformers{BackendInformers: context.BackendInformers, tracking: tracking}
		context.FleetInformers = &trackingFleetInformers{FleetInformers: context.FleetInformers, tracking: tracking}
		if usesUnion {
			tracking.add(context.UnionKubeApplierInformers.HasSynced)
		}
		runnable, err := instantiate(context)
		tracking.collecting = false
		if err != nil {
			return nil, err
		}
		controller, ok := runnable.(interface{ AddCacheSyncs(...cache.InformerSynced) })
		if !ok {
			return nil, fmt.Errorf("controller %T does not support cache synchronization", runnable)
		}
		controller.AddCacheSyncs(tracking.syncs...)
		return runnable, nil
	}
}

type cacheTracking struct {
	collecting bool
	syncs      []cache.InformerSynced
}

func (tracking *cacheTracking) add(synced cache.InformerSynced) {
	if tracking.collecting {
		tracking.syncs = append(tracking.syncs, synced)
	}
}

type trackingBackendInformers struct {
	coreinformers.BackendInformers
	tracking *cacheTracking
}

func (informers *trackingBackendInformers) Subscriptions() (cache.SharedIndexInformer, corelisters.SubscriptionLister) {
	informer, lister := informers.BackendInformers.Subscriptions()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) ActiveOperations() (cache.SharedIndexInformer, corelisters.ActiveOperationLister) {
	informer, lister := informers.BackendInformers.ActiveOperations()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) AllOperations() cache.SharedIndexInformer {
	informer := informers.BackendInformers.AllOperations()
	informers.tracking.add(informer.HasSynced)
	return informer
}

func (informers *trackingBackendInformers) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
	informer, lister := informers.BackendInformers.Clusters()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) NodePools() (cache.SharedIndexInformer, corelisters.NodePoolLister) {
	informer, lister := informers.BackendInformers.NodePools()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) ExternalAuths() (cache.SharedIndexInformer, corelisters.ExternalAuthLister) {
	informer, lister := informers.BackendInformers.ExternalAuths()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister) {
	informer, lister := informers.BackendInformers.ServiceProviderClusters()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) ServiceProviderNodePools() (cache.SharedIndexInformer, corelisters.ServiceProviderNodePoolLister) {
	informer, lister := informers.BackendInformers.ServiceProviderNodePools()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) Controllers() (cache.SharedIndexInformer, corelisters.ControllerLister) {
	informer, lister := informers.BackendInformers.Controllers()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) ManagementClusterContents() (cache.SharedIndexInformer, corelisters.ManagementClusterContentLister) {
	informer, lister := informers.BackendInformers.ManagementClusterContents()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) SystemAdminCredentialRequests() (cache.SharedIndexInformer, corelisters.SystemAdminCredentialRequestLister) {
	informer, lister := informers.BackendInformers.SystemAdminCredentialRequests()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) SystemAdminCredentialRevocations() (cache.SharedIndexInformer, corelisters.SystemAdminCredentialRevocationLister) {
	informer, lister := informers.BackendInformers.SystemAdminCredentialRevocations()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingBackendInformers) BillingDocs() (cache.SharedIndexInformer, corelisters.BillingLister) {
	informer, lister := informers.BackendInformers.BillingDocs()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

type trackingFleetInformers struct {
	fleetinformers.FleetInformers
	tracking *cacheTracking
}

func (informers *trackingFleetInformers) Stamps() (cache.SharedIndexInformer, fleetlisters.StampLister) {
	informer, lister := informers.FleetInformers.Stamps()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingFleetInformers) ManagementClusters() (cache.SharedIndexInformer, fleetlisters.ManagementClusterLister) {
	informer, lister := informers.FleetInformers.ManagementClusters()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}

func (informers *trackingFleetInformers) ManagementClusterSchedulings() (cache.SharedIndexInformer, fleetlisters.ManagementClusterSchedulingLister) {
	informer, lister := informers.FleetInformers.ManagementClusterSchedulings()
	informers.tracking.add(informer.HasSynced)
	return informer, lister
}
