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

package subscription

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// cosmosMigrationController performs a read/write cycle on every document in the
// Resources container to force Cosmos DB re-serialization. This is a subscription-
// watching controller that processes each subscription exactly once per process
// lifetime.
type cosmosMigrationController struct {
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients

	// completedSubscriptions tracks subscription IDs that have been fully
	// migrated. Each subscription is processed at most once per process lifetime.
	completedSubscriptions sync.Map

	cooldown controllerutil.CooldownChecker
}

// NewCosmosMigrationController creates a subscription-watching controller that
// iterates depth-first through every named document type in the Resources container
// and the per-management-cluster kube-applier containers, performing a read/write
// cycle to force Cosmos DB re-serialization.
func NewCosmosMigrationController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	resyncDuration time.Duration,
) controllerutils.Controller {
	syncer := &cosmosMigrationController{
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
		cooldown:             controllerutil.NewTimeBasedCooldownChecker(resyncDuration),
	}
	return controllerutils.NewSubscriptionWatchingController(
		"CosmosMigration",
		backendInformers,
		resyncDuration,
		syncer,
	)
}

func (c *cosmosMigrationController) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldown
}

// MigrateAllSubscriptionsOrDie runs the Cosmos migration once across every
// subscription currently in the Resources container and panics on the first
// unrecoverable error. It is intended for integration-test setup where the
// long-running subscription-watching controller is not wired in; production
// migration runs as the controller returned by NewCosmosMigrationController.
func MigrateAllSubscriptionsOrDie(ctx context.Context, resourcesDBClient database.ResourcesDBClient, kubeApplierDBClients database.KubeApplierDBClients) {
	logger := utils.LoggerFromContext(ctx)
	c := &cosmosMigrationController{
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}
	subscriptionIterator, err := resourcesDBClient.Subscriptions().List(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to list subscriptions")
		panic(err)
	}
	for _, subscription := range subscriptionIterator.Items(ctx) {
		if err := c.migrateSubscription(ctx, logger, subscription.ResourceID.Name); err != nil {
			logger.Error(err, "cosmos migration failed", "subscription", subscription.ResourceID)
			panic(err)
		}
	}
	if err := subscriptionIterator.GetError(); err != nil {
		logger.Error(err, "failed to iterate subscriptions")
		panic(err)
	}
}

func (c *cosmosMigrationController) SyncOnce(ctx context.Context, key controllerutils.SubscriptionKey) error {
	// Only process each subscription once per process lifetime.
	// The workqueue guarantees that a given subscription key is processed by at
	// most one goroutine at a time, so the separate Load → Store sequence below
	// does not need to be atomic (no concurrent writer for the same key).
	if _, alreadyDone := c.completedSubscriptions.Load(key.SubscriptionID); alreadyDone {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("subscriptionID", key.SubscriptionID)
	ctx = logr.NewContext(ctx, logger)
	logger.Info("starting cosmos migration for subscription")

	err := c.migrateSubscription(ctx, logger, key.SubscriptionID)
	if err != nil {
		logger.Error(err, "cosmos migration encountered errors for subscription")
		return err
	}

	c.completedSubscriptions.Store(key.SubscriptionID, struct{}{})
	logger.Info("cosmos migration completed successfully for subscription")
	return nil
}

// migrateSubscription performs a depth-first read/write migration of every document
// type in the Resources container for the given subscription.
func (c *cosmosMigrationController) migrateSubscription(ctx context.Context, logger logr.Logger, subscriptionID string) error {
	var migrationErrors []error

	// 1. Migrate the subscription document itself.
	if err := c.migrateSubscriptionDoc(ctx, logger, subscriptionID); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("subscription doc: %w", err))
	}

	// 2. Migrate operations under the subscription.
	if err := c.migrateOperations(ctx, logger, subscriptionID); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("operations: %w", err))
	}

	// 3. Migrate clusters and all nested resources.
	if err := c.migrateClusters(ctx, logger, subscriptionID); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("clusters: %w", err))
	}

	return errors.Join(migrationErrors...)
}

