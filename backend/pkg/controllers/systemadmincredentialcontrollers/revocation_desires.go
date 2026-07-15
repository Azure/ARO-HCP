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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type revocationDesires struct {
	cooldownChecker              controllerutil.CooldownChecker
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	applyDesireLister            dblisters.ApplyDesireLister
	readDesireLister             dblisters.ReadDesireLister

	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.RevocationSyncer = (*revocationDesires)(nil)

// NewRevocationDesiresController returns a RevocationWatchingController that
// manages the CertificateRevocationRequest (CRR) desires used to revoke a
// cluster's already-issued certificates. It creates the RBAC, CRR ApplyDesire,
// and CRR ReadDesire so the hosted cluster can process the revocation. Observing
// the CRR for confirmation and marking the revocation complete is handled by the
// separate revocation-completion controller.
func NewRevocationDesiresController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	applyDesireLister dblisters.ApplyDesireLister,
	readDesireLister dblisters.ReadDesireLister,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &revocationDesires{
		cooldownChecker:                     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		serviceProviderClusterLister:        serviceProviderClusterLister,
		applyDesireLister:                   applyDesireLister,
		readDesireLister:                    readDesireLister,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	return controllerutils.NewRevocationWatchingController(
		"SystemAdminCredentialRevocationDesires",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *revocationDesires) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *revocationDesires) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRevocationKey) error {
	logger := utils.LoggerFromContext(ctx)

	revocationCRUD := c.resourcesDBClient.SystemAdminCredentialRevocations(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	revocation, err := revocationCRUD.Get(ctx, key.RevocationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRevocation: %w", err))
	}

	// Once the revocation is marked for deletion the deletion controller owns it.
	if revocation.Status.DeleteTimestamp != nil {
		return nil
	}

	cluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
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
		logger.Info("waiting for ServiceProviderCluster.Status.ManagementClusterResourceID before creating revocation desires")
		return nil
	}

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		logger.Info("waiting for kube-applier client for management cluster", "managementCluster", mcResourceID.String())
		return nil
	}

	suffix := revocation.Spec.RevokeOpSuffix
	csClusterID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcpNamespace := fmt.Sprintf("ocm-%s-%s", c.hostedClusterNamespaceEnvIdentifier, csClusterID)
	clusterResourceID := key.GetClusterResourceID()

	if err := c.ensureRevocationDesires(ctx, key, suffix, hcpNamespace, clusterResourceID, mcResourceID, kubeApplierClient); err != nil {
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
	suffix, hcpNamespace string,
	owner, mcResourceID *azcorearm.ResourceID,
	kubeApplierClient database.KubeApplierDBClient,
) error {
	// Revocation desires are nested under the SystemAdminCredentialRevocation so
	// the hierarchy mirrors the resource that owns them.
	parent := revocationDesireParent(key.RevocationName)
	applyCRUD, err := kubeApplierClient.ApplyDesiresForRevocation(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.RevocationName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	readCRUD, err := kubeApplierClient.ReadDesiresForRevocation(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.RevocationName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	// 1. RBAC granting the klusterlet permission to manage CRRs.
	rbacObjects := systemadmincredential.BuildRBACRevocation(owner, suffix, hcpNamespace)
	for i, obj := range rbacObjects {
		dName := fmt.Sprintf("systemAdminCredentialRevocationRBAC-%s", suffix)
		if i > 0 {
			dName = fmt.Sprintf("%s-%d", dName, i)
		}
		if err := ensureApplyDesire(ctx, applyCRUD, c.applyDesireLister, parent,
			key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
			dName, mcResourceID, targetRefForKubeObject(obj), obj); err != nil {
			return err
		}
	}

	// 2. CRR ApplyDesire.
	crrObj := systemadmincredential.BuildRevocationRequest(owner, suffix, hcpNamespace)
	crrTarget := kubeapplier.ResourceReference{
		Group:     "certificates.hypershift.openshift.io",
		Version:   "v1alpha1",
		Resource:  "certificaterevocationrequests",
		Namespace: hcpNamespace,
		Name:      crrObj.Name,
	}
	crrDesireName := fmt.Sprintf("systemAdminCredentialRevocation-%s", suffix)
	if err := ensureApplyDesire(ctx, applyCRUD, c.applyDesireLister, parent,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		crrDesireName, mcResourceID, crrTarget, crrObj); err != nil {
		return err
	}

	// 3. CRR ReadDesire so the CRR status is mirrored back for the completion controller.
	crrReadDesireName := kubeapplierhelpers.ReadDesireNameForSystemAdminCredentialRequestRevocation(suffix)
	if err := ensureReadDesire(ctx, readCRUD, c.readDesireLister, parent,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		crrReadDesireName, mcResourceID, crrTarget); err != nil {
		return err
	}

	return nil
}
