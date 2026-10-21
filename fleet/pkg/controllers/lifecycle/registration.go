// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package lifecycle

import (
	"strings"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
)

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(ManagementClusterLifecycleControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 1,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			managementClusterInformer, managementClusterLister := ctx.FleetInformers.ManagementClusters()
			return NewManagementClusterLifecycleController(
				managementClusterInformer, ctx.FleetDBClient,
				base.StampWatchingControllerConfig{Cooldown: base.DefaultRegistrationAwareCooldown(managementClusterLister)},
			), nil
		},
	}
}
