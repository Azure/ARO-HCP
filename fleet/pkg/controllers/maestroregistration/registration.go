// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package maestroregistration

import (
	"strings"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
)

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(MaestroRegistrationControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 4,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			stampInformer, stampLister := ctx.FleetInformers.Stamps()
			managementClusterInformer, managementClusterLister := ctx.FleetInformers.ManagementClusters()
			return NewMaestroRegistrationController(
				managementClusterInformer, stampInformer, ctx.FleetDBClient, ctx.MaestroConsumerClientFactory,
				stampLister,
				base.StampWatchingControllerConfig{Cooldown: base.DefaultRegistrationAwareCooldown(managementClusterLister)},
			), nil
		},
	}
}
