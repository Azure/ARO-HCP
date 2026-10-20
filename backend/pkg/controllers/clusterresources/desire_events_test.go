// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package clusterresources

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/kubeapplierinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	unioninformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type eventSource struct {
	cache.SharedIndexInformer
	handlers []cache.ResourceEventHandler
}

func (s *eventSource) HasSynced() bool { return true }
func (s *eventSource) AddEventHandlerWithOptions(h cache.ResourceEventHandler, _ cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	s.handlers = append(s.handlers, h)
	return eventRegistration{}, nil
}

type eventRegistration struct {
	cache.ResourceEventHandlerRegistration
}

func (eventRegistration) HasSynced() bool { return true }

type eventBackend struct {
	coreinformers.BackendInformers
	cluster, provider, nodes, content eventSource
}

func (b *eventBackend) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
	return &b.cluster, &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{newCluster()}}
}
func (b *eventBackend) ServiceProviderClusters() (cache.SharedIndexInformer, corelisters.ServiceProviderClusterLister) {
	return &b.provider, &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: []*coreapi.ServiceProviderCluster{newSPC(testManagementClusterResourceID)}}
}
func (b *eventBackend) NodePools() (cache.SharedIndexInformer, corelisters.NodePoolLister) {
	return &b.nodes, nil
}
func (b *eventBackend) ManagementClusterContents() (cache.SharedIndexInformer, corelisters.ManagementClusterContentLister) {
	return &b.content, nil
}

type eventDesires struct {
	kubeapplierinformers.KubeApplierInformers
	read, apply eventSource
}

func (d *eventDesires) ReadDesires() (cache.SharedIndexInformer, kubeapplierlisters.ReadDesireLister) {
	return &d.read, nil
}
func (d *eventDesires) ApplyDesires() (cache.SharedIndexInformer, kubeapplierlisters.ApplyDesireLister) {
	return &d.apply, nil
}

type countingResourcesClient struct {
	ocm.ClusterServiceClientSpec
	calls int
}

func (c *countingResourcesClient) GetClusterResources(context.Context, metadataapi.InternalID) (*arohcpv1alpha1.ClusterResources, error) {
	c.calls++
	return nil, nil
}

// This protects live Cluster Service traffic, rather than just asserting a
// watch inventory: events are delivered after each worker pass so workqueue
// coalescing cannot conceal an accidentally added desire subscription.
func TestDesireUpdatesDoNotRefetchLiveClusterResources(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := &eventBackend{}
		desires := &eventDesires{}
		union := unioninformers.NewUnionKubeApplierInformers()
		require.NoError(t, union.Add(testManagementClusterResourceID, desires))
		client := &countingResourcesClient{}
		controller := NewClusterResourcesController(corecosmosstoragetesting.NewMockResourcesDBClient(), nil, nil, backend, union, client)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go controller.Run(ctx, 1)
		for _, h := range backend.cluster.handlers {
			h.OnAdd(newCluster(), false)
		}
		synctest.Wait()
		require.Equal(t, 1, client.calls)
		for i := 0; i < 5; i++ {
			for kind, source := range map[string]*eventSource{"readDesires": &desires.read, "applyDesires": &desires.apply} {
				id := metadataapi.Must(azcorearm.ParseResourceID(newCluster().ResourceID.String() + "/" + kind + "/test"))
				old := &coreapi.CosmosMetadata{ResourceID: id}
				old.SetEtag("old")
				current := old.DeepCopy()
				current.SetEtag("new")
				for _, h := range source.handlers {
					h.OnUpdate(old, current)
				}
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			require.Equal(t, 1, client.calls, "desire updates must not fetch the live resource set")
		}
		// Primary resync remains able to fetch and repair desired resources.
		for _, h := range backend.cluster.handlers {
			h.OnUpdate(newCluster(), newCluster())
		}
		synctest.Wait()
		require.Equal(t, 2, client.calls)
		cancel()
		synctest.Wait()
	})
}
