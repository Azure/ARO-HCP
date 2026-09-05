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

package datadump

import (
	"context"
	"time"

	"k8s.io/client-go/tools/cache"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const defaultResyncPeriod = 5 * time.Minute

type stampDataDumpSyncer struct {
	stampLister             fleetlisters.StampLister
	managementClusterLister fleetlisters.ManagementClusterLister
}

func NewStampDataDumpController(
	stampInformer cache.SharedIndexInformer,
	managementClusterInformer cache.SharedIndexInformer,
	stampLister fleetlisters.StampLister,
	managementClusterLister fleetlisters.ManagementClusterLister,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) fleetcontrollers.Controller {
	syncer := &stampDataDumpSyncer{
		stampLister:             stampLister,
		managementClusterLister: managementClusterLister,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		"StampDataDump",
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(defaultResyncPeriod, stampInformer, managementClusterInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *stampDataDumpSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	logger := utils.LoggerFromContext(ctx)

	stamp, err := s.stampLister.Get(ctx, key.StampIdentifier)
	if err != nil {
		logger.Error(err, "failed to get stamp from cache")
		return nil
	}

	logger.Info("dumping stamp",
		"snapshotType", "cosmos",
		"resourceID", stamp.CosmosMetadata.ResourceID,
		"objectMetadata", metadataapi.ObjectMetadataForResourceID("fleet", stamp.CosmosMetadata.ResourceID),
		"content", stamp,
	)

	managementCluster, err := s.managementClusterLister.Get(ctx, key.StampIdentifier)
	if err != nil {
		logger.Error(err, "failed to get management cluster from cache")
		return nil
	}

	logger.Info("dumping management cluster",
		"snapshotType", "cosmos",
		"resourceID", managementCluster.CosmosMetadata.ResourceID,
		"objectMetadata", metadataapi.ObjectMetadataForResourceID("fleet", managementCluster.CosmosMetadata.ResourceID),
		"content", managementCluster,
	)

	return nil
}
