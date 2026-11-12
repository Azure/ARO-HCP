// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package nodepool

import (
	"strings"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
)

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(NodePoolControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 4,
		Enabled: func(ctx controllerconfig.ControllerContext) bool { return ctx.NodePoolProfile != nil },
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			managementClusterInformer, managementClusterLister := ctx.FleetInformers.ManagementClusters()
			return NewNodePoolController(
				managementClusterInformer, managementClusterLister, ctx.FleetDBClient,
				*ctx.NodePoolProfile, ctx.NodePoolZones, ctx.Region, ctx.AzureCredential, ctx.AzureClientOptions,
			), nil
		},
	}
}