// migrateSubscriptionDoc reads and writes back the subscription document.
func (c *cosmosMigrationController) migrateSubscriptionDoc(ctx context.Context, logger logr.Logger, subscriptionID string) error {
	err := replaceWithRetry(ctx, logger, c.resourcesDBClient.Subscriptions(), subscriptionID, "subscription")
	if err != nil {
		return err
	}
	logger.Info("migrated subscription document")
	return nil
}

// migrateOperations reads and writes back all operation documents under the subscription.
func (c *cosmosMigrationController) migrateOperations(ctx context.Context, logger logr.Logger, subscriptionID string) error {
	operationCRUD := c.resourcesDBClient.Operations(subscriptionID)
	operationIterator, err := operationCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list operations: %w", err)
	}

	var migrationErrors []error
	count := 0
	for _, operation := range operationIterator.Items(ctx) {
		count++
		if err := replaceWithRetry(ctx, logger, operationCRUD, operation.ResourceID.Name, "operation"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}
	}
	if err := operationIterator.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate operations: %w", err))
	}
	logger.Info("migrated operations", "count", count)
	return errors.Join(migrationErrors...)
}

// migrateClusters lists all HCP clusters in the subscription and migrates each
// cluster along with its nested resources.
func (c *cosmosMigrationController) migrateClusters(ctx context.Context, logger logr.Logger, subscriptionID string) error {
	clusterIterator, err := c.resourcesDBClient.HCPClusters(subscriptionID, "").List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	var migrationErrors []error
	clusterCount := 0
	for _, cluster := range clusterIterator.Items(ctx) {
		clusterCount++
		clusterSubID := cluster.ID.SubscriptionID
		clusterRG := cluster.ID.ResourceGroupName
		clusterName := cluster.ID.Name
		clusterLogger := logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(cluster.ID)...)
		clusterCtx := logr.NewContext(ctx, clusterLogger)

		// Migrate the cluster document itself.
		clusterCRUD := c.resourcesDBClient.HCPClusters(clusterSubID, clusterRG)
		if err := replaceWithRetry(clusterCtx, clusterLogger, clusterCRUD, clusterName, "cluster"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate cluster-scoped controllers.
		if err := migrateControllers(clusterCtx, clusterLogger, clusterCRUD.Controllers(clusterName), "cluster controller"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate cluster-scoped management cluster contents.
		if err := migrateManagementClusterContents(clusterCtx, clusterLogger, clusterCRUD.ManagementClusterContents(clusterName), "cluster management cluster content"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate service provider clusters.
		if err := c.migrateServiceProviderClusters(clusterCtx, clusterLogger, clusterSubID, clusterRG, clusterName); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Resolve the kube-applier client for this cluster's management cluster.
		// Returns nil if no ServiceProviderCluster exists yet (e.g. placement is
		// pending) or the management cluster is unknown.
		kubeApplierClient := c.resolveKubeApplierClient(clusterCtx, clusterLogger, clusterSubID, clusterRG, clusterName)

		// Migrate node pools and their nested resources (including kube-applier desires).
		if err := c.migrateNodePools(clusterCtx, clusterLogger, clusterSubID, clusterRG, clusterName, kubeApplierClient); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate external auths and their nested resources.
		if err := c.migrateExternalAuths(clusterCtx, clusterLogger, clusterSubID, clusterRG, clusterName); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate cluster-scoped kube-applier desires.
		if kubeApplierClient != nil {
			if err := migrateClusterDesires(clusterCtx, clusterLogger, kubeApplierClient, clusterSubID, clusterRG, clusterName); err != nil {
				migrationErrors = append(migrationErrors, fmt.Errorf("kube-applier cluster desires: %w", err))
			}
		}
	}
	if err := clusterIterator.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate clusters: %w", err))
	}
	logger.Info("migrated clusters", "count", clusterCount)
	return errors.Join(migrationErrors...)
}

// migrateServiceProviderClusters migrates service provider cluster documents under a cluster.
func (c *cosmosMigrationController) migrateServiceProviderClusters(ctx context.Context, logger logr.Logger, subscriptionID, resourceGroupName, clusterName string) error {
	serviceProviderClusterCRUD := c.resourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName)
	serviceProviderClusterIterator, err := serviceProviderClusterCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list service provider clusters: %w", err)
	}

	var migrationErrors []error
	count := 0
	for _, serviceProviderCluster := range serviceProviderClusterIterator.Items(ctx) {
		count++
		if err := replaceWithRetry(ctx, logger, serviceProviderClusterCRUD, serviceProviderCluster.ResourceID.Name, "service provider cluster"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}
	}
	if err := serviceProviderClusterIterator.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate service provider clusters: %w", err))
	}
	if count > 0 {
		logger.Info("migrated service provider clusters", "count", count)
	}
	return errors.Join(migrationErrors...)
}

