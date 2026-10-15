// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package amwscaling

import (
	"strings"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
)

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(controllerName)] = controllerconfig.ControllerRegistration{
		Workers: 1,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			return controllerconfig.SingleWorker{Controller: NewController(
				ctx.AMWScalingPollInterval, ctx.AMWWorkspaceResourceIDs, ctx.AzureCredential, ctx.AzureClientOptions,
			)}, nil
		},
	}
}
