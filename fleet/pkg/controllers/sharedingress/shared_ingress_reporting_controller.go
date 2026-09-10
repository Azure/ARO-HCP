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

package sharedingress

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	fleetcontrollers "github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	SharedIngressReportingControllerName = "SharedIngressReportingController"

	sharedIngressReportingResyncPeriod = 10 * time.Minute
)

type sharedIngressReportingSyncer struct {
	fleetDBClient    fleetcosmosstorage.FleetDBClient
	readDesireLister kubeapplierlisters.ReadDesireLister
}

func NewSharedIngressReportingController(
	readDesireNotifier controllerutils.Notifier,
	managementClusterInformer cache.SharedIndexInformer,
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	cfg fleetcontrollers.StampWatchingControllerConfig,
) fleetcontrollers.Controller {
	syncer := &sharedIngressReportingSyncer{
		fleetDBClient:    fleetDBClient,
		readDesireLister: readDesireLister,
	}

	controller := fleetcontrollers.NewStampWatchingController(
		SharedIngressReportingControllerName,
		syncer,
		cfg,
	)

	if err := controller.QueueForInformers(sharedIngressReportingResyncPeriod, readDesireNotifier, managementClusterInformer); err != nil {
		panic(err) // coding error
	}

	return controller
}

func (s *sharedIngressReportingSyncer) SyncOnce(ctx context.Context, key fleetcontrollers.StampKey) error {
	logger := utils.LoggerFromContext(ctx)

	service, err := GetSharedIngressService(ctx, s.readDesireLister, key.StampIdentifier)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.V(1).Info("shared ingress ReadDesire not found, waiting for ensure controller")
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	managementClusterCRUD := s.fleetDBClient.Stamps().ManagementClusters(key.StampIdentifier)
	managementCluster, err := managementClusterCRUD.Get(ctx, fleetapi.ManagementClusterResourceName)
	if err != nil {
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil
		}
		return utils.TrackError(err)
	}

	updated := managementCluster.DeepCopy()

	if service == nil {
		// The ReadDesire exists but its content has not been mirrored yet.
		logger.V(1).Info("shared ingress service not mirrored yet, will retry on next change")
		updated.Status.SharedIngressIPAddresses = nil
		apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
			Type:    string(fleetapi.ManagementClusterConditionSharedIngressAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  string(fleetapi.ManagementClusterConditionReasonSharedIngressNotMirrored),
			Message: "shared ingress router Service has not been mirrored yet",
		})
	} else if ips := loadBalancerIPs(service); len(ips) > 0 {
		updated.Status.SharedIngressIPAddresses = ips
		apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
			Type:    string(fleetapi.ManagementClusterConditionSharedIngressAvailable),
			Status:  metav1.ConditionTrue,
			Reason:  string(fleetapi.ManagementClusterConditionReasonSharedIngressMirrored),
			Message: fmt.Sprintf("shared ingress is available with %d load balancer IP address(es)", len(ips)),
		})
	} else {
		updated.Status.SharedIngressIPAddresses = nil
		apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
			Type:    string(fleetapi.ManagementClusterConditionSharedIngressAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  string(fleetapi.ManagementClusterConditionReasonSharedIngressUnavailable),
			Message: "shared ingress router Service has no load balancer ingress IP addresses",
		})
	}

	if controllerutils.NeedsUpdate(managementCluster, updated) {
		if _, err := managementClusterCRUD.Replace(ctx, updated, managementCluster, nil); err != nil {
			if cosmosstorageutils.IsPreconditionFailedError(err) {
				// The ManagementCluster was updated concurrently; a newer
				// generation will re-trigger this sync, so treat as a no-op.
				return nil
			}
			return utils.TrackError(err)
		}
	}

	return nil
}

// loadBalancerIPs collects the non-empty load balancer ingress IP addresses
// from the shared-ingress router Service status, preserving order.
func loadBalancerIPs(service *corev1.Service) []string {
	var ips []string
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if ingress.IP == "" {
			continue
		}
		ips = append(ips, ingress.IP)
	}
	return ips
}
