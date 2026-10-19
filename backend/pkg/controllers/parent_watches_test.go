// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllers_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	clustercreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/creation"
	nodepoolcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/creation"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

type parentSource struct {
	cache.SharedIndexInformer
	handlers []cache.ResourceEventHandler
}

func (s *parentSource) AddEventHandlerWithOptions(h cache.ResourceEventHandler, _ cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	s.handlers = append(s.handlers, h)
	return nil, nil
}
func (s *parentSource) HasSynced() bool { return true }

type parentTestInformers struct {
	coreinformers.BackendInformers
	subscriptions, clusters, nodePools *parentSource
	clusterLister                      corelisters.ClusterLister
	nodePoolLister                     corelisters.NodePoolLister
}

func (i *parentTestInformers) Subscriptions() (cache.SharedIndexInformer, corelisters.SubscriptionLister) {
	_, l := i.BackendInformers.Subscriptions()
	return i.subscriptions, l
}
func (i *parentTestInformers) Clusters() (cache.SharedIndexInformer, corelisters.ClusterLister) {
	return i.clusters, i.clusterLister
}
func (i *parentTestInformers) NodePools() (cache.SharedIndexInformer, corelisters.NodePoolLister) {
	return i.nodePools, i.nodePoolLister
}

type mappedClusterObserver struct {
	corelisters.ClusterLister
	observed chan string
}

func (l mappedClusterObserver) Get(_ context.Context, sub, rg, name string) (*coreapi.Cluster, error) {
	l.observed <- sub + "/" + rg + "/" + name
	return nil, cosmosstorageutils.NewNotFoundError()
}

type mappedNodePoolObserver struct {
	corelisters.NodePoolLister
	observed chan string
}

func (l mappedNodePoolObserver) Get(_ context.Context, sub, rg, cluster, name string) (*coreapi.NodePool, error) {
	l.observed <- sub + "/" + rg + "/" + cluster + "/" + name
	return nil, cosmosstorageutils.NewNotFoundError()
}

func TestRealControllersEnqueueCachedChildren(t *testing.T) {
	for _, kind := range []string{"subscription to cluster", "cluster to nodepool"} {
		for _, event := range []string{"add", "update", "resync", "delete", "tombstone"} {
			t.Run(kind+"/"+event, func(t *testing.T) {
				db := corecosmosstoragetesting.NewMockResourcesDBClient()
				real := coreinformers.NewBackendInformers(t.Context(), db.ResourcesGlobalListers(), db, billingcosmosstoragetesting.NewMockBillingDBClient().BillingGlobalListers())
				ci, cl := real.Clusters()
				ni, nl := real.NodePools()
				si, _ := real.Subscriptions()
				observed := make(chan string, 20)
				inf := &parentTestInformers{BackendInformers: real, subscriptions: &parentSource{SharedIndexInformer: si}, clusters: &parentSource{SharedIndexInformer: ci}, nodePools: &parentSource{SharedIndexInformer: ni}, clusterLister: mappedClusterObserver{cl, observed}, nodePoolLister: mappedNodePoolObserver{nl, observed}}
				const cluster = "/subscriptions/sub1/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"
				for _, id := range []string{cluster, "/subscriptions/sub2/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"} {
					rid := metadataapi.Must(azcorearm.ParseResourceID(id))
					c := &coreapi.Cluster{}
					c.ID = rid
					c.ResourceID = rid
					require.NoError(t, ci.GetIndexer().Add(c))
					for _, name := range []string{"a", "b"} {
						rid := metadataapi.Must(azcorearm.ParseResourceID(id + "/nodePools/" + name))
						np := &coreapi.NodePool{}
						np.ID = rid
						np.ResourceID = rid
						require.NoError(t, ni.GetIndexer().Add(np))
					}
				}
				var controller controllerutils.Controller
				var source, primary *parentSource
				var parentID, sentinelID string
				var want []string
				if kind == "subscription to cluster" {
					controller = clustercreation.NewClusterClusterServiceCreateController(db, nil, nil, inf, false)
					source = inf.subscriptions
					primary = inf.clusters
					parentID = "/subscriptions/sub1"
					sentinelID = cluster + "-sentinel"
					want = []string{"sub1/rg/cluster"}
				} else {
					controller = nodepoolcreation.NewNodePoolClusterServiceCreateController(db, nil, inf, nil)
					source = inf.clusters
					primary = inf.nodePools
					parentID = cluster
					sentinelID = cluster + "/nodePools/sentinel"
					want = []string{"sub1/rg/cluster/a", "sub1/rg/cluster/b"}
				}
				require.NotEmpty(t, source.handlers, "constructor must register parent watch")
				obj := &coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(parentID))}
				for _, h := range source.handlers {
					switch event {
					case "add":
						h.OnAdd(obj, false)
					case "update":
						h.OnUpdate(obj.DeepCopy(), obj)
					case "resync":
						h.OnUpdate(obj, obj)
					case "delete":
						h.OnDelete(obj)
					case "tombstone":
						h.OnDelete(cache.DeletedFinalStateUnknown{Obj: obj})
					}
				}
				for _, h := range primary.handlers {
					h.OnAdd(&coreapi.CosmosMetadata{ResourceID: metadataapi.Must(azcorearm.ParseResourceID(sentinelID))}, false)
				}
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() { defer close(done); controller.Run(ctx, 1) }()
				defer func() { cancel(); <-done }()
				var got []string
				timer := time.NewTimer(5 * time.Second)
				defer timer.Stop()
				for {
					select {
					case key := <-observed:
						if key == "sub1/rg/cluster-sentinel" || key == "sub1/rg/cluster/sentinel" {
							require.ElementsMatch(t, want, got, "map only cached children in the changed parent's scope")
							return
						}
						got = append(got, key)
					case <-timer.C:
						t.Fatalf("sentinel never reconciled: %v", got)
					}
				}
			})
		}
	}
}
