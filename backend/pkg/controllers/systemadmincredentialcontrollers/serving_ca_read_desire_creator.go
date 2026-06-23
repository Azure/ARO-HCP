// Copyright 2025 Microsoft Corporation
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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// servingCASecretName is the name of the kube-apiserver serving CA
	// Secret in the HCP namespace. The HyperShift control-plane-pki-operator
	// creates this Secret to hold the serving CA cert+key.
	servingCASecretName = "kube-apiserver-server-ca"
)

// servingCAReadDesireCreatorSyncer creates a long-lived ReadDesire
// for the kube-apiserver serving CA Secret on the management cluster.
// This is a per-cluster ReadDesire that the CA bundle sync controller
// reads to populate ServiceProviderClusterStatus.ServingCABundle.
type servingCAReadDesireCreatorSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients

	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.ClusterSyncer = (*servingCAReadDesireCreatorSyncer)(nil)

// NewServingCAReadDesireCreatorController wires the serving CA
// ReadDesire creator as a cluster-watching controller.
func NewServingCAReadDesireCreatorController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &servingCAReadDesireCreatorSyncer{
		cooldownChecker:                     controllerutil.NewTimeBasedCooldownChecker(30 * time.Second),
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialServingCAReadDesireCreator",
		resourcesDBClient,
		backendInformers,
		nil, // no kube-applier informers needed for creation
		5*time.Minute,
		syncer,
	)
}

func (c *servingCAReadDesireCreatorSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *servingCAReadDesireCreatorSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}

	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}

	csClusterID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcpNamespace := fmt.Sprintf("ocm-%s-%s", c.hostedClusterNamespaceEnvIdentifier, csClusterID)

	desireName := systemadmincredhelpers.ReadDesireNameServingCA
	resourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName)
	resourceID, _ := azcorearm.ParseResourceID(resourceIDStr)

	target := kubeapplier.ResourceReference{
		Group:     "",
		Version:   "v1",
		Resource:  "secrets",
		Namespace: hcpNamespace,
		Name:      servingCASecretName,
	}

	desired := &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(mcResourceID.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: mcResourceID,
			TargetItem:        target,
		},
	}

	crud, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	existing, err := crud.Get(ctx, desireName)
	if database.IsNotFoundError(err) {
		logger.Info("creating serving CA ReadDesire")
		if _, err := crud.Create(ctx, desired, nil); err != nil {
			if database.IsConflictError(err) {
				return nil
			}
			return utils.TrackError(fmt.Errorf("create serving CA ReadDesire: %w", err))
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get serving CA ReadDesire: %w", err))
	}

	// Update if the target changed
	if existing.Spec.TargetItem != target {
		replacement := existing.DeepCopy()
		replacement.Spec = *desired.Spec.DeepCopy()
		if _, err := crud.Replace(ctx, replacement, nil); err != nil {
			return utils.TrackError(fmt.Errorf("replace serving CA ReadDesire: %w", err))
		}
	}

	return nil
}
