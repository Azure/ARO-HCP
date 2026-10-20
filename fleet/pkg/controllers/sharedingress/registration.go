// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package sharedingress

import (
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
)

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(EnsureSharedIngressReadDesireControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 1,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			managementClusterInformer, _ := ctx.FleetInformers.ManagementClusters()
			readDesireInformer, readDesireLister := ctx.UnionKubeApplierInformers.ReadDesires()
			controller := NewEnsureSharedIngressReadDesireController(
				managementClusterInformer, ctx.KubeApplierDBClients, readDesireLister,
				base.StampWatchingControllerConfig{CooldownPeriod: 5 * time.Minute},
			)
			if err := controller.QueueForInformers(5*time.Minute, readDesireInformer); err != nil {
				return nil, err
			}
			return controller, nil
		},
	}
	registry[strings.ToLower(SharedIngressReportingControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 1,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			managementClusterInformer, managementClusterLister := ctx.FleetInformers.ManagementClusters()
			readDesireInformer, readDesireLister := ctx.UnionKubeApplierInformers.ReadDesires()
			return NewSharedIngressReportingController(
				readDesireInformer, managementClusterInformer, ctx.FleetDBClient, readDesireLister, managementClusterLister,
				base.StampWatchingControllerConfig{CooldownPeriod: 5 * time.Minute},
			), nil
		},
	}
}
