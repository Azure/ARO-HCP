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

	"github.com/go-logr/logr"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// credentialDesiresCreator is controller #11. Driven by the
// SystemAdminCredential informer: every credential reconcile checks
// whether the expected per-credential ApplyDesires and CSR ReadDesire
// exist in Cosmos and appends to Status.OutstandingDesires when it
// creates them.
//
// Idempotent: every desire write tolerates ConflictError (already
// exists), and the OutstandingDesires list is only appended to when
// the entry is not already present. Re-running the controller for
// the same credential is safe.
//
// Skips credentials whose Phase is not Requested — Issued/Failed
// credentials are owned by the post-issuance cleanup; AwaitingRevocation
// and Revoked credentials are owned by the revoke poller.
type credentialDesiresCreator struct {
	cooldownChecker              controllerutil.CooldownChecker
	clusterLister                listers.ClusterLister
	credentialLister             listers.SystemAdminCredentialLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	hostedClusterNSEnvID         string
}

func NewCredentialDesiresCreatorController(
	clusterLister listers.ClusterLister,
	credentialLister listers.SystemAdminCredentialLister,
	serviceProviderClusterLister listers.ServiceProviderClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &credentialDesiresCreator{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:                clusterLister,
		credentialLister:             credentialLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		hostedClusterNSEnvID:         hostedClusterNamespaceEnvIdentifier,
	}
	return controllerutils.NewSystemAdminCredentialWatchingController(
		"SystemAdminCredentialDesiresCreator",
		resourcesDBClient,
		informers,
		30*time.Second,
		syncer,
	)
}

