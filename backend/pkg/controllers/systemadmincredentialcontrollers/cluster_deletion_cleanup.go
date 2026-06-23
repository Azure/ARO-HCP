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

package systemadmincredentialcontrollers

import (
	"context"
	"fmt"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// SystemAdminCredentialContentDeletedConditionType is the
// ServiceProviderCluster.Status condition this controller flips to True
// once every SystemAdminCredential under the cluster has been removed.
// The cluster-deletion finalizer treats True as a precondition.
const SystemAdminCredentialContentDeletedConditionType = "SystemAdminCredentialContentDeleted"

// clusterDeletionCleanup is controller #6. It is the precondition gate
// for cluster deletion. Cleanup is delegated to the per-credential
// lifecycle:
//
//  1. Walk every SystemAdminCredential under the cluster, and stamp
//     `Spec.DeletionTimestamp` on any that lack it. That hands each
//     credential to `credentialDesiresCreator`'s teardown branch, which
//     drives the kube-applier desires to gone and then flips the
//     `DesiresCleanedUp=True` condition. The
//     `credentialDeletionFinalizer` deletes the credential document
//     once both conditions hold.
//  2. Once there are zero SystemAdminCredentials left under the
//     cluster, flip the SPC condition to True.
//
// No direct kube-applier interaction lives here anymore; this
// controller is now a stamper + a counter.
type clusterDeletionCleanup struct {
	clock                        utilsclock.PassiveClock
	cooldownChecker              controllerutil.CooldownChecker
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	resourcesDBClient            database.ResourcesDBClient
}

func NewClusterDeletionCleanupController(
	clock utilsclock.PassiveClock,
	clusterLister listers.ClusterLister,
	serviceProviderClusterLister listers.ServiceProviderClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &clusterDeletionCleanup{
		clock:                        clock,
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
	}
	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialClusterDeletionCleanup",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *clusterDeletionCleanup) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *clusterDeletionCleanup) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get cluster: %w", err))
	}

	// Gate: only run once the cluster is actively being deleted by the
	// existing finalizer pipeline.
	if cluster.ServiceProviderProperties.DeletionTimestamp == nil ||
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil {
		return nil
	}

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued
		// via the ServiceProviderCluster informer that ClusterWatchingController
		// already wires.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ServiceProviderCluster: %w", err))
	}
	if isServiceProviderClusterConditionTrue(serviceProviderCluster, SystemAdminCredentialContentDeletedConditionType) {
		return nil
	}

	credentialsCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminCredentials(key.HCPClusterName)
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list credentials: %w", err))
	}
	now := metav1.NewTime(c.clock.Now())
	remainingCredentials := 0
	for _, credential := range iter.Items(ctx) {
		if credential == nil {
			continue
		}
		remainingCredentials++
		if credential.Spec.DeletionTimestamp != nil {
			continue
		}
		// Stamp DeletionTimestamp; credentialDesiresCreator runs the
		// teardown, credentialDeletionFinalizer removes the doc.
		replacement := credential.DeepCopy()
		replacement.Spec.DeletionTimestamp = &now
		if _, err := credentialsCRUD.Replace(ctx, replacement, nil); database.IsPreconditionFailedError(err) {
			// Another writer beat us; the informer will re-enqueue us and
			// the next pass will see the up-to-date document.
			continue
		} else if err != nil {
			return utils.TrackError(fmt.Errorf("stamp DeletionTimestamp on credential %q: %w", credential.GetResourceID().Name, err))
		}
		logger.Info("cluster-deletion initiated credential deletion", "credential", credential.GetResourceID().Name)
	}
	if err := iter.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("iterate credentials: %w", err))
	}

	if remainingCredentials > 0 {
		// Still waiting on the per-credential teardown + finalizer to
		// drop the docs.
		return nil
	}

	logger.Info("SystemAdminCredential cleanup complete; clearing finalizer precondition")
	return c.markDone(ctx, serviceProviderCluster)
}

func (c *clusterDeletionCleanup) markDone(ctx context.Context, serviceProviderCluster *api.ServiceProviderCluster) error {
	// Lister hands back a shared pointer; DeepCopy before mutating so we
	// don't poison the cache for other consumers.
	replacement := serviceProviderCluster.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
		Type:    SystemAdminCredentialContentDeletedConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  "AllCredentialsRemoved",
		Message: "Every SystemAdminCredential under this cluster has been deleted.",
	})
	clusterRID := replacement.GetResourceID().Parent
	if _, err := c.resourcesDBClient.ServiceProviderClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name).Replace(ctx, replacement, nil); database.IsPreconditionFailedError(err) {
		// Another writer beat us to the ServiceProviderCluster; the informer
		// will deliver the updated document and re-enqueue us automatically.
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("replace ServiceProviderCluster: %w", err))
	}
	return nil
}

func isServiceProviderClusterConditionTrue(serviceProviderCluster *api.ServiceProviderCluster, conditionType string) bool {
	cond := apimeta.FindStatusCondition(serviceProviderCluster.Status.Conditions, conditionType)
	if cond == nil {
		return false
	}
	return cond.Status == metav1.ConditionTrue
}
