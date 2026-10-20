// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package controllers

import (
	"context"
	"time"

	fleetbase "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/hack/verify-controller-watches/testdata/dispatch"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
)

// Three candidates must retain all three dependencies, both as callbacks and as
// concrete implementations returned by a constructor. Each type is unique.
type threeCallbacks struct{ context controllerContext }

func (c *threeCallbacks) Run(ctx context.Context, _ int) {
	jobs := map[string]func(){
		"first":  func() { _, _ = c.context.Resources.Subscriptions().Get(ctx, "sub") },
		"second": func() { _, _ = c.context.Resources.HCPClusters("sub", "rg").Get(ctx, "cluster") },
		"third":  func() { _, _ = c.context.Resources.HCPClusters("sub", "rg").ExternalAuth("cluster").Get(ctx, "auth") },
	}
	jobs[c.context.Key]()
}

type clusterWorker struct{ context controllerContext }

func (c *clusterWorker) Work(ctx context.Context) {
	_, _ = c.context.Resources.HCPClusters("sub", "rg").Get(ctx, "cluster")
}

func chooseThree(c controllerContext) dispatch.Worker {
	if c.Flag {
		return &subscriptionWorker{resources: c.Resources}
	}
	if c.OtherFlag {
		return &externalAuthWorker{resources: c.Resources}
	}
	return &clusterWorker{context: c}
}

type threeImplementations struct{ worker dispatch.Worker }

func (c *threeImplementations) Run(ctx context.Context, _ int) { dispatch.Forward(ctx, c.worker) }

const selectedCallback = "selected"

// Constant identifiers are map values, not struct-field names. Keep the chosen
// entry second to catch accidental fallback to the first callback.
type constantKeys struct{ context controllerContext }

func (c *constantKeys) Run(ctx context.Context, _ int) {
	local := map[string]func(){
		"unselected":     func() { _, _ = c.context.Resources.HCPClusters("sub", "rg").Get(ctx, "cluster") },
		selectedCallback: func() { _, _ = c.context.Resources.Subscriptions().Get(ctx, "sub") },
	}
	local[selectedCallback]()
	qualified := map[string]func(){
		"unselected": func() {
			_, _ = c.context.Resources.ServiceProviderClusters("sub", "rg", "cluster").Get(ctx, "provider")
		},
		dispatch.SelectedCallback: func() { _, _ = c.context.Resources.HCPClusters("sub", "rg").ExternalAuth("cluster").Get(ctx, "auth") },
	}
	qualified[dispatch.SelectedCallback]()
}

type cooldownSyncer struct{ stamps fleetlisters.StampLister }

func (c *cooldownSyncer) SyncOnce(ctx context.Context, key fleetbase.StampKey) error {
	_, err := c.stamps.Get(ctx, key.StampIdentifier)
	return err
}

func newCooldownController(c controllerContext, watchManagementClusters bool) controllerregistry.Runnable {
	stampInformer, stampLister := c.FleetInformers.Stamps()
	mcInformer, mcLister := c.FleetInformers.ManagementClusters()
	controller := fleetbase.NewStampWatchingController("fixture-cooldown", &cooldownSyncer{stamps: stampLister},
		fleetbase.StampWatchingControllerConfig{Cooldown: fleetbase.DefaultRegistrationAwareCooldown(mcLister)})
	if err := controller.QueueForInformers(time.Minute, stampInformer); err != nil {
		panic(err)
	}
	if watchManagementClusters {
		if err := controller.QueueForInformers(time.Minute, mcInformer); err != nil {
			panic(err)
		}
	}
	return controller
}