// migrateNodePools migrates node pool documents and their nested resources under a cluster.
func (c *cosmosMigrationController) migrateNodePools(ctx context.Context, logger logr.Logger, subscriptionID, resourceGroupName, clusterName string, kubeApplierClient database.KubeApplierDBClient) error {
	nodePoolListCRUD := c.resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName)
	nodePoolIterator, err := nodePoolListCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list node pools: %w", err)
	}

	var migrationErrors []error
	count := 0
	for _, nodePool := range nodePoolIterator.Items(ctx) {
		count++
		nodePoolSubID := nodePool.ID.SubscriptionID
		nodePoolRG := nodePool.ID.ResourceGroupName
		nodePoolParentCluster := nodePool.ID.Parent.Name
		nodePoolName := nodePool.ID.Name
		nodePoolLogger := logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(nodePool.ID)...)
		nodePoolCtx := logr.NewContext(ctx, nodePoolLogger)

		// Migrate the node pool document itself.
		nodePoolItemCRUD := c.resourcesDBClient.HCPClusters(nodePoolSubID, nodePoolRG).NodePools(nodePoolParentCluster)
		if err := replaceWithRetry(nodePoolCtx, nodePoolLogger, nodePoolItemCRUD, nodePoolName, "node pool"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate node-pool-scoped controllers.
		if err := migrateControllers(nodePoolCtx, nodePoolLogger, nodePoolItemCRUD.Controllers(nodePoolName), "node pool controller"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate node-pool-scoped management cluster contents.
		if err := migrateManagementClusterContents(nodePoolCtx, nodePoolLogger, nodePoolItemCRUD.ManagementClusterContents(nodePoolName), "node pool management cluster content"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate service provider node pools.
		if err := c.migrateServiceProviderNodePools(nodePoolCtx, nodePoolLogger, nodePoolSubID, nodePoolRG, nodePoolParentCluster, nodePoolName); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate node-pool-scoped kube-applier desires.
		if kubeApplierClient != nil {
			if err := migrateNodePoolDesires(nodePoolCtx, nodePoolLogger, kubeApplierClient, nodePoolSubID, nodePoolRG, nodePoolParentCluster, nodePoolName); err != nil {
				migrationErrors = append(migrationErrors, fmt.Errorf("kube-applier node pool %q desires: %w", nodePoolName, err))
			}
		}
	}
	if err := nodePoolIterator.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate node pools: %w", err))
	}
	if count > 0 {
		logger.Info("migrated node pools", "count", count)
	}
	return errors.Join(migrationErrors...)
}

