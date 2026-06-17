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
	cooldownChecker      controllerutil.CooldownChecker
	clusterLister        listers.ClusterLister
	credentialLister     listers.SystemAdminCredentialLister
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
	hostedClusterNSEnvID string
}

func NewCredentialDesiresCreatorController(
	clusterLister listers.ClusterLister,
	credentialLister listers.SystemAdminCredentialLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &credentialDesiresCreator{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:        clusterLister,
		credentialLister:     credentialLister,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
		hostedClusterNSEnvID: hostedClusterNamespaceEnvIdentifier,
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
	if cred.Status.Phase != api.SystemAdminCredentialPhaseRequested {
		return nil
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
	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterRID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get/create SPC: %w", err))
	}
	mcRID := spc.Status.ManagementClusterResourceID
	if mcRID == nil {
		return nil
	}
	kaClient := c.kubeApplierDBClients.For(ctx, mcRID)
	if kaClient == nil {
		return nil
	}

	csClusterID := cluster.ServiceProviderProperties.ClusterServiceID.ID()
	hcpNamespace := hostedClusterNamespace(c.hostedClusterNSEnvID, csClusterID)
	signerName := fmt.Sprintf(HypershiftSignerNameFmt, hcpNamespace)
	credName := cred.GetResourceID().Name
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
	rbacGive, err := systemadmincredential.BuildRBACGiveCSRPerm(clusterRID, credName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build RBAC give-csr-perm: %w", err))
	}
	rbacCSRA, err := systemadmincredential.BuildRBACCSRA(clusterRID, credName, hcpNamespace)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build RBAC csra-perm: %w", err))
	}
	rbacRev, err := systemadmincredential.BuildRBACRevocation(clusterRID, credName, hcpNamespace)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build RBAC revocation-perm: %w", err))
	}

	type applyPlan struct {
		nameSegment string
		obj         runtime.Object
	}
	plans := []applyPlan{
		{credentialDesireName(systemadmincredential.CSRNamePrefix, credName), csrObj},
		{credentialDesireName(systemadmincredential.CSRANamePrefix, credName), csraObj},
	}
	for _, o := range append(append(rbacGive, rbacCSRA...), rbacRev...) {
		plans = append(plans, applyPlan{rbacPlanName(o, credName), o})
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

// rbacPlanName derives a unique ApplyDesire name for an RBAC object in
// one of the three bundles. Objects in the same bundle (Role+Binding)
// must not collide on name, so we suffix by the object's k8s Kind.
func rbacPlanName(o runtime.Object, credName string) string {
	gvk := o.GetObjectKind().GroupVersionKind()
	switch gvk.Kind {
	case "ClusterRole":
		return credentialDesireName(systemadmincredential.RBACGiveCSRPermNamePrefix, credName) + "-clusterrole"
	case "ClusterRoleBinding":
		return credentialDesireName(systemadmincredential.RBACGiveCSRPermNamePrefix, credName) + "-clusterrolebinding"
	case "Role":
		return credentialDesireName("system-admin-credential-role", credName) + "-" + objNameSuffix(o)
	case "RoleBinding":
		return credentialDesireName("system-admin-credential-rolebinding", credName) + "-" + objNameSuffix(o)
	}
	return credentialDesireName("system-admin-credential-unknown", credName)
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

func objNameSuffix(o runtime.Object) string {
	if m, ok := o.(metaObject); ok {
		return m.GetName()
	}
	return ""
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
