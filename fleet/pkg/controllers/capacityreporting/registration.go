// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package capacityreporting

import (
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
)

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(EnsureCapacityReadDesireControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 1,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			managementClusterInformer, _ := ctx.FleetInformers.ManagementClusters()
			controller := NewEnsureCapacityReadDesireController(
				managementClusterInformer, ctx.KubeApplierDBClients,
				base.StampWatchingControllerConfig{CooldownPeriod: 5 * time.Minute},
			)
			readInformer, _ := ctx.UnionKubeApplierInformers.ReadDesires()
			if err := controller.QueueForInformers(5*time.Minute, readInformer); err != nil {
				return nil, err
			}
			return controller, nil
		},
	}
	registry[strings.ToLower(CapacityReportingControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 1,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			managementClusterInformer, _ := ctx.FleetInformers.ManagementClusters()
			readDesireInformer, readDesireLister := ctx.UnionKubeApplierInformers.ReadDesires()
			return NewCapacityReportingController(
				readDesireInformer, managementClusterInformer, ctx.FleetDBClient, readDesireLister,
				base.StampWatchingControllerConfig{CooldownPeriod: 5 * time.Minute},
			), nil
		},
	}
	registry[strings.ToLower(ManagementClusterScaleCeilingReportingControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 1,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			managementClusterInformer, _ := ctx.FleetInformers.ManagementClusters()
			readDesireInformer, readDesireLister := ctx.UnionKubeApplierInformers.ReadDesires()
			controller := NewManagementClusterScaleCeilingReportingController(
				managementClusterInformer, ctx.FleetDBClient, readDesireLister, ctx.Region,
				ctx.AzureCredential, ctx.AzureClientOptions,
				base.StampWatchingControllerConfig{CooldownPeriod: 10 * time.Minute},
			)
			if err := controller.QueueForInformers(5*time.Minute, readDesireInformer); err != nil {
				return nil, err
			}
			return controller, nil
		},
	}
}
