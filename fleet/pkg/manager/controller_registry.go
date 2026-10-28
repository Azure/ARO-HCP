// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package manager

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/amwscaling"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/capacityreporting"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/clustersserviceregistration"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/hcpresourcerequirements"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/lifecycle"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/maestroregistration"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/nodepool"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/sharedingress"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type instantiatedController struct {
	runnable controllerconfig.Runnable
	workers  int
}

func newControllerRegistry() map[string]controllerconfig.ControllerRegistration {
	registry := map[string]controllerconfig.ControllerRegistration{}
	amwscaling.Register(registry)
	capacityreporting.Register(registry)
	clustersserviceregistration.Register(registry)
	hcpresourcerequirements.Register(registry)
	lifecycle.Register(registry)
	maestroregistration.Register(registry)
	nodepool.Register(registry)
	sharedingress.Register(registry)
	unionkubeapplierinformers.Register(registry, func(ctx controllerconfig.ControllerContext) *unionkubeapplierinformers.UnionKubeApplierInformersController {
		return ctx.UnionKubeApplierInformersController
	})
	// Fleet has historically used two workers for management-cluster discovery.
	unionName := strings.ToLower(unionkubeapplierinformers.ControllerName)
	unionRegistration := registry[unionName]
	unionRegistration.Workers = 2
	registry[unionName] = unionRegistration
	return registry
}

func (m *Manager) newControllerContext(ctx context.Context) controllerconfig.ControllerContext {
	fleetInformers := fleetinformers.NewFleetInformers(ctx, m.FleetDBClient.GlobalListers(), m.FleetDBClient)
	managementClusterInformer, managementClusterLister := fleetInformers.ManagementClusters()
	unionController := unionkubeapplierinformers.NewUnionKubeApplierInformersController(
		managementClusterInformer, managementClusterLister,
		unionkubeapplierinformers.NewKubeApplierInformerFactory(m.KubeApplierDBClients, nil),
	)
	return controllerconfig.ControllerContext{
		FleetDBClient:                       m.FleetDBClient,
		ClustersServiceClient:               m.ClustersServiceClient,
		MaestroConsumerClientFactory:        m.MaestroConsumerClientFactory,
		KubeApplierDBClients:                m.KubeApplierDBClients,
		Region:                              m.Region,
		AzureCredential:                     m.AzureCredential,
		AzureClientOptions:                  m.AzureClientOptions,
		NodePoolProfile:                     m.NodePoolProfile,
		NodePoolZones:                       m.NodePoolZones,
		AMWWorkspaceResourceIDs:             m.AMWWorkspaceResourceIDs,
		AMWScalingPollInterval:              m.AMWScalingPollInterval,
		FleetInformers:                      fleetInformers,
		UnionKubeApplierInformers:           unionController.Union(),
		UnionKubeApplierInformersController: unionController,
	}
}

func instantiateControllers(registry map[string]controllerconfig.ControllerRegistration, controllerContext controllerconfig.ControllerContext) (map[string]instantiatedController, error) {
	controllers := make(map[string]instantiatedController, len(registry))
	for name, entry := range registry {
		if entry.Enabled != nil && !entry.Enabled(controllerContext) {
			continue
		}
		runnable, err := entry.Instantiate(controllerContext)
		if err != nil {
			return nil, fmt.Errorf("failed to instantiate controller %q: %w", name, err)
		}
		controllers[name] = instantiatedController{runnable: runnable, workers: entry.Workers}
	}
	return controllers, nil
}

// startControllers retains Fleet's startup barrier: the stamp and management
// cluster caches must sync before any registered controller starts.
func startControllers(ctx context.Context, controllers map[string]instantiatedController, informers fleetinformers.FleetInformers) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("acquired leader election lease; starting informers")
	go informers.RunWithContext(ctx)
	stampInformer, _ := informers.Stamps()
	managementClusterInformer, _ := informers.ManagementClusters()
	if !cache.WaitForCacheSync(ctx.Done(), stampInformer.HasSynced, managementClusterInformer.HasSynced) {
		logger.Info("informer caches did not sync; aborting controller startup")
		return
	}
	logger.Info("informer caches synced; starting controllers")
	for _, controller := range controllers {
		go controller.runnable.Run(ctx, controller.workers)
	}
}
