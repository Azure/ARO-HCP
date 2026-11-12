// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License 2.0.

package hcpresourcerequirements

import (
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/controllerconfig"
)

func Register(registry map[string]controllerconfig.ControllerRegistration) {
	registry[strings.ToLower(HCPResourceRequirementsControllerName)] = controllerconfig.ControllerRegistration{
		Workers: 1,
		Instantiate: func(ctx controllerconfig.ControllerContext) (controllerconfig.Runnable, error) {
			_, stampLister := ctx.FleetInformers.Stamps()
			_, readDesireLister := ctx.UnionKubeApplierInformers.ReadDesires()
			return controllerconfig.SingleWorker{Controller: NewController(
				5*time.Minute, ctx.FleetDBClient, readDesireLister, stampLister,
			)}, nil
		},
	}
}
