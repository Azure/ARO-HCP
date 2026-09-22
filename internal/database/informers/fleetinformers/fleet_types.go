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

package fleetinformers

import (
	"context"
	"sync"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// FleetInformers bundles one SharedIndexInformer per fleet type plus the
// matching fleetlisters. Both the fleet management binary and the backend
// construct one of these with the appropriate fleetcosmosstorage.FleetGlobalListers
// and fleetcosmosstorage.FleetDBClient — the factory does not care which.
type FleetInformers interface {
	Stamps() (cache.SharedIndexInformer, fleetlisters.StampLister)
	ManagementClusters() (cache.SharedIndexInformer, fleetlisters.ManagementClusterLister)
	ManagementClusterSchedulings() (cache.SharedIndexInformer, fleetlisters.ManagementClusterSchedulingLister)
	RunWithContext(ctx context.Context)
}

type fleetInformers struct {
	stampInformer                       cache.SharedIndexInformer
	stampLister                         fleetlisters.StampLister
	managementClusterInformer           cache.SharedIndexInformer
	managementClusterLister             fleetlisters.ManagementClusterLister
	managementClusterSchedulingInformer cache.SharedIndexInformer
	managementClusterSchedulingLister   fleetlisters.ManagementClusterSchedulingLister
}

func (f *fleetInformers) Stamps() (cache.SharedIndexInformer, fleetlisters.StampLister) {
	return f.stampInformer, f.stampLister
}

func (f *fleetInformers) ManagementClusters() (cache.SharedIndexInformer, fleetlisters.ManagementClusterLister) {
	return f.managementClusterInformer, f.managementClusterLister
}

func (f *fleetInformers) ManagementClusterSchedulings() (cache.SharedIndexInformer, fleetlisters.ManagementClusterSchedulingLister) {
	return f.managementClusterSchedulingInformer, f.managementClusterSchedulingLister
}

// NewFleetInformers creates FleetInformers with default relist durations.
func NewFleetInformers(ctx context.Context, globalListers fleetcosmosstorage.FleetGlobalListers, fleetDBClient fleetcosmosstorage.FleetDBClient) FleetInformers {
	ret := &fleetInformers{}
	ret.stampInformer = NewStampInformer(globalListers.Stamps(), fleetDBClient)
	ret.stampLister = fleetlisters.NewStampLister(ret.stampInformer.GetIndexer())
	ret.managementClusterInformer = NewManagementClusterInformer(globalListers.ManagementClusters(), fleetDBClient)
	ret.managementClusterLister = fleetlisters.NewManagementClusterLister(ret.managementClusterInformer.GetIndexer())
	ret.managementClusterSchedulingInformer = NewManagementClusterSchedulingInformer(globalListers.ManagementClusterSchedulings(), fleetDBClient)
	ret.managementClusterSchedulingLister = fleetlisters.NewManagementClusterSchedulingLister(ret.managementClusterSchedulingInformer.GetIndexer())

	return ret
}

func (f *fleetInformers) RunWithContext(ctx context.Context) {
	defer utilruntime.HandleCrash()
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting fleet informers")
	defer logger.Info("stopped fleet informers")

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		f.stampInformer.RunWithContext(ctx)
	}()

	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		f.managementClusterInformer.RunWithContext(ctx)
	}()

	wg.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer wg.Done()
		f.managementClusterSchedulingInformer.RunWithContext(ctx)
	}()

	<-ctx.Done()
	wg.Wait()
}
