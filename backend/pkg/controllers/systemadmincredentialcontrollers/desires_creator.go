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

	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/runtime"

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
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// credentialDesirePrefix is used to identify credential-related desires by name prefix.
const credentialDesirePrefix = "systemAdminCredential"

type desiresCreator struct {
	cooldownChecker controllerutil.CooldownChecker

	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	serviceProviderClusterLister listers.ServiceProviderClusterLister

	hostedClusterNamespaceEnvIdentifier string
}

var _ controllerutils.CredentialRequestSyncer = (*desiresCreator)(nil)

// NewDesiresCreatorController returns a CredentialRequestWatchingController that
// creates the per-credential ApplyDesires (CSR, CSRA, 3 RBAC bundles) and
// ReadDesire (CSR) for individual SystemAdminCredentialRequest documents that are
// pending.
func NewDesiresCreatorController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	backendInformers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()

	syncer := &desiresCreator{
		cooldownChecker:                     controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:                   resourcesDBClient,
		kubeApplierDBClients:                kubeApplierDBClients,
		serviceProviderClusterLister:        serviceProviderClusterLister,
		hostedClusterNamespaceEnvIdentifier: hostedClusterNamespaceEnvIdentifier,
	}

	return controllerutils.NewCredentialRequestWatchingController(
		"SystemAdminCredentialDesiresCreator",
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (c *desiresCreator) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
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

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}

	csClusterID := existingCluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcpNamespace := fmt.Sprintf("ocm-%s-%s", c.hostedClusterNamespaceEnvIdentifier, csClusterID)

	// Owner for annotations is the cluster's ARM resource ID.
	clusterResourceID := key.GetClusterResourceID()

	// Get the specific credential request.
	credCRUD := c.resourcesDBClient.SystemAdminCredentialRequests(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	cred, err := credCRUD.Get(ctx, key.CredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get SystemAdminCredentialRequest: %w", err))
	}

	if !cred.Status.IsPending() {
		return nil
	}

	if err := c.ensureDesires(ctx, key, cred, key.CredentialName, hcpNamespace, clusterResourceID, mcResourceID, kaClient); err != nil {
		return err
	}

	logger.Info("ensured desires for credential", "credential", key.CredentialName)
	return nil
}

