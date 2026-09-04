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

package capacityreporting

import (
	"context"
	"time"

	"k8s.io/client-go/tools/cache"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	EnsureCapacityReadDesireControllerName = "EnsureCapacityReadDesireController"

	// ensureResyncPeriod controls the informer resync interval. The cooldown
	// (configured in manager.go) gates how often the controller actually
	// reconciles on resync events where the etag hasn't changed. In steady
	// state the controller runs at most once per cooldown period.
	ensureResyncPeriod = 10 * time.Minute
)

type ensureReadDesireSyncer struct {
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients
}

func NewEnsureCapacityReadDesireController(
	managementClusterInformer cache.SharedIndexInformer,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) fleetcontrollers.Controller {
	syncer := &ensureReadDesireSyncer{
		kubeApplierDBClients: kubeApplierDBClients,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		EnsureCapacityReadDesireControllerName,
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(ensureResyncPeriod, managementClusterInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *ensureReadDesireSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	logger := utils.LoggerFromContext(ctx)

	managementClusterResourceID := metadataapi.Must(fleetapi.ToManagementClusterResourceID(key.StampIdentifier))

	kubeApplierClient := s.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if kubeApplierClient == nil {
		logger.V(1).Info("kube-applier Cosmos client not available for management cluster")
		return nil
	}

	crud, err := kubeApplierClient.ReadDesiresForManagementCluster(key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	desireIDString := kubeapplierapi.ToManagementClusterScopedReadDesireResourceIDString(key.StampIdentifier, ReadDesireName)
	desired := controllerutil.BuildReadDesire(desireIDString, managementClusterResourceID, CapacityReportTarget)

	existing, err := crud.Get(ctx, ReadDesireName)
	if cosmosstorageutils.IsNotFoundError(err) {
		if _, err := crud.Create(ctx, desired, nil); err != nil {
			if cosmosstorageutils.IsConflictError(err) {
				return nil
			}
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if !controllerutil.ReadDesireNeedsWork(existing, desired) {
		return nil
	}

	replacement := existing.DeepCopy()
	replacement.Spec = *desired.Spec.DeepCopy()
	if _, err := crud.Replace(ctx, replacement, nil); err != nil {
		return utils.TrackError(err)
	}
	return nil
}