// migrateServiceProviderNodePools migrates service provider node pool documents under a node pool.
func (c *cosmosMigrationController) migrateServiceProviderNodePools(ctx context.Context, logger logr.Logger, subscriptionID, resourceGroupName, clusterName, nodePoolName string) error {
	serviceProviderNodePoolCRUD := c.resourcesDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	serviceProviderNodePoolIterator, err := serviceProviderNodePoolCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list service provider node pools: %w", err)
	}

	var migrationErrors []error
	count := 0
	for _, serviceProviderNodePool := range serviceProviderNodePoolIterator.Items(ctx) {
		count++
		if err := replaceWithRetry(ctx, logger, serviceProviderNodePoolCRUD, serviceProviderNodePool.ResourceID.Name, "service provider node pool"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}
	}
	if err := serviceProviderNodePoolIterator.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate service provider node pools: %w", err))
	}
	if count > 0 {
		logger.Info("migrated service provider node pools", "count", count)
	}
	return errors.Join(migrationErrors...)
}

// migrateExternalAuths migrates external auth documents and their nested resources under a cluster.
func (c *cosmosMigrationController) migrateExternalAuths(ctx context.Context, logger logr.Logger, subscriptionID, resourceGroupName, clusterName string) error {
	externalAuthListCRUD := c.resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName)
	externalAuthIterator, err := externalAuthListCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list external auths: %w", err)
	}

	var migrationErrors []error
	count := 0
	for _, externalAuth := range externalAuthIterator.Items(ctx) {
		count++
		externalAuthSubID := externalAuth.ID.SubscriptionID
		externalAuthRG := externalAuth.ID.ResourceGroupName
		externalAuthParentCluster := externalAuth.ID.Parent.Name
		externalAuthName := externalAuth.ID.Name
		externalAuthLogger := logger.WithValues(utils.LogValues{}.AddLogValuesForResourceID(externalAuth.ID)...)
		externalAuthCtx := logr.NewContext(ctx, externalAuthLogger)

		// Migrate the external auth document itself.
		externalAuthItemCRUD := c.resourcesDBClient.HCPClusters(externalAuthSubID, externalAuthRG).ExternalAuth(externalAuthParentCluster)
		if err := replaceWithRetry(externalAuthCtx, externalAuthLogger, externalAuthItemCRUD, externalAuthName, "external auth"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}

		// Migrate external-auth-scoped controllers.
		if err := migrateControllers(externalAuthCtx, externalAuthLogger, externalAuthItemCRUD.Controllers(externalAuthName), "external auth controller"); err != nil {
			migrationErrors = append(migrationErrors, err)
		}
	}
	if err := externalAuthIterator.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate external auths: %w", err))
	}
	if count > 0 {
		logger.Info("migrated external auths", "count", count)
	}
	return errors.Join(migrationErrors...)
}

// resolveKubeApplierClient looks up the ServiceProviderCluster "default" for the
// given HCP cluster and returns the corresponding kube-applier DB client.
// Returns nil if no ServiceProviderCluster exists, the management cluster is not
// yet resolved, or the kube-applier client is unavailable.
func (c *cosmosMigrationController) resolveKubeApplierClient(ctx context.Context, logger logr.Logger, subscriptionID, resourceGroupName, clusterName string) database.KubeApplierDBClient {
	serviceProviderCluster, err := c.resourcesDBClient.ServiceProviderClusters(subscriptionID, resourceGroupName, clusterName).Get(ctx, api.ServiceProviderClusterResourceName)
	if err != nil && !database.IsNotFoundError(err) {
		logger.Error(err, "failed to get service provider cluster for kube-applier migration")
	}
	if err != nil {
		return nil
	}

	managementClusterResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if managementClusterResourceID == nil {
		logger.V(1).Info("no management cluster resource ID found; skipping kube-applier migration")
		return nil
	}

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, managementClusterResourceID)
	if kubeApplierClient == nil {
		logger.V(1).Info("kube-applier client unavailable for management cluster; skipping kube-applier migration",
			"managementClusterResourceID", managementClusterResourceID.String())
	}
	return kubeApplierClient
}

