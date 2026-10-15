// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllers

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	backendutils "github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/hack/verify-controller-watches/testdata/dispatch"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

// Generic access, two transaction sites, a reused helper reached by paths of
// different lengths, and a direct Controller write. No watches are installed.
type operations struct {
	resources   corecosmosstorage.ResourcesDBClient
	transaction cosmosstorageutils.DBTransaction
}

func (c *operations) Run(ctx context.Context, _ int) {
	crud := c.resources.Subscriptions()
	longRead(ctx, crud)
	readDocument(ctx, crud)
	_, _ = crud.AddCreateToTransaction(ctx, c.transaction, &coreapi.Subscription{}, nil)
	_, _ = crud.AddReplaceToTransaction(ctx, c.transaction, &coreapi.Subscription{}, nil)
	_ = backendutils.WriteController(ctx, c.resources.HCPClusters("subscription", "rg").Controllers("cluster"), "fixture", func(string) *coreapi.Controller { return &coreapi.Controller{} })
}

func longRead(ctx context.Context, crud cosmosstorageutils.ResourceCRUD[coreapi.Subscription, *coreapi.Subscription]) {
	readDocument(ctx, crud)
}
func readDocument[T any, P coreapi.CosmosMetadataAccessorPtr[T]](ctx context.Context, crud cosmosstorageutils.ResourceCRUD[T, P]) {
	_, _ = crud.Get(ctx, "name")
}

// A concrete embedded method and an uncalled method with an unrelated access.
// The latter must not contaminate the controller's reachable inventory.
type embeddedWorker struct{ *subscriptionWorker }
type subscriptionWorker struct {
	resources corecosmosstorage.ResourcesDBClient
}

func (c *subscriptionWorker) Work(ctx context.Context) {
	readDocument(ctx, c.resources.Subscriptions())
}
func (c *subscriptionWorker) Unused(ctx context.Context) {
	_, _ = c.resources.ServiceProviderNodePools("sub", "rg", "cluster", "np").Get(ctx, "provider")
}

type externalAuthWorker struct {
	resources corecosmosstorage.ResourcesDBClient
}

func (c *externalAuthWorker) Work(ctx context.Context) {
	_, _ = c.resources.HCPClusters("sub", "rg").ExternalAuth("cluster").Get(ctx, "auth")
}

func chooseWorker(c controllerContext) dispatch.Worker {
	if c.Flag {
		return &embeddedWorker{&subscriptionWorker{resources: c.Resources}}
	}
	return &externalAuthWorker{resources: c.Resources}
}

// Unknown configuration preserves both concrete choices. Dynamic map keys
// preserve both callbacks; a real third-party wait helper invokes another one.
type callbacks struct {
	context controllerContext
	worker  dispatch.Worker
}

func (c *callbacks) Run(ctx context.Context, _ int) {
	_ = dispatch.Pure()
	dispatch.Forward(ctx, c.worker)
	jobs := map[string]func(){
		c.context.Key: func() { _, _ = c.context.Resources.HCPClusters("sub", "rg").Get(ctx, "cluster") },
		c.context.OtherKey: func() {
			_, _ = c.context.Resources.ServiceProviderClusters("sub", "rg", "cluster").Get(ctx, "provider")
		},
	}
	jobs[c.context.Key]()
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		_ = writeNodePool(ctx, c.context.Resources.HCPClusters("sub", "rg").NodePools("cluster"))
	}, time.Second)
}

// Indexer reads inherit the document type of the real informer. Merely handling
// event-routing metadata must not count as a read of another resource.
type indexed struct{ indexer cache.Indexer }

func newIndexed(c controllerContext) controllerregistry.Runnable {
	informer, _ := c.Informers.Clusters()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: func(obj any) { _ = obj.(coreapi.CosmosPersistable).GetCosmosData().GetResourceID() }})
	if err != nil {
		panic(err)
	}
	return &indexed{indexer: informer.GetIndexer()}
}
func (c *indexed) Run(_ context.Context, _ int) { _, _, _ = c.indexer.GetByKey("cluster") }

// Nil optional notifier branches must be pruned during construction, while the
// non-nil case records the optional watch. The wrapped method is promoted.
type promoted struct{ *wrapped }

func newOptional(c controllerContext, extra controllerutils.Notifier) controllerregistry.Runnable {
	_, provider := c.Informers.ServiceProviderClusters()
	watcher := controllerutils.NewGenericWatchingController("optional", coreapi.ClusterResourceType, &optionalSyncer{promoted: promoted{&wrapped{provider: provider}}}, nil)
	if extra != nil {
		if err := watcher.QueueForInformers(time.Minute, extra); err != nil {
			panic(err)
		}
	}
	return watcher
}

type optionalSyncer struct{ promoted }

func (c *optionalSyncer) MakeKey(id *azcorearm.ResourceID) backendutils.HCPClusterKey {
	return backendutils.HCPClusterKey{HCPClusterName: id.Name}
}
func (c *optionalSyncer) CooldownChecker() controllerutils.CooldownChecker { return nil }

// Same semantic inventory, but a different Run path and an additional access
// site. Selecting this variant must not churn the known-bad identity file.
type operationsMoreEvidence struct{ *operations }

func (c *operationsMoreEvidence) Run(ctx context.Context, workers int) {
	c.operations.Run(ctx, workers)
	_, _ = c.resources.Subscriptions().Get(ctx, "another-subscription")
}

// The incomplete fixture is separate so the supported-case suite still demands
// zero gaps. Untyped CRUD inference is intentionally deferred.
type incomplete struct {
	informer cache.SharedIndexInformer
	crud     cosmosstorageutils.UntypedResourceCRUD
}

func (c *incomplete) Run(ctx context.Context, _ int) {
	_, _ = c.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{})
	_, _ = c.crud.ListRecursive(ctx, nil)
}