func (c *credentialDesiresCreator) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *credentialDesiresCreator) SyncOnce(ctx context.Context, key controllerutils.HCPSystemAdminCredentialKey) error {
	logger := utils.LoggerFromContext(ctx)

	cred, err := c.credentialLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPSystemAdminCredentialName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get credential: %w", err))
	}

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get cluster: %w", err))
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		return nil
	}

	clusterRID := cluster.GetResourceID()
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it. SystemAdminCredentialWatchingController
		// does not watch the ServiceProviderCluster informer (a ServiceProviderCluster
		// arrival can't be walked down to a specific credential), so the next
		// attempt happens on the controller's resync or the next
		// SystemAdminCredential event for this credential.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ServiceProviderCluster: %w", err))
	}
	mcRID := serviceProviderCluster.Status.ManagementClusterResourceID
	if mcRID == nil {
		return nil
	}
	kaClient := c.kubeApplierDBClients.For(ctx, mcRID)
	if kaClient == nil {
		return nil
	}

	credName := cred.GetResourceID().Name

	// Teardown branch. Two triggers map to the same MC-content-removal
	// flow because in both cases we no longer need the CSR / CSRA / RBAC
	// on the management cluster:
	//   - DeletionTimestamp set: the credential is being deleted; the
	//     finalizer controller takes over once DesiresCleanedUp is True.
	//   - SignedCertificate set: the certificate has been issued; the
	//     scaffolding that produced it is no longer needed and is freed
	//     to keep the kube-applier partition small.
	if cred.Spec.DeletionTimestamp != nil || cred.Status.SignedCertificate != "" {
		return c.runTeardown(ctx, logger, kaClient, cred, credName)
	}

	// Creation branch. Only credentials still in the Requested phase need
	// their CSR/CSRA/RBAC scaffolding written. Once the IssuanceObserver
	// flips Phase to Issued (and populates SignedCertificate) the teardown
	// branch above takes over.
	if cred.Status.Phase != api.SystemAdminCredentialPhaseRequested {
		return nil
	}

	csClusterID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcpNamespace := hostedClusterNamespace(c.hostedClusterNSEnvID, csClusterID)
	signerName := fmt.Sprintf(HypershiftSignerNameFmt, hcpNamespace)
	username := cred.Spec.Username
	if username == "" {
		username = defaultUsername
	}

	csrObj, err := systemadmincredential.BuildCSR(clusterRID, credName, signerName, hcpNamespace, username, []byte(cred.Spec.PrivateKeyPEM))
	if err != nil {
		return utils.TrackError(fmt.Errorf("build CSR: %w", err))
	}
	csraObj, err := systemadmincredential.BuildCSRA(clusterRID, credName, hcpNamespace)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build CSRA: %w", err))
	}
	giveCSRPermClusterRole, giveCSRPermClusterRoleBinding, err := systemadmincredential.BuildRBACGiveCSRPerm(clusterRID, credName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build RBAC give-csr-perm: %w", err))
	}
	csraPermRole, csraPermRoleBinding, err := systemadmincredential.BuildRBACCSRA(clusterRID, credName, hcpNamespace)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build RBAC csra-perm: %w", err))
	}

	// RBAC for revocation is created by operationRevokeCredentialsDispatch when
	// the customer actually revokes, not here — credentials in Phase=Requested
	// never need it. Keeps the give-csr/csra-perm bundles narrowly scoped to
	// issuance.
	type applyPlan struct {
		nameSegment string
		obj         runtime.Object
	}
	plans := []applyPlan{
		{credentialDesireName(systemadmincredential.CSRNamePrefix, credName), csrObj},
		{credentialDesireName(systemadmincredential.CSRANamePrefix, credName), csraObj},
		{credentialDesireName(systemadmincredential.RBACGiveCSRPermNamePrefix, credName) + "-clusterrole", giveCSRPermClusterRole},
		{credentialDesireName(systemadmincredential.RBACGiveCSRPermNamePrefix, credName) + "-clusterrolebinding", giveCSRPermClusterRoleBinding},
		{credentialDesireName(systemadmincredential.RBACCSRAPermNamePrefix, credName) + "-role", csraPermRole},
		{credentialDesireName(systemadmincredential.RBACCSRAPermNamePrefix, credName) + "-rolebinding", csraPermRoleBinding},
	}

	applyCRUD, err := kaClient.ApplyDesiresForCluster(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesires CRUD: %w", err))
	}
	readCRUD, err := kaClient.ReadDesiresForCluster(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesires CRUD: %w", err))
	}

	updated := cred.DeepCopy()
	mutated := false
	for _, plan := range plans {
		if hasOutstandingDesire(updated, api.SystemAdminCredentialDesireKindApply, plan.nameSegment) {
			continue
		}
		raw, err := json.Marshal(plan.obj)
		if err != nil {
			return utils.TrackError(fmt.Errorf("marshal %s: %w", plan.nameSegment, err))
		}
		ad := &kubeapplier.ApplyDesire{
			CosmosMetadata: buildScopedDesireMetadata(clusterRID, plan.nameSegment, kubeapplier.ApplyDesireResourceTypeName),
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: mcRID,
				TargetItem:        targetItemFor(plan.obj),
				KubeContent:       &runtime.RawExtension{Raw: raw},
			},
		}
		if _, err := applyCRUD.Create(ctx, ad, nil); err != nil && !database.IsConflictError(err) {
			return utils.TrackError(fmt.Errorf("create ApplyDesire %s: %w", plan.nameSegment, err))
		}
		updated.Status.OutstandingDesires = append(updated.Status.OutstandingDesires, api.SystemAdminCredentialDesireRef{
			Kind: api.SystemAdminCredentialDesireKindApply, Name: plan.nameSegment,
		})
		mutated = true
	}

	csrReadName := credentialDesireName(systemadmincredential.CSRNamePrefix, credName)
	if !hasOutstandingDesire(updated, api.SystemAdminCredentialDesireKindRead, csrReadName) {
		rd := &kubeapplier.ReadDesire{
			CosmosMetadata: buildScopedDesireMetadata(clusterRID, csrReadName, kubeapplier.ReadDesireResourceTypeName),
			Spec: kubeapplier.ReadDesireSpec{
				ManagementCluster: mcRID,
				TargetItem:        targetItemFor(csrObj),
			},
		}
		if _, err := readCRUD.Create(ctx, rd, nil); err != nil && !database.IsConflictError(err) {
			return utils.TrackError(fmt.Errorf("create ReadDesire %s: %w", csrReadName, err))
		}
		updated.Status.OutstandingDesires = append(updated.Status.OutstandingDesires, api.SystemAdminCredentialDesireRef{
			Kind: api.SystemAdminCredentialDesireKindRead, Name: csrReadName,
		})
		mutated = true
	}

	if !mutated {
		return nil
	}
	credentialsCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).SystemAdminCredentials(key.HCPClusterName)
	if _, err := credentialsCRUD.Replace(ctx, updated, nil); err != nil {
		return utils.TrackError(fmt.Errorf("persist credential OutstandingDesires: %w", err))
	}
	logger.Info("credential OutstandingDesires updated", "credential", credName, "count", len(updated.Status.OutstandingDesires))
	return nil
}

