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

package informers

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// FleetInformers bundles one SharedIndexInformer per fleet type plus the
// matching listers. Both the fleet management binary (future) and the
// backend (cross-partition) construct one of these with the appropriate
// database.FleetGlobalListers — the factory does not care which.
type FleetInformers interface {
	Stamps() (cache.SharedIndexInformer, listers.StampLister)
	ManagementClusters() (cache.SharedIndexInformer, listers.ManagementClusterLister)
	RunWithContext(ctx context.Context)
}

type fleetInformers struct {
	stampInformer             cache.SharedIndexInformer
	stampLister               listers.StampLister
	managementClusterInformer cache.SharedIndexInformer
	managementClusterLister   listers.ManagementClusterLister
}

func (f *fleetInformers) Stamps() (cache.SharedIndexInformer, listers.StampLister) {
	return f.stampInformer, f.stampLister
}

func (f *fleetInformers) ManagementClusters() (cache.SharedIndexInformer, listers.ManagementClusterLister) {
	return f.managementClusterInformer, f.managementClusterLister
}

// NewFleetInformers creates FleetInformers with default relist durations.
func NewFleetInformers(ctx context.Context, gl database.FleetGlobalListers) FleetInformers {
	return NewFleetInformersWithRelistDuration(ctx, gl, nil)
}

// NewFleetInformersWithRelistDuration creates FleetInformers with a configurable relist duration.
func NewFleetInformersWithRelistDuration(ctx context.Context, gl database.FleetGlobalListers, relistDuration *time.Duration) FleetInformers {
	stampRelistDuration := StampRelistDuration
	managementClusterRelistDuration := ManagementClusterRelistDuration
	if relistDuration != nil {
		stampRelistDuration = *relistDuration
		managementClusterRelistDuration = *relistDuration
	}

	ret := &fleetInformers{}
	ret.stampInformer = NewStampInformerWithRelistDuration(gl.Stamps(), stampRelistDuration)
	ret.stampLister = listers.NewStampLister(ret.stampInformer.GetIndexer())
	ret.managementClusterInformer = NewManagementClusterInformerWithRelistDuration(gl.ManagementClusters(), managementClusterRelistDuration)
	ret.managementClusterLister = listers.NewManagementClusterLister(ret.managementClusterInformer.GetIndexer())

	return ret
}

func (f *fleetInformers) RunWithContext(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting fleet informers")
	defer logger.Info("stopped fleet informers")

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		f.stampInformer.RunWithContext(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		f.managementClusterInformer.RunWithContext(ctx)
	}()

	<-ctx.Done()
	wg.Wait()
}