func (c *desiresCreator) ensureDesires(
	ctx context.Context,
	key controllerutils.SystemAdminCredentialRequestKey,
	cred *api.SystemAdminCredentialRequest,
	credName, hcpNamespace string,
	owner, mcResourceID *azcorearm.ResourceID,
	kaClient database.KubeApplierDBClient,
) error {
	logger := utils.LoggerFromContext(ctx)

	applyCRUD, err := kaClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesire CRUD: %w", err))
	}
	readCRUD, err := kaClient.ReadDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesire CRUD: %w", err))
	}

	// 1-3. RBAC bundles (created before CSR/CSRA so permissions are in place).
	rbacSpecs := []struct {
		desireName string
		builder    func() []client.Object
	}{
		{
			desireName: fmt.Sprintf("systemAdminCredentialRBACGiveCSRPerm-%s", credName),
			builder:    func() []client.Object { return systemadmincredential.BuildRBACGiveCSRPerm(owner, credName) },
		},
		{
			desireName: fmt.Sprintf("systemAdminCredentialRBACCSRA-%s", credName),
			builder:    func() []client.Object { return systemadmincredential.BuildRBACCSRA(owner, credName, hcpNamespace) },
		},
		{
			desireName: fmt.Sprintf("systemAdminCredentialRBACRevocation-%s", credName),
			builder: func() []client.Object {
				return systemadmincredential.BuildRBACRevocation(owner, credName, hcpNamespace)
			},
		},
	}

	for _, rbacSpec := range rbacSpecs {
		objects := rbacSpec.builder()
		for i, obj := range objects {
			suffix := ""
			if i > 0 {
				suffix = fmt.Sprintf("-%d", i)
			}
			dName := rbacSpec.desireName + suffix
			ref := targetRefForClientObject(obj)
			if err := c.createApplyDesire(ctx, applyCRUD, key, dName, mcResourceID, ref, obj); err != nil {
				return err
			}
		}
	}

	// 4. CSR ApplyDesire
	csrDesireName := fmt.Sprintf("systemAdminCredentialCSR-%s", credName)
	csrObj, err := systemadmincredential.BuildCSR(owner, credName, cred.Spec.Username, hcpNamespace, []byte(cred.Spec.PrivateKeyPEM))
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build CSR: %w", err))
	}
	if err := c.createApplyDesire(ctx, applyCRUD, key, csrDesireName, mcResourceID, csrTarget(csrObj), csrObj); err != nil {
		return err
	}
	logger.Info("created CSR ApplyDesire", "credential", credName)

	// 5. CSRA ApplyDesire
	csraDesireName := fmt.Sprintf("systemAdminCredentialCSRA-%s", credName)
	csraObj := systemadmincredential.BuildCSRA(owner, credName, hcpNamespace)
	if err := c.createApplyDesire(ctx, applyCRUD, key, csraDesireName, mcResourceID,
		kubeapplier.ResourceReference{
			Group:     "certificates.hypershift.openshift.io",
			Version:   "v1alpha1",
			Resource:  "certificatesigningrequestapprovals",
			Namespace: hcpNamespace,
			Name:      csraObj.Name,
		}, csraObj); err != nil {
		return err
	}

	// 6. CSR ReadDesire
	csrReadDesireName := maestrohelpers.ReadDesireNameForSystemAdminCredentialRequestCSR(credName)
	if err := c.createReadDesire(ctx, readCRUD, key, csrReadDesireName, mcResourceID, kubeapplier.ResourceReference{
		Group:    "certificates.k8s.io",
		Version:  "v1",
		Resource: "certificatesigningrequests",
		Name:     fmt.Sprintf("system-admin-credential-%s", credName),
	}); err != nil {
		return err
	}
	logger.Info("created CSR ReadDesire", "credential", credName)

	return nil
}

func (c *desiresCreator) createApplyDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire],
	key controllerutils.SystemAdminCredentialRequestKey,
	desireName string,
	mcResourceID *azcorearm.ResourceID,
	target kubeapplier.ResourceReference,
	obj client.Object,
) error {
	resourceIDStr := kubeapplier.ToClusterScopedApplyDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName)
	resourceID, _ := azcorearm.ParseResourceID(resourceIDStr)

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
			TargetItem:        target,
			KubeContent:       &runtime.RawExtension{Raw: rawJSON},
		},
	}

	_, err = crud.Create(ctx, desire, nil)
	if err != nil && !database.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create ApplyDesire %s: %w", desireName, err))
	}
	return nil
}

func (c *desiresCreator) createReadDesire(
	ctx context.Context,
	crud database.ResourceCRUD[kubeapplier.ReadDesire, *kubeapplier.ReadDesire],
	key controllerutils.SystemAdminCredentialRequestKey,
	desireName string,
	mcResourceID *azcorearm.ResourceID,
	target kubeapplier.ResourceReference,
) error {
	resourceIDStr := kubeapplier.ToClusterScopedReadDesireResourceIDString(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, desireName)
	resourceID, _ := azcorearm.ParseResourceID(resourceIDStr)

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

	_, err := crud.Create(ctx, desire, nil)
	if err != nil && !database.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create ReadDesire %s: %w", desireName, err))
	}
	return nil
}

// csrTarget builds the ResourceReference for a CertificateSigningRequest.
func csrTarget(csr *certificatesv1.CertificateSigningRequest) kubeapplier.ResourceReference {
	return kubeapplier.ResourceReference{
		Group:    "certificates.k8s.io",
		Version:  "v1",
		Resource: "certificatesigningrequests",
		Name:     csr.Name,
	}
}

// targetRefForClientObject builds a ResourceReference for known RBAC types.
func targetRefForClientObject(obj client.Object) kubeapplier.ResourceReference {
	gvk := obj.GetObjectKind().GroupVersionKind()
	resource := strings.ToLower(gvk.Kind) + "s"
	return kubeapplier.ResourceReference{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Resource:  resource,
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
}
