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

package creation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type revocationDesires struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	applyDesireLister            dblisters.ApplyDesireLister
	readDesireLister             dblisters.ReadDesireLister
}

var _ controllerutils.SystemAdminCredentialRevocationSyncer = (*revocationDesires)(nil)

// NewRevocationDesiresController returns a RevocationWatchingController that
// manages the CertificateRevocationRequest (CRR) desires used to revoke a
// cluster's already-issued certificates. It creates the CRR ApplyDesire and
// CRR ReadDesire so the hosted cluster can process the revocation. Observing
// the CRR for confirmation and marking the revocation complete is handled by the
// separate revocation-completion controller.
func NewRevocationDesiresController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
	backendInformers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	applyDesireLister dblisters.ApplyDesireLister,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	_, clusterLister := backendInformers.Clusters()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &revocationDesires{
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		applyDesireLister:            applyDesireLister,
		readDesireLister:             readDesireLister,
	}

	return controllerutils.NewSystemAdminCredentialRevocationWatchingController(
		"SystemAdminCredentialRevocationDesires",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *revocationDesires) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRevocationKey) error {
	logger := utils.LoggerFromContext(ctx)

	revocationCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRevocations(key.HCPClusterName)
	revocation, err := revocationCRUD.Get(ctx, key.RevocationName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// Once the revocation is marked for deletion the deletion controller owns it.
	if revocation.Status.DeletionTimestamp != nil {
		return nil
	}

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		logger.Info("waiting for ServiceProviderCluster.Status.ManagementClusterResourceID before creating revocation desires")
		return nil
	}

	controlPlaneNamespace := serviceProviderCluster.Status.ControlPlaneNamespace
	if len(controlPlaneNamespace) == 0 {
		logger.Info("waiting for ServiceProviderCluster.Status.ControlPlaneNamespace before creating revocation desires")
		return nil
	}

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		logger.Info("waiting for kube-applier client for management cluster", "managementCluster", mcResourceID.String())
		return nil
	}

	suffix := revocation.Spec.RevokeOpSuffix
	clusterResourceID := key.GetClusterResourceID()

	if err := c.ensureRevocationDesires(ctx, key, suffix, controlPlaneNamespace, clusterResourceID, mcResourceID, kubeApplierClient); err != nil {
		return err
	}

	return nil
}

// ensureRevocationDesires creates the RBAC, CRR ApplyDesire, and CRR ReadDesire
// for the revocation if they do not already exist. All desires are cluster-scoped
// and named by the revocation suffix so the deletion controller can find them.
func (c *revocationDesires) ensureRevocationDesires(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRevocationKey,
	suffix, controlPlaneNamespace string,
	owner, mcResourceID *azcorearm.ResourceID,
	kubeApplierClient kubeappliercosmosstorage.KubeApplierDBClient,
) error {
	// Revocation desires are nested under the SystemAdminCredentialRevocation so
	// the hierarchy mirrors the resource that owns them.
	applyCRUD, err := kubeApplierClient.ApplyDesiresForSystemAdminCredentialRevocation(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.RevocationName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	readCRUD, err := kubeApplierClient.ReadDesiresForSystemAdminCredentialRevocation(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.RevocationName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	// 1. CRR ApplyDesire.
	crrObj := systemadmincredential.BuildRevocationRequest(owner, suffix, controlPlaneNamespace)
	crrTarget := kubeapplierapi.ResourceReference{
		Group:     "certificates.hypershift.openshift.io",
		Version:   "v1alpha1",
		Resource:  "certificaterevocationrequests",
		Namespace: controlPlaneNamespace,
		Name:      crrObj.Name,
	}
	crrDesireName := "systemadmincredentialrevocation"
	crrDesire, err := buildRevocationApplyDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		key.RevocationName, crrDesireName, mcResourceID, crrTarget, crrObj)
	if err != nil {
		return err
	}
	if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyCRUD, c.applyDesireLister, crrDesire); err != nil {
		return err
	}

	// 2. CRR ReadDesire so the CRR status is mirrored back for the completion controller.
	crrReadDesireName := kubeapplierhelpers.ReadDesireNameForSystemAdminCredentialRequestRevocation()
	crrReadDesire, err := buildRevocationReadDesire(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		key.RevocationName, crrReadDesireName, mcResourceID, crrTarget)
	if err != nil {
		return err
	}
	if err := kubeapplierhelpers.EnsureReadDesire(ctx, readCRUD, c.readDesireLister, crrReadDesire); err != nil {
		return err
	}

	return nil
}

// buildRevocationApplyDesire builds a server-side-apply ApplyDesire for obj,
// nested under the named SystemAdminCredentialRevocation. Construction lives in
// this package (the revocation controller owns the shape of its own desires); the
// shared kubeapplierhelpers.EnsureApplyDesire helper then persists it.
func buildRevocationApplyDesire(
	subscriptionID, resourceGroupName, clusterName, revocationName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
	obj systemadmincredential.KubeObject,
) (*kubeapplierapi.ApplyDesire, error) {
	resourceIDStr := kubeapplierapi.ToSystemAdminCredentialRevocationScopedApplyDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, revocationName, desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse ApplyDesire resource ID %q: %w", resourceIDStr, err))
	}

	rawJSON, err := json.Marshal(obj)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to marshal kube object: %w", err))
	}

	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplierapi.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: rawJSON},
			},
		},
	}, nil
}

// buildRevocationReadDesire builds a ReadDesire that observes target, nested
// under the named SystemAdminCredentialRevocation.
func buildRevocationReadDesire(
	subscriptionID, resourceGroupName, clusterName, revocationName, desireName string,
	managementCluster *azcorearm.ResourceID,
	target kubeapplierapi.ResourceReference,
) (*kubeapplierapi.ReadDesire, error) {
	resourceIDStr := kubeapplierapi.ToSystemAdminCredentialRevocationScopedReadDesireResourceIDString(
		subscriptionID, resourceGroupName, clusterName, revocationName, desireName,
	)
	resourceID, err := azcorearm.ParseResourceID(resourceIDStr)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse ReadDesire resource ID %q: %w", resourceIDStr, err))
	}

	return &kubeapplierapi.ReadDesire{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(managementCluster.String()),
		},
		Spec: kubeapplierapi.ReadDesireSpec{
			ManagementCluster: managementCluster,
			TargetItem:        target,
		},
	}, nil
}
