// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package manager

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestControllerRegistry(t *testing.T) {
	wantWorkers := map[string]int{
		"amwingestionscaling":                              1,
		"capacityreportingcontroller":                      1,
		"clustersserviceregistrationcontroller":            4,
		"ensurecapacityreaddesirecontroller":               1,
		"ensuresharedingressreaddesirecontroller":          1,
		"hcpresourcerequirementscontroller":                1,
		"managementclusterlifecyclecontroller":             1,
		"managementclusterscaleceilingreportingcontroller": 1,
		"maestroregistrationcontroller":                    4,
		"nodepoolcontroller":                               4,
		"sharedingressreportingcontroller":                 1,
		"union-kube-applier-informers-controller":          2,
	}
	registry := newControllerRegistry()
	gotWorkers := map[string]int{}
	for name, entry := range registry {
		gotWorkers[name] = entry.Workers
		require.NotNil(t, entry.Instantiate, "constructor for %s", name)
		if name != "nodepoolcontroller" {
			require.Nil(t, entry.Enabled, "%s must remain unconditionally registered", name)
		}
	}
	require.Equal(t, wantWorkers, gotWorkers, "preserve the complete Fleet launch inventory")

	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "without node pools", true: "with node pools"}[enabled], func(t *testing.T) {
			manager := &Manager{FleetDBClient: fleetcosmosstoragetesting.NewMockFleetDBClient()}
			if enabled {
				manager.NodePoolProfile = &compute.Profile{}
			}
			ctx := manager.newControllerContext(t.Context())
			require.Same(t, ctx.UnionKubeApplierInformersController.Union(), ctx.UnionKubeApplierInformers,
				"consumers must use the registered union controller's informers")
			controllers, err := instantiateControllers(registry, ctx)
			require.NoError(t, err)
			wantCount := len(wantWorkers)
			if !enabled {
				wantCount--
			}
			require.Len(t, controllers, wantCount)
			_, nodePoolsPresent := controllers["nodepoolcontroller"]
			require.Equal(t, enabled, nodePoolsPresent)
			require.Same(t, ctx.UnionKubeApplierInformersController, controllers["union-kube-applier-informers-controller"].runnable)
			for name, controller := range controllers {
				require.NotNil(t, controller.runnable, "instance for %s", name)
				require.Equal(t, wantWorkers[name], controller.workers, "workers for %s", name)
			}
		})
	}
}

func TestInstantiateControllers(t *testing.T) {
	t.Run("disabled constructor is not called", func(t *testing.T) {
		controllers, err := instantiateControllers(map[string]controllerconfig.ControllerRegistration{
			"disabled": {
				Enabled: func(controllerconfig.ControllerContext) bool { return false },
				Instantiate: func(controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
					t.Fatal("disabled controller was instantiated")
					return nil, nil
				},
			},
		}, controllerconfig.ControllerContext{})
		require.NoError(t, err)
		require.Empty(t, controllers)
	})
	t.Run("constructor failure identifies controller", func(t *testing.T) {
		failure := errors.New("constructor failed")
		controllers, err := instantiateControllers(map[string]controllerconfig.ControllerRegistration{
			"broken": {Instantiate: func(controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
				return nil, failure
			}},
		}, controllerconfig.ControllerContext{})
		require.Nil(t, controllers)
		require.ErrorIs(t, err, failure)
		require.ErrorContains(t, err, `controller "broken"`)
	})
}

func TestStartControllersWaitsForBothCaches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), testr.New(t)))
		defer cancel()
		informers := &startupInformers{}
		first, second := &recordingController{}, &recordingController{}
		go startControllers(ctx, map[string]instantiatedController{
			"first":  {runnable: first, workers: 4},
			"second": {runnable: second, workers: 2},
		}, informers)
		synctest.Wait()
		require.True(t, informers.started, "start informers before waiting for sync")
		require.Zero(t, first.calls.Load())
		require.Zero(t, second.calls.Load())

		informers.stamps.synced.Store(true)
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		require.Zero(t, first.calls.Load(), "stamp sync alone must not release controllers")
		require.Zero(t, second.calls.Load())

		informers.managementClusters.synced.Store(true)
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		require.Equal(t, int32(1), first.calls.Load())
		require.Equal(t, 4, first.workers)
		require.Equal(t, int32(1), second.calls.Load())
		require.Equal(t, 2, second.workers)
		require.Equal(t, ctx, first.ctx, "forward the leader-election context")
		cancel()
		synctest.Wait()
		require.True(t, first.stopped)
		require.True(t, second.stopped)
	})
}

func TestStartControllersAbortsWhenCacheSyncCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(utils.ContextWithLogger(t.Context(), testr.New(t)))
		defer cancel()
		informers := &startupInformers{}
		controller := &recordingController{}
		go startControllers(ctx, map[string]instantiatedController{
			"waiting": {runnable: controller, workers: 1},
		}, informers)
		synctest.Wait()
		cancel()
		synctest.Wait()
		require.True(t, informers.started)
		require.Zero(t, controller.calls.Load(), "unsynced caches must prevent controller startup")
	})
}

type recordingController struct {
	calls   atomic.Int32
	workers int
	ctx     context.Context
	stopped bool
}

func (c *recordingController) Run(ctx context.Context, workers int) {
	c.calls.Add(1)
	c.workers = workers
	c.ctx = ctx
	<-ctx.Done()
	c.stopped = true
}

type startupInformer struct {
	cache.SharedIndexInformer
	synced atomic.Bool
}

func (i *startupInformer) HasSynced() bool { return i.synced.Load() }

type startupInformers struct {
	fleetinformers.FleetInformers
	stamps, managementClusters startupInformer
	started                    bool
}

func (i *startupInformers) Stamps() (cache.SharedIndexInformer, fleetlisters.StampLister) {
	return &i.stamps, nil
}

func (i *startupInformers) ManagementClusters() (cache.SharedIndexInformer, fleetlisters.ManagementClusterLister) {
	return &i.managementClusters, nil
}

func (i *startupInformers) RunWithContext(ctx context.Context) {
	i.started = true
	<-ctx.Done()
}
