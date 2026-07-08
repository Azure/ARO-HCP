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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilsclock "k8s.io/utils/clock"

	"sigs.k8s.io/controller-runtime/pkg/client"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type revocationDesires struct {
	cooldownChecker              controllerutil.CooldownChecker
	clock                        utilsclock.PassiveClock
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	readDesireLister             dblisters.ReadDesireLister

	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.RevocationSyncer = (*revocationDesires)(nil)

// NewRevocationDesiresController returns a RevocationWatchingController that
// manages the CertificateRevocationRequest (CRR) desires used to revoke a
// cluster's already-issued certificates. It creates the RBAC, CRR ApplyDesire,
// and CRR ReadDesire, watches the mirrored CRR for confirmation, and — once the
// hosted cluster confirms revocation and the credential requests have been
// marked for deletion — marks the revocation Complete and stamps its
// DeleteTimestamp so the deletion controller can tear everything down.
func NewRevocationDesiresController(
	clock utilsclock.PassiveClock,
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	readDesireLister dblisters.ReadDesireLister,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &revocationDesires{
		cooldownChecker:                     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clock:                               clock,
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		serviceProviderClusterLister:        serviceProviderClusterLister,
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

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		logger.Info("waiting for kube-applier client for management cluster", "managementCluster", mcResourceID.String())
		return nil
	}

	suffix := revocation.Spec.RevokeOpSuffix
	csClusterID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcpNamespace := fmt.Sprintf("ocm-%s-%s", c.hostedClusterNamespaceEnvIdentifier, csClusterID)
	clusterResourceID := key.GetClusterResourceID()

	if err := c.ensureRevocationDesires(ctx, key, suffix, hcpNamespace, clusterResourceID, mcResourceID, kaClient); err != nil {
		return err
	}

	// Check whether the hosted cluster has confirmed the certificates are revoked.
	if !revocation.Status.IsCertificatesRevoked() {
		cachedCRR, err := maestrohelpers.GetCachedCertificateRevocationRequestForCluster(
			ctx, c.readDesireLister,
			key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, suffix)
		if err != nil {
			return utils.TrackError(err)
		}
		if cachedCRR == nil {
			logger.Info("waiting for CertificateRevocationRequest to be mirrored back from the hosted cluster")
			return nil
		}
		revoked := false
		for _, cond := range cachedCRR.Status.Conditions {
			if cond.Type == "PreviousCertificatesRevoked" && cond.Status == metav1.ConditionTrue {
				revoked = true
				break
			}
		}
		if !revoked {
			logger.Info("waiting for the hosted cluster to confirm certificate revocation")
			return nil
		}
		replacement := revocation.DeepCopy()
		replacement.Status.SetCondition(
			api.SystemAdminCredentialRevocationConditionCertificatesRevoked,
			metav1.ConditionTrue, "CertificatesRevoked", "Hosted cluster confirmed previously-issued certificates are revoked")
		if _, err := revocationCRUD.Replace(ctx, replacement, nil); err != nil {
			if database.IsPreconditionFailedError(err) {
				return nil
			}
			return utils.TrackError(fmt.Errorf("failed to set CertificatesRevoked condition: %w", err))
		}
		revocation = replacement
	}

	// The revocation is complete once the certificates are revoked and every
	// credential request has been marked for deletion. Stamp the DeleteTimestamp
	// so the deletion controller can tear the desires down and remove the doc.
	if revocation.Status.IsCertificatesRevoked() && revocation.Status.IsCredentialsMarkedForDeletion() {
		replacement := revocation.DeepCopy()
		replacement.Status.SetCondition(
			api.SystemAdminCredentialRevocationConditionComplete,
			metav1.ConditionTrue, "Complete", "Revocation is complete and ready for teardown")
		now := metav1.NewTime(c.clock.Now())
		replacement.Status.DeleteTimestamp = &now
		if _, err := revocationCRUD.Replace(ctx, replacement, nil); err != nil {
			if database.IsPreconditionFailedError(err) {
				return nil
			}
			return utils.TrackError(fmt.Errorf("failed to mark revocation complete: %w", err))
		}
		logger.Info("revocation complete, marked for deletion")
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
	kaClient database.KubeApplierDBClient,
) error {
	applyCRUD, err := kaClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	readCRUD, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
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
		if err := c.ensureApplyDesire(ctx, applyCRUD, key, dName, mcResourceID, targetRefForClientObject(obj), obj); err != nil {
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
	if err := c.ensureApplyDesire(ctx, applyCRUD, key, crrDesireName, mcResourceID, crrTarget, crrObj); err != nil {
		return err
	}

	// 3. CRR ReadDesire so the CRR status is mirrored back for the poll above.
	crrReadDesireName := maestrohelpers.ReadDesireNameForSystemAdminCredentialRequestRevocation(suffix)
	if err := c.ensureReadDesire(ctx, readCRUD, key, crrReadDesireName, mcResourceID, crrTarget); err != nil {
		return err
	}

	return nil
}

func (c *revocationDesires) ensureApplyDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	key controllerutils.SystemAdminCredentialRevocationKey,
	desireName string,
	mcResourceID *azcorearm.ResourceID,
	target kubeapplier.ResourceReference,
	obj client.Object,
) error {
	if _, err := crud.Get(ctx, strings.ToLower(desireName)); err == nil {
		return nil
	} else if !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("get ApplyDesire %s: %w", desireName, err))
	}

	resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName)
	// resourceIDStr is produced by an internal ID builder from already-validated key
	// components, so a parse failure indicates a programming error; fail fast rather
	// than silently writing a Cosmos document with a nil ResourceID.
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))

	rawJSON, err := json.Marshal(obj)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to marshal kube object: %w", err))
	}

	desire := &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(mcResourceID.String()),
		},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: mcResourceID,
			Type:              kubeapplier.ApplyDesireTypeServerSideApply,
			TargetItem:        target,
			ServerSideApply: &kubeapplier.ServerSideApplyConfig{
				KubeContent: &runtime.RawExtension{Raw: rawJSON},
			},
		},
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil && !database.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create ApplyDesire %s: %w", desireName, err))
	}
	return nil
}

func (c *revocationDesires) ensureReadDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	key controllerutils.SystemAdminCredentialRevocationKey,
	desireName string,
	mcResourceID *azcorearm.ResourceID,
	target kubeapplier.ResourceReference,
) error {
	if _, err := crud.Get(ctx, strings.ToLower(desireName)); err == nil {
		return nil
	} else if !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("get ReadDesire %s: %w", desireName, err))
	}

	resourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName)
	// resourceIDStr is produced by an internal ID builder from already-validated key
	// components, so a parse failure indicates a programming error; fail fast rather
	// than silently writing a Cosmos document with a nil ResourceID.
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))

	desire := &kubeapplier.ReadDesire{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(mcResourceID.String()),
		},
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: mcResourceID,
			TargetItem:        target,
		},
	}
	if _, err := crud.Create(ctx, desire, nil); err != nil && !database.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create ReadDesire %s: %w", desireName, err))
	}
	return nil
}
