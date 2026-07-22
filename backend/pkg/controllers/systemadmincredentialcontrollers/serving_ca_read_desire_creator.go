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
	"strings"
	"time"

	"github.com/blang/semver/v4"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	readDesireNameServingCA         = "systemadmincredential-serving-ca"
	servingCAConfigMapName          = "root-ca"
	minServingCAConfigMapOCPVersion = "4.20"
)

type servingCAReadDesireCreator struct {
	cooldownChecker              controllerutil.CooldownChecker
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.ClusterSyncer = (*servingCAReadDesireCreator)(nil)

func NewServingCAReadDesireCreatorController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &servingCAReadDesireCreator{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialServingCAReadDesireCreator",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *servingCAReadDesireCreator) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *servingCAReadDesireCreator) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}
	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}

	atLeastMinVersion, err := clusterVersionAtLeast(existingCluster.CustomerProperties.Version.ID, minServingCAConfigMapOCPVersion)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to evaluate cluster version for serving CA ReadDesire: %w", err))
	}
	if !atLeastMinVersion {
		return nil
	}

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	clusterName := existingCluster.CustomerProperties.DNS.BaseDomainPrefix
	if len(clusterName) == 0 {
		return nil
	}
	hcpNamespace := hostedControlPlaneNamespace(clusterName)

	target := kubeapplier.ResourceReference{
		Group:     "",
		Version:   "v1",
		Resource:  "configmaps",
		Namespace: hcpNamespace,
		Name:      servingCAConfigMapName,
	}

	desired, err := buildServingCAReadDesire(
		kubeapplier.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, readDesireNameServingCA),
		mcResourceID,
		target,
	)
	if err != nil {
		return err
	}

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		return nil
	}

	crud, err := kubeApplierClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	existing, err := crud.Get(ctx, readDesireNameServingCA)
	if database.IsNotFoundError(err) {
		existing = nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire: %w", err))
	}

	if existing == nil {
		if _, err := crud.Create(ctx, desired, nil); err != nil && !database.IsConflictError(err) {
			return utils.TrackError(fmt.Errorf("create serving CA ReadDesire: %w", err))
		}
		logger.Info("created serving CA ReadDesire")
		return nil
	}

	if !controllerutil.ResourceIDsEqual(existing.Spec.ManagementCluster, desired.Spec.ManagementCluster) ||
		existing.Spec.TargetItem != desired.Spec.TargetItem {
		replacement := existing.DeepCopy()
		replacement.Spec = *desired.Spec.DeepCopy()
		if _, err := crud.Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("replace serving CA ReadDesire: %w", err))
		}
		logger.Info("updated serving CA ReadDesire")
	}

	return nil
}

func buildServingCAReadDesire(resourceIDString string, managementCluster *azcorearm.ResourceID, target kubeapplier.ResourceReference) (*kubeapplier.ReadDesire, error) {
	resourceID, err := azcorearm.ParseResourceID(resourceIDString)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse ReadDesire resource ID %q: %w", resourceIDString, err))
	}
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
	}, nil
}

func hostedControlPlaneNamespace(clusterName string) string {
	return fmt.Sprintf("clusters-%s", clusterName)
}

func clusterVersionAtLeast(versionID, minVersion string) (bool, error) {
	if len(versionID) == 0 {
		return false, nil
	}
	current, err := semver.ParseTolerant(versionID)
	if err != nil {
		return false, fmt.Errorf("failed to parse cluster version %q: %w", versionID, err)
	}
	minimum, err := semver.ParseTolerant(minVersion)
	if err != nil {
		return false, fmt.Errorf("failed to parse minimum version %q: %w", minVersion, err)
	}
	return current.GE(minimum), nil
}
