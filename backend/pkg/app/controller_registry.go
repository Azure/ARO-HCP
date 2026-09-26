// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package app

import (
	"context"
	"fmt"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/billing"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterresources"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerconfig"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cosmosmigration"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/datadump"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/metrics"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatch"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type Runnable = controllerconfig.Runnable
type ControllerRegistration = controllerconfig.ControllerRegistration

type instantiatedController struct {
	name     string
	runnable Runnable
	workers  int
}

func instantiateControllers(registry map[string]ControllerRegistration, controllerContext ControllerContext) (map[string]instantiatedController, error) {
	controllers := make(map[string]instantiatedController, len(registry))
	for name, entry := range registry {
		if entry.Enabled != nil && !entry.Enabled(controllerContext) {
			continue
		}
		runnable, err := entry.Instantiate(controllerContext)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to instantiate controller %q: %w", name, err))
		}
		controllers[name] = instantiatedController{name: name, runnable: runnable, workers: entry.Workers}
	}
	return controllers, nil
}

func newControllerRegistry() map[string]ControllerRegistration {
	registry := map[string]ControllerRegistration{}
	billing.Register(registry)
	cluster.Register(registry)
	clusterresources.Register(registry)
	cosmosmigration.Register(registry)
	datadump.Register(registry)
	externalauth.Register(registry)
	metrics.Register(registry)
	mismatch.Register(registry)
	nodepool.Register(registry)
	cachedreader.Register(registry, func(context ControllerContext) *cachedreader.FPAVirtualMachineResourceSKUsCachedReaderController {
		return context.VirtualMachineResourceSKUsCachedReaderController
	})
	unionkubeapplierinformers.Register(registry, func(context ControllerContext) *unionkubeapplierinformers.UnionKubeApplierInformersController {
		return context.UnionKubeApplierInformersController
	})
	return registry
}

type informerRunner struct {
	run func(context.Context)
}

func (runner informerRunner) Run(ctx context.Context, _ int) {
	defer utilruntime.HandleCrash()
	runner.run(ctx)
}

func startControllers(ctx context.Context, controllers map[string]instantiatedController, controllerContext ControllerContext) {
	runners := make(map[string]instantiatedController, len(controllers)+2)
	for name, controller := range controllers {
		runners[name] = controller
	}
	runners["backend-informers"] = instantiatedController{runnable: informerRunner{run: controllerContext.BackendInformers.RunWithContext}}
	runners["fleet-informers"] = instantiatedController{runnable: informerRunner{run: controllerContext.FleetInformers.RunWithContext}}
	for _, controller := range runners {
		go controller.runnable.Run(ctx, controller.workers)
	}
}
