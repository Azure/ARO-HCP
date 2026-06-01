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

package maestroregistration

import (
	"context"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"

	maestroopenapi "github.com/openshift-online/maestro/pkg/api/openapi"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type maestroRegistrationSyncer struct {
	fleetDBClient                database.FleetDBClient
	maestroConsumerClientFactory MaestroConsumerClientFactory
	stampLister                  listers.StampLister
}

func NewMaestroRegistrationController(
	managementClusterInformer cache.SharedIndexInformer,
	stampInformer cache.SharedIndexInformer,
	fleetDBClient database.FleetDBClient,
	maestroConsumerClientFactory MaestroConsumerClientFactory,
	stampLister listers.StampLister,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) *fleetcontrollers.StampWatchingController {
	syncer := &maestroRegistrationSyncer{
		fleetDBClient:                fleetDBClient,
		maestroConsumerClientFactory: maestroConsumerClientFactory,
		stampLister:                  stampLister,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		"MaestroRegistrationController",
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(fleetcontrollers.DefaultInformerResyncPeriod, managementClusterInformer, stampInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *maestroRegistrationSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	managementClusterCRUD := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier)
	managementCluster, err := managementClusterCRUD.Get(ctx, fleet.ManagementClusterResourceName)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(err)
	}

	stamp, err := s.stampLister.Get(ctx, key.StampIdentifier)
	if err != nil {
		return utils.TrackError(err)
	}

	updated := managementCluster.DeepCopy()

	var syncErr error
	if !apimeta.IsStatusConditionTrue(stamp.Status.Conditions, string(fleet.StampConditionApproved)) {
		// an unapproved stamp is not a sync error
		// the controller will wake up when the stamp is approved and try again
		// we update the condition though to reflect the fact
		fleetcontrollers.SetRegistrationCondition(&updated.Status.Conditions, string(fleet.ManagementClusterConditionMaestroRegistered), fleetcontrollers.ErrStampNotApproved)
	} else {
		client := s.maestroConsumerClientFactory.NewMaestroConsumerClient(updated.Status.MaestroRESTAPIURL)
		syncErr = s.ensureConsumer(ctx, client, updated.Status.MaestroConsumerName)
		fleetcontrollers.SetRegistrationCondition(&updated.Status.Conditions, string(fleet.ManagementClusterConditionMaestroRegistered), syncErr)
	}

	if controllerutils.NeedsUpdate(managementCluster, updated) {
		if _, writeErr := managementClusterCRUD.Replace(ctx, updated, managementCluster, nil); writeErr != nil {
			return utils.TrackError(writeErr)
		}
	}

	if syncErr != nil {
		return utils.TrackError(syncErr)
	}

	return nil
}

// ensureConsumer creates the Maestro consumer if it does not already exist.
func (s *maestroRegistrationSyncer) ensureConsumer(ctx context.Context, client MaestroConsumerClient, consumerName string) error {
	logger := utils.LoggerFromContext(ctx)

	consumer, err := client.GetConsumer(ctx, consumerName)
	if err != nil {
		return fmt.Errorf("getting consumer %q: %w", consumerName, err)
	}
	if consumer != nil {
		logger.Info("Maestro consumer already exists", "consumerName", consumerName)
		return nil
	}

	newConsumer := maestroopenapi.NewConsumer()
	newConsumer.SetName(consumerName)
	if _, err := client.CreateConsumer(ctx, *newConsumer); err != nil {
		return fmt.Errorf("creating consumer %q: %w", consumerName, err)
	}
	logger.Info("Maestro consumer created", "consumerName", consumerName)
	return nil
}
