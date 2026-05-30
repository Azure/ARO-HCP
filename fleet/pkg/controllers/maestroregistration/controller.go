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
	"errors"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	maestroopenapi "github.com/openshift-online/maestro/pkg/api/openapi"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var errStampNotApproved = errors.New("parent stamp is not approved")

const defaultInformerResyncPeriod = 5 * time.Minute

type maestroRegistrationSyncer struct {
	fleetDBClient         database.FleetDBClient
	maestroConsumerClient MaestroConsumerClient
	stampLister           listers.StampLister
}

func NewMaestroRegistrationController(
	managementClusterInformer cache.SharedIndexInformer,
	stampInformer cache.SharedIndexInformer,
	fleetDBClient database.FleetDBClient,
	maestroConsumerClient MaestroConsumerClient,
	stampLister listers.StampLister,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) *fleetcontrollers.StampWatchingController {
	syncer := &maestroRegistrationSyncer{
		fleetDBClient:         fleetDBClient,
		maestroConsumerClient: maestroConsumerClient,
		stampLister:           stampLister,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		"MaestroRegistrationController",
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(defaultInformerResyncPeriod, managementClusterInformer, stampInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *maestroRegistrationSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	logger := utils.LoggerFromContext(ctx)

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

	existing := managementCluster.DeepCopy()

	syncErr := s.reconcile(ctx, managementCluster, stamp)
	setMaestroRegisteredCondition(&managementCluster.Status.Conditions, syncErr)

	if controllerutils.NeedsUpdate(existing, managementCluster) {
		if _, writeErr := managementClusterCRUD.Replace(ctx, managementCluster, existing, nil); writeErr != nil {
			return utils.TrackError(writeErr)
		}
	}

	if syncErr != nil {
		if errors.Is(syncErr, errStampNotApproved) {
			return nil
		}
		return utils.TrackError(syncErr)
	}

	logger.Info("Maestro registration synced", "consumerName", managementCluster.Status.MaestroConsumerName)
	return nil
}

func (s *maestroRegistrationSyncer) reconcile(ctx context.Context, managementCluster *fleet.ManagementCluster, stamp *fleet.Stamp) error {
	if !apimeta.IsStatusConditionTrue(stamp.Status.Conditions, string(fleet.StampConditionApproved)) {
		return utils.TrackError(errStampNotApproved)
	}
	return s.ensureConsumer(ctx, managementCluster.Status.MaestroConsumerName)
}

func (s *maestroRegistrationSyncer) ensureConsumer(ctx context.Context, consumerName string) error {
	consumer, err := s.maestroConsumerClient.GetConsumer(ctx, consumerName)
	if err != nil {
		return err
	}
	if consumer != nil {
		return nil
	}

	newConsumer := maestroopenapi.NewConsumer()
	newConsumer.SetName(consumerName)
	_, err = s.maestroConsumerClient.CreateConsumer(ctx, *newConsumer)
	return err
}

func setMaestroRegisteredCondition(conditions *[]metav1.Condition, syncErr error) {
	if syncErr == nil {
		apimeta.SetStatusCondition(conditions, metav1.Condition{
			Type:               string(fleet.ManagementClusterConditionMaestroRegistered),
			Status:             metav1.ConditionTrue,
			Reason:             string(fleet.ManagementClusterConditionReasonRegistered),
			Message:            "Maestro consumer registered",
			LastTransitionTime: metav1.Now(),
		})
		return
	}

	reason := fleet.ManagementClusterConditionReasonRegistrationFailed
	if errors.Is(syncErr, errStampNotApproved) {
		reason = fleet.ManagementClusterConditionReasonStampNotApproved
	}

	apimeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               string(fleet.ManagementClusterConditionMaestroRegistered),
		Status:             metav1.ConditionFalse,
		Reason:             string(reason),
		Message:            syncErr.Error(),
		LastTransitionTime: metav1.Now(),
	})
}