// migrateClusterDesires migrates all three kube-applier desire types (ApplyDesire,
// DeleteDesire, ReadDesire) for a cluster scope.
func migrateClusterDesires(ctx context.Context, logger logr.Logger, kubeApplierClient database.KubeApplierDBClient, subscriptionID, resourceGroupName, clusterName string) error {
	var migrationErrors []error

	applyCRUD, err := kubeApplierClient.ApplyDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to construct CRUD for cluster apply desire: %w", err))
	} else if err := migrateDesireDocuments(ctx, logger, applyCRUD, "cluster apply desire", applyDesireName); err != nil {
		migrationErrors = append(migrationErrors, err)
	}

	deleteCRUD, err := kubeApplierClient.DeleteDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to construct CRUD for cluster delete desire: %w", err))
	} else if err := migrateDesireDocuments(ctx, logger, deleteCRUD, "cluster delete desire", deleteDesireName); err != nil {
		migrationErrors = append(migrationErrors, err)
	}

	readCRUD, err := kubeApplierClient.ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to construct CRUD for cluster read desire: %w", err))
	} else if err := migrateDesireDocuments(ctx, logger, readCRUD, "cluster read desire", readDesireName); err != nil {
		migrationErrors = append(migrationErrors, err)
	}

	return errors.Join(migrationErrors...)
}

// migrateNodePoolDesires migrates all three kube-applier desire types (ApplyDesire,
// DeleteDesire, ReadDesire) for a node pool scope.
func migrateNodePoolDesires(ctx context.Context, logger logr.Logger, kubeApplierClient database.KubeApplierDBClient, subscriptionID, resourceGroupName, clusterName, nodePoolName string) error {
	var migrationErrors []error

	applyCRUD, err := kubeApplierClient.ApplyDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to construct CRUD for node pool apply desire: %w", err))
	} else if err := migrateDesireDocuments(ctx, logger, applyCRUD, "node pool apply desire", applyDesireName); err != nil {
		migrationErrors = append(migrationErrors, err)
	}

	deleteCRUD, err := kubeApplierClient.DeleteDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to construct CRUD for node pool delete desire: %w", err))
	} else if err := migrateDesireDocuments(ctx, logger, deleteCRUD, "node pool delete desire", deleteDesireName); err != nil {
		migrationErrors = append(migrationErrors, err)
	}

	readCRUD, err := kubeApplierClient.ReadDesiresForNodePool(subscriptionID, resourceGroupName, clusterName, nodePoolName)
	if err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to construct CRUD for node pool read desire: %w", err))
	} else if err := migrateDesireDocuments(ctx, logger, readCRUD, "node pool read desire", readDesireName); err != nil {
		migrationErrors = append(migrationErrors, err)
	}

	return errors.Join(migrationErrors...)
}

// applyDesireName returns the document name from an ApplyDesire, or empty string if ResourceID is nil.
func applyDesireName(d *kubeapplier.ApplyDesire) string {
	if d.ResourceID == nil {
		return ""
	}
	return d.ResourceID.Name
}

// deleteDesireName returns the document name from a DeleteDesire, or empty string if ResourceID is nil.
func deleteDesireName(d *kubeapplier.DeleteDesire) string {
	if d.ResourceID == nil {
		return ""
	}
	return d.ResourceID.Name
}

// readDesireName returns the document name from a ReadDesire, or empty string if ResourceID is nil.
func readDesireName(d *kubeapplier.ReadDesire) string {
	if d.ResourceID == nil {
		return ""
	}
	return d.ResourceID.Name
}