// runTeardown drives the shared per-credential desire teardown helper.
// It is invoked from both lifecycle triggers (DeletionTimestamp set, or
// SignedCertificate populated). On completion (OutstandingDesires empty)
// it flips the DesiresCleanedUp condition to True so the
// credential-deletion finalizer can act.
func (c *credentialDesiresCreator) runTeardown(
	ctx context.Context,
	logger logr.Logger,
	kaClient database.KubeApplierDBClient,
	cred *api.SystemAdminCredential,
	credName string,
) error {
	updated := cred.DeepCopy()
	remaining, err := teardownCredentialOutstandingDesires(ctx, kaClient, c.resourcesDBClient, updated)
	if err != nil {
		return utils.TrackError(fmt.Errorf("teardown credential %q: %w", credName, err))
	}
	if remaining == 0 {
		apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
			Type:    api.SystemAdminCredentialDesiresCleanedUpConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  "AllDesiresTornDown",
			Message: "Every kube-applier desire this credential owned has been torn down on the management cluster.",
		})
	}
	if equalCredentialTeardownState(cred, updated) {
		return nil
	}
	credentialsCRUD := c.resourcesDBClient.HCPClusters(cred.GetResourceID().Parent.SubscriptionID, cred.GetResourceID().Parent.ResourceGroupName).
		SystemAdminCredentials(cred.GetResourceID().Parent.Name)
	if _, err := credentialsCRUD.Replace(ctx, updated, nil); database.IsPreconditionFailedError(err) {
		// Another writer beat us; informer re-enqueue will retry.
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("persist credential teardown: %w", err))
	}
	logger.Info("credential teardown progressed", "credential", credName, "outstanding", remaining)
	return nil
}

// equalCredentialTeardownState reports whether the teardown-relevant
// fields (OutstandingDesires + the DesiresCleanedUp condition) are
// identical between the cached credential and the post-sweep copy.
// Avoids a Replace when nothing changed.
func equalCredentialTeardownState(a, b *api.SystemAdminCredential) bool {
	if len(a.Status.OutstandingDesires) != len(b.Status.OutstandingDesires) {
		return false
	}
	for i := range a.Status.OutstandingDesires {
		if a.Status.OutstandingDesires[i] != b.Status.OutstandingDesires[i] {
			return false
		}
	}
	aCond := apimeta.FindStatusCondition(a.Status.Conditions, api.SystemAdminCredentialDesiresCleanedUpConditionType)
	bCond := apimeta.FindStatusCondition(b.Status.Conditions, api.SystemAdminCredentialDesiresCleanedUpConditionType)
	if (aCond == nil) != (bCond == nil) {
		return false
	}
	if aCond != nil && aCond.Status != bCond.Status {
		return false
	}
	return true
}

func hasOutstandingDesire(cred *api.SystemAdminCredential, kind api.SystemAdminCredentialDesireKind, name string) bool {
	for _, ref := range cred.Status.OutstandingDesires {
		if ref.Kind == kind && ref.Name == name {
			return true
		}
	}
	return false
}

// credentialDesireName builds a stable per-credential desire name from a
// prefix and the credential's 16-char suffix.
func credentialDesireName(prefix, credName string) string {
	return prefix + "-" + credName
}

// targetItemFor extracts a ResourceReference identifying the k8s object
// the desire targets on the MC.
func targetItemFor(o runtime.Object) kubeapplier.ResourceReference {
	gvk := o.GetObjectKind().GroupVersionKind()
	objMeta, _ := o.(metaObject)
	var ns, n string
	if objMeta != nil {
		ns, n = objMeta.GetNamespace(), objMeta.GetName()
	}
	return kubeapplier.ResourceReference{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Resource:  kindToResource(gvk.Kind),
		Namespace: ns,
		Name:      n,
	}
}

type metaObject interface {
	GetNamespace() string
	GetName() string
}

func kindToResource(kind string) string {
	switch kind {
	case "CertificateSigningRequest":
		return "certificatesigningrequests"
	case "CertificateSigningRequestApproval":
		return "certificatesigningrequestapprovals"
	case "CertificateRevocationRequest":
		return "certificaterevocationrequests"
	case "ClusterRole":
		return "clusterroles"
	case "ClusterRoleBinding":
		return "clusterrolebindings"
	case "Role":
		return "roles"
	case "RoleBinding":
		return "rolebindings"
	}
	return kind
}

// hostedClusterNamespace mirrors the convention used by the per-cluster
// HostedCluster ReadDesire creator: "ocm-<envID>-<csClusterID>".
func hostedClusterNamespace(envIdentifier, csClusterID string) string {
	return fmt.Sprintf("ocm-%s-%s", envIdentifier, csClusterID)
}

// HypershiftSignerNameFmt is the cluster-service-equivalent signer name
// the HyperShift control-plane-pki-operator watches for. The %s is the
// cluster's HCP namespace. Renaming requires a HyperShift change.
const HypershiftSignerNameFmt = "hypershift.openshift.io/%s.customer-break-glass"

const defaultUsername = "customer-cluster-admin"
