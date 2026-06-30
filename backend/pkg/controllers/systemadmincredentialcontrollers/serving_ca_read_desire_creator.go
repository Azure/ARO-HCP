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
	// readDesireNameServingCA is the well-known ReadDesire name used to
	// mirror the kube-apiserver serving CA secret from the management cluster.
	readDesireNameServingCA = "systemadmincredential-serving-ca"

	// servingCASecretName is the name of the HyperShift-managed Secret
	// containing the kube-apiserver serving CA in the hosted cluster namespace.
	servingCASecretName = "kube-apiserver-server-ca"
)

type servingCAReadDesireCreator struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients

	serviceProviderClusterLister listers.ServiceProviderClusterLister

	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.CredentialRequestSyncer = (*servingCAReadDesireCreator)(nil)

// NewServingCAReadDesireCreatorController returns a CredentialRequestWatchingController
// that ensures a ReadDesire exists per cluster pointing at the kube-apiserver
// serving CA Secret in the hosted cluster namespace on the management cluster.
// The kube-applier mirrors the Secret content into ReadDesire.Status.KubeContent;
// controller #8 (CABundleSync) reads from there.
//
// This controller fires on credential request events so it immediately creates
// the serving CA ReadDesire when the first credential request appears for a
// cluster.
func NewServingCAReadDesireCreatorController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &servingCAReadDesireCreator{
		cooldownChecker:                     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		serviceProviderClusterLister:        serviceProviderClusterLister,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	return controllerutils.NewCredentialRequestWatchingController(
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

func (c *servingCAReadDesireCreator) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
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

	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	csClusterID := existingCluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcpNamespace := fmt.Sprintf("ocm-%s-%s", c.hostedClusterNamespaceEnvIdentifier, csClusterID)

	target := kubeapplier.ResourceReference{
		Group:     "",
		Version:   "v1",
		Resource:  "secrets",
		Namespace: hcpNamespace,
		Name:      servingCASecretName,
	}

	desired := buildServingCAReadDesire(
		kubeapplier.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, readDesireNameServingCA),
		mcResourceID,
		target,
	)

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}

	crud, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
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

	// Check if spec needs updating.
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

func buildServingCAReadDesire(resourceIDString string, managementCluster *azcorearm.ResourceID, target kubeapplier.ResourceReference) *kubeapplier.ReadDesire {
	resourceID, _ := azcorearm.ParseResourceID(resourceIDString)
	return &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
	}
}
