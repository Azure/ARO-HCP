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
	"fmt"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type desiresCreator struct {
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	applyDesireLister            dblisters.ApplyDesireLister
	readDesireLister             dblisters.ReadDesireLister
}

var _ controllerutils.SystemAdminCredentialRequestSyncer = (*desiresCreator)(nil)

// NewDesiresCreatorController returns a CredentialRequestWatchingController that
// creates the per-credential ApplyDesires (CSR, CSRApproval, 2 RBAC bundles) and
// ReadDesire (CSR) for individual SystemAdminCredentialRequest documents that are
// pending.
//
// The controller also fires on ReadDesire changes and consults the ApplyDesire /
// ReadDesire listers before writing so it skips the create entirely when a desire
// already exists with the desired content.
func NewDesiresCreatorController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, applyDesireLister := kubeApplierInformers.ApplyDesires()
	_, readDesireLister := kubeApplierInformers.ReadDesires()

	syncer := &desiresCreator{
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		serviceProviderClusterLister: serviceProviderClusterLister,
		applyDesireLister:            applyDesireLister,
		readDesireLister:             readDesireLister,
	}

	return controllerutils.NewSystemAdminCredentialRequestWatchingController(
		"SystemAdminCredentialDesiresCreator",
		resourcesDBClient,
		backendInformers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
}

// needsWork reports whether the desires-creator has anything to do for this
// credential request. It bundles the preconditions that gate creation: the
// cluster must be live (not being deleted, and already mapped to a
// cluster-service ID) and the credential must still be pending issuance.
func (c *desiresCreator) needsWork(cluster *coreapi.HCPOpenShiftCluster, cred *coreapi.SystemAdminCredentialRequest) bool {
	if cluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return false
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return false
	}
	return credentialrequest.IsCredentialRequestPending(cred)
}

func (c *desiresCreator) SyncOnce(ctx context.Context, key controllerutils.SystemAdminCredentialRequestKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// Get the specific credential request.
	credCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentialRequests(key.HCPClusterName)
	cred, err := credCRUD.Get(ctx, key.CredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}

	if !c.needsWork(existingCluster, cred) {
		return nil
	}

	// Preconditions satisfied — resolve the management cluster and kube-applier
	// client. These are readiness checks: if the mapping is not available yet we
	// return and wait to be retriggered.
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

	controlPlaneNamespace := serviceProviderCluster.Status.ControlPlaneNamespace
	if len(controlPlaneNamespace) == 0 {
		logger.Info("waiting for ServiceProviderCluster.Status.ControlPlaneNamespace before creating desires")
		return nil
	}

	kubeApplierClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kubeApplierClient == nil {
		return nil
	}

	// Owner for annotations is the cluster's ARM resource ID.
	clusterResourceID := key.GetClusterResourceID()

	if err := c.ensureDesires(ctx, key, cred, key.CredentialName, controlPlaneNamespace, clusterResourceID, mcResourceID, kubeApplierClient); err != nil {
		return err
	}

	logger.Info("ensured desires for credential", "credential", key.CredentialName)
	return nil
}

func (c *desiresCreator) ensureDesires(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRequestKey,
	cred *coreapi.SystemAdminCredentialRequest,
	credName, controlPlaneNamespace string,
	owner, mcResourceID *azcorearm.ResourceID,
	kubeApplierClient database.KubeApplierDBClient,
) error {
	// Desires for a credential are nested under the SystemAdminCredentialRequest
	// so the hierarchy mirrors the resource that owns them.
	parent := kubeapplierhelpers.CredentialRequestDesireParent(credName)
	applyCRUD, err := kubeApplierClient.ApplyDesiresForSystemAdminCredentialRequest(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, credName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	readCRUD, err := kubeApplierClient.ReadDesiresForSystemAdminCredentialRequest(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, credName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	// 1. CSR ApplyDesire
	csrDesireName := "systemadmincredentialcsr"
	csrObj := systemadmincredential.BuildCSR(owner, credName, controlPlaneNamespace, []byte(cred.Spec.CertificateSigningRequestPEM))
	if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyCRUD, c.applyDesireLister, parent,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		csrDesireName, mcResourceID, csrTarget(csrObj), csrObj); err != nil {
		return err
	}

	// 2. CSRApproval ApplyDesire
	csrApprovalDesireName := "systemadmincredentialcsrapproval"
	csrApprovalObj := systemadmincredential.BuildCSRApproval(owner, credName, controlPlaneNamespace)
	csrApprovalTarget := kubeapplierapi.ResourceReference{
		Group:     "certificates.hypershift.openshift.io",
		Version:   "v1alpha1",
		Resource:  "certificatesigningrequestapprovals",
		Namespace: controlPlaneNamespace,
		Name:      csrApprovalObj.Name,
	}
	if err := kubeapplierhelpers.EnsureApplyDesire(ctx, applyCRUD, c.applyDesireLister, parent,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		csrApprovalDesireName, mcResourceID, csrApprovalTarget, csrApprovalObj); err != nil {
		return err
	}

	// 3. CSR ReadDesire
	csrReadDesireName := kubeapplierhelpers.ReadDesireNameForSystemAdminCredentialRequestCSR()
	csrReadTarget := kubeapplierapi.ResourceReference{
		Group:    "certificates.k8s.io",
		Version:  "v1",
		Resource: "certificatesigningrequests",
		Name:     fmt.Sprintf("system-admin-credential-%s", credName),
	}
	if err := kubeapplierhelpers.EnsureReadDesire(ctx, readCRUD, c.readDesireLister, parent,
		key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName,
		csrReadDesireName, mcResourceID, csrReadTarget); err != nil {
		return err
	}

	return nil
}

// csrTarget builds the ResourceReference for a CertificateSigningRequest.
func csrTarget(csr *certificatesv1.CertificateSigningRequest) kubeapplierapi.ResourceReference {
	return kubeapplierapi.ResourceReference{
		Group:    "certificates.k8s.io",
		Version:  "v1",
		Resource: "certificatesigningrequests",
		Name:     csr.Name,
	}
}