// migrateDesireDocuments lists and migrates all documents from a pre-constructed
// desire CRUD. The getName function extracts the document name used for the
// Get+Replace cycle; it returns empty string to skip items (e.g. nil ResourceID).
func migrateDesireDocuments[T any, TPointer arm.CosmosMetadataAccessorPtr[T]](ctx context.Context, logger logr.Logger, crud database.ResourceCRUD[T, TPointer], resourceDesc string, getName func(*T) string) error {
	iter, err := crud.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list %ss: %w", resourceDesc, err)
	}

	var migrationErrors []error
	seen := 0
	migrated := 0
	for _, item := range iter.Items(ctx) {
		seen++
		name := getName(item)
		if name == "" {
			continue
		}
		migrated++
		if err := replaceWithRetry[T, TPointer](ctx, logger, crud, name, resourceDesc); err != nil {
			migrationErrors = append(migrationErrors, err)
		}
	}
	if err := iter.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate %ss: %w", resourceDesc, err))
	}
	if seen > 0 {
		logger.Info("migrated kube-applier desires", "resourceType", resourceDesc, "seen", seen, "migrated", migrated)
	}
	return errors.Join(migrationErrors...)
}

// migrateControllers lists and migrates all controller documents under a given scope.
func migrateControllers(ctx context.Context, logger logr.Logger, controllerCRUD database.ResourceCRUD[api.Controller, *api.Controller], resourceDesc string) error {
	controllersIterator, err := controllerCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list %ss: %w", resourceDesc, err)
	}

	var migrationErrors []error
	count := 0
	for _, controller := range controllersIterator.Items(ctx) {
		count++
		if err := replaceWithRetry(ctx, logger, controllerCRUD, controller.ResourceID.Name, resourceDesc); err != nil {
			migrationErrors = append(migrationErrors, err)
		}
	}
	if err := controllersIterator.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate %ss: %w", resourceDesc, err))
	}
	if count > 0 {
		logger.Info("migrated resources", "resourceType", resourceDesc, "count", count)
	}
	return errors.Join(migrationErrors...)
}

// migrateManagementClusterContents lists and migrates all management cluster content documents under a given scope.
func migrateManagementClusterContents(ctx context.Context, logger logr.Logger, managementClusterContentCRUD database.ResourceCRUD[api.ManagementClusterContent, *api.ManagementClusterContent], resourceDesc string) error {
	managementClusterContentIterator, err := managementClusterContentCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list %ss: %w", resourceDesc, err)
	}

	var migrationErrors []error
	count := 0
	for _, managementClusterContent := range managementClusterContentIterator.Items(ctx) {
		count++
		if err := replaceWithRetry(ctx, logger, managementClusterContentCRUD, managementClusterContent.ResourceID.Name, resourceDesc); err != nil {
			migrationErrors = append(migrationErrors, err)
		}
	}
	if err := managementClusterContentIterator.GetError(); err != nil {
		migrationErrors = append(migrationErrors, fmt.Errorf("failed to iterate %ss: %w", resourceDesc, err))
	}
	if count > 0 {
		logger.Info("migrated resources", "resourceType", resourceDesc, "count", count)
	}
	return errors.Join(migrationErrors...)
}

// replaceWithRetry performs a Get+Replace cycle on a single document.
// On conflict errors (HTTP 409 or 412), it retries the full cycle up to maxRetries attempts.
func replaceWithRetry[T any, TPointer arm.CosmosMetadataAccessorPtr[T]](ctx context.Context, logger logr.Logger, crud database.ResourceCRUD[T, TPointer], name string, resourceDesc string) error {
	const maxRetries = 3
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		curr, err := crud.Get(ctx, name)
		if database.IsNotFoundError(err) {
			// Document was deleted between list and get; skip it.
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to get %s %q: %w", resourceDesc, name, err)
		}
		_, err = crud.Replace(ctx, curr, nil)
		if err == nil {
			return nil
		}
		if !database.IsConflictError(err) && !database.IsPreconditionFailedError(err) {
			return fmt.Errorf("failed to replace %s %q: %w", resourceDesc, name, err)
		}
		lastErr = err
		if attempt < maxRetries {
			logger.Info("conflict on replace, retrying",
				"resourceType", resourceDesc,
				"resourceName", name,
				"attempt", attempt,
				"maxRetries", maxRetries,
			)
		}
	}
	return fmt.Errorf("failed to replace %s %q after %d attempts due to conflict/precondition failure: %w", resourceDesc, name, maxRetries, lastErr)
}
