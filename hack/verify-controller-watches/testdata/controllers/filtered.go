// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllers

import (
	"context"
	"time"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unioninformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

type filteredSyncer struct{ clusters corelisters.ClusterLister }

func (c *filteredSyncer) SyncOnce(ctx context.Context, key string) error {
	_, err := c.clusters.Get(ctx, "sub", "rg", key)
	return err
}
func (c *filteredSyncer) MakeKey(id *azcorearm.ResourceID) string          { return id.Name }
func (c *filteredSyncer) CooldownChecker() controllerutils.CooldownChecker { return nil }

// Register before configuring the filter, as the production wrapper does. All
// instances share informer sources but their interest sets must stay isolated.
func newFiltered(c controllerContext, configure, acceptCluster, customHandler bool) controllerregistry.Runnable {
	clusterInformer, clusters := c.Informers.Clusters()
	providerInformer, _ := c.Informers.ServiceProviderClusters()
	controller := controllerutils.NewGenericWatchingController("filtered", coreapi.ClusterResourceType, &filteredSyncer{clusters: clusters}, nil)
	if err := controller.QueueForInformers(time.Minute, clusterInformer, providerInformer); err != nil {
		panic(err)
	}
	if configure {
		if acceptCluster {
			// Exercise case normalization and construction of an equivalent ARM type.
			controller.AddInterestingResource(azcorearm.NewResourceType("MICROSOFT.REDHATOPENSHIFT", "HCPOPENSHIFTCLUSTERS"), "*")
		} else {
			controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, "default")
		}
		// Declaring interest cannot create a watch for this unsubscribed type.
		controller.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")
	}
	if customHandler {
		_, err := providerInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: func(any) { controller.Enqueue("cluster") }})
		if err != nil {
			panic(err)
		}
	}
	return controller
}

func registerFiltered(registry map[string]registration) {
	registry["filter-desires"] = registration{Instantiate: func(controllerContext) (controllerregistry.Runnable, error) { return newFilteredDesires(), nil }}
	registry["filter-unconfigured"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) {
		return newFiltered(c, false, false, false), nil
	}}
	registry["filter-cluster"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) {
		return newFiltered(c, true, true, false), nil
	}}
	registry["filter-provider"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) {
		return newFiltered(c, true, false, false), nil
	}}
	registry["filter-custom"] = registration{Instantiate: func(c controllerContext) (controllerregistry.Runnable, error) {
		return newFiltered(c, true, true, true), nil
	}}
}

// Apply/ReadDesire each have several scoped ARM types but one Go document type.
// Derive that mapping from the real typed lister declarations, including helper-
// constructed ResourceTypes, and count fixed-name interests at Go-type level.
type filteredDesires struct {
	*filteredSyncer
	reads kubeapplierlisters.ReadDesireLister
}

func (c *filteredDesires) SyncOnce(ctx context.Context, _ string) error {
	_, err := c.reads.List(ctx)
	return err
}
func newFilteredDesires() controllerregistry.Runnable {
	union := unioninformers.NewUnionKubeApplierInformers()
	readInformer, reads := union.ReadDesires()
	applyInformer, _ := union.ApplyDesires()
	c := controllerutils.NewGenericWatchingController("desire-filter", coreapi.ClusterResourceType, &filteredDesires{filteredSyncer: &filteredSyncer{}, reads: reads}, nil)
	if err := c.QueueForInformersWithMaxDepth(time.Minute, 1, readInformer, applyInformer); err != nil {
		panic(err)
	}
	c.AddInterestingResource(kubeapplierapi.ClusterScopedReadDesireResourceType, "hosted-cluster")
	return c
}
