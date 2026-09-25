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

package controllerconfig

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
)

type dependencyRecordingController struct {
	syncs []cache.InformerSynced
}

func (controller *dependencyRecordingController) Run(context.Context, int) {}
func (controller *dependencyRecordingController) AddCacheSyncs(syncs ...cache.InformerSynced) {
	controller.syncs = append(controller.syncs, syncs...)
}

func TestCacheTrackingPreservesInstancesAndIncludesEveryAccessor(t *testing.T) {
	resources := corecosmosstoragetesting.NewMockResourcesDBClient()
	billing := billingcosmosstoragetesting.NewMockBillingDBClient()
	fleet := fleetcosmosstoragetesting.NewMockFleetDBClient()
	controllerContext := ControllerContext{
		BackendInformers:          coreinformers.NewBackendInformers(t.Context(), resources.ResourcesGlobalListers(), resources, billing.BillingGlobalListers()),
		FleetInformers:            fleetinformers.NewFleetInformers(t.Context(), fleet.GlobalListers(), fleet),
		UnionKubeApplierInformers: unionkubeapplierinformers.NewUnionKubeApplierInformers(),
	}
	controller := &dependencyRecordingController{}
	var accessorCount int
	instantiate := WithCacheSyncs(func(tracked ControllerContext) (Runnable, error) {
		for _, pair := range [][2]any{
			{controllerContext.BackendInformers, tracked.BackendInformers},
			{controllerContext.FleetInformers, tracked.FleetInformers},
		} {
			original, wrapped := reflect.ValueOf(pair[0]), reflect.ValueOf(pair[1])
			for index := range original.NumMethod() {
				method := original.Type().Method(index)
				if method.Name == "RunWithContext" {
					continue
				}
				before := original.Method(index).Call(nil)
				after := wrapped.MethodByName(method.Name).Call(nil)
				require.Len(t, after, len(before), method.Name)
				for index := range before {
					require.Same(t, before[index].Interface(), after[index].Interface(), method.Name)
				}
				accessorCount++
			}
		}
		return controller, nil
	}, true)
	runnable, err := instantiate(controllerContext)
	require.NoError(t, err)
	require.Same(t, controller, runnable, "must not replace controller queueing machinery")
	require.Equal(t, 16, accessorCount)
	require.Len(t, controller.syncs, accessorCount+1, "includes lister-only dependencies and authoritative union readiness")
	for _, synced := range controller.syncs {
		require.False(t, synced(), "unstarted caches must block reconciliation")
	}
}
