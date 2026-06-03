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

package lifecycle

import (
	"context"
	"fmt"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type lifecycleSyncer struct {
	fleetDBClient database.FleetDBClient
}

func NewManagementClusterLifecycleController(
	managementClusterInformer cache.SharedIndexInformer,
	fleetDBClient database.FleetDBClient,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) *fleetcontrollers.StampWatchingController {
	syncer := &lifecycleSyncer{
		fleetDBClient: fleetDBClient,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		"ManagementClusterLifecycleController",
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(fleetcontrollers.DefaultInformerResyncPeriod, managementClusterInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *lifecycleSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	managementClusterCRUD := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier)
	managementCluster, err := managementClusterCRUD.Get(ctx, fleet.ManagementClusterResourceName)
	if err != nil {
		if database.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(err)
	}

	updated := managementCluster.DeepCopy()

	clustersServiceRegistrationCondition := apimeta.FindStatusCondition(updated.Status.Conditions, string(fleet.ManagementClusterConditionClustersServiceRegistered))
	maestroRegistrationCondition := apimeta.FindStatusCondition(updated.Status.Conditions, string(fleet.ManagementClusterConditionMaestroRegistered))

	if clustersServiceRegistrationCondition == nil || maestroRegistrationCondition == nil {
		var missing []string
		if clustersServiceRegistrationCondition == nil {
			missing = append(missing, string(fleet.ManagementClusterConditionClustersServiceRegistered))
		}
		if maestroRegistrationCondition == nil {
			missing = append(missing, string(fleet.ManagementClusterConditionMaestroRegistered))
		}
		logger := utils.LoggerFromContext(ctx)
		logger.Info("Skipping Ready aggregation: preserving current Ready value until all registration conditions are present",
			"missingConditions", missing,
		)
		return nil
	}

	if clustersServiceRegistrationCondition.Status == metav1.ConditionTrue && maestroRegistrationCondition.Status == metav1.ConditionTrue {
		apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
			Type:    string(fleet.ManagementClusterConditionReady),
			Status:  metav1.ConditionTrue,
			Reason:  string(fleet.ManagementClusterConditionReasonAllRegistered),
			Message: "All downstream registrations completed successfully",
		})
	} else {
		var notReady []string
		if clustersServiceRegistrationCondition.Status != metav1.ConditionTrue {
			notReady = append(notReady, string(fleet.ManagementClusterConditionClustersServiceRegistered))
		}
		if maestroRegistrationCondition.Status != metav1.ConditionTrue {
			notReady = append(notReady, string(fleet.ManagementClusterConditionMaestroRegistered))
		}
		apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
			Type:    string(fleet.ManagementClusterConditionReady),
			Status:  metav1.ConditionFalse,
			Reason:  string(fleet.ManagementClusterConditionReasonRegistrationIncomplete),
			Message: fmt.Sprintf("Pending downstream registrations: %s", strings.Join(notReady, ", ")),
		})
	}

	if controllerutils.NeedsUpdate(managementCluster, updated) {
		if _, err := managementClusterCRUD.Replace(ctx, updated, managementCluster, nil); err != nil {
			return utils.TrackError(err)
		}
	}

	return nil
}
