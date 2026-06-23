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
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// revocationDesiresCreator is the second of two SystemAdminRevocationWatching
// controllers. It owns the per-revoke kube-applier desires on the
// management cluster:
//
//  1. CertificateRevocationRequest ApplyDesire + ReadDesire (the CRR itself
//     and its mirror).
//  2. The revocation-permission RBAC pair (Role + RoleBinding) so the
//     klusterlet can manage the CRR.
//
// Lifecycle:
//   - On first run, build and Create all four ApplyDesires and the CRR
//     ReadDesire, appending each to Status.OutstandingDesires.
//   - Once the CRR mirror reports PreviousCertificatesRevoked=True, drive
//     the teardown: issue DeleteDesires for each ApplyDesire, wait for
//     each kube-applier Successful=True, drop the Apply/Delete Cosmos
//     docs, drop the ReadDesire, and prune the matching refs.
//   - When OutstandingDesires becomes empty, set the condition
//     SystemAdminRevocationCompleteConditionType to True so the
//     operationRevokeCredentialsPoll can drive the ARM op to Succeeded.
type revocationDesiresCreator struct {
	cooldownChecker              controllerutil.CooldownChecker
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	resourcesDBClient            database.ResourcesDBClient
	kubeApplierDBClients         database.KubeApplierDBClients
	readDesireLister             dblisters.ReadDesireLister
	hostedClusterNSEnvID         string
}

func NewRevocationDesiresCreatorController(
	clusterLister listers.ClusterLister,
	serviceProviderClusterLister listers.ServiceProviderClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	readDesireLister dblisters.ReadDesireLister,
	activeOperationLister listers.ActiveOperationLister,
	backendInformers informers.BackendInformers,
	hostedClusterNamespaceEnvIdentifier string,
) controllerutils.Controller {
	syncer := &revocationDesiresCreator{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		readDesireLister:             readDesireLister,
		hostedClusterNSEnvID:         hostedClusterNamespaceEnvIdentifier,
	}
	return controllerutils.NewSystemAdminRevocationWatchingController(
		"SystemAdminRevocationDesiresCreator",
		resourcesDBClient,
		backendInformers,
		30*time.Second,
		syncer,
	)
}

func (c *revocationDesiresCreator) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *revocationDesiresCreator) SyncOnce(ctx context.Context, key controllerutils.HCPSystemAdminRevocationKey) error {
	logger := utils.LoggerFromContext(ctx)

	revocationCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminRevocations(key.HCPClusterName)
	revocation, err := revocationCRUD.Get(ctx, key.HCPSystemAdminRevocationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get SystemAdminRevocation: %w", err))
	}
	// Already complete: nothing to do.
	if isRevocationConditionTrue(revocation, api.SystemAdminRevocationCompleteConditionType) {
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

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it. SystemAdminRevocationWatchingController
		// does not watch the ServiceProviderCluster informer (an SPC arrival
		// can't be walked down to a specific revocation), so the next attempt
		// happens on the controller's resync or the next SystemAdminRevocation
		// event for this revocation.
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

	clusterRID := cluster.GetResourceID()
	revokeSuffix := key.HCPSystemAdminRevocationName
	hcpNamespace := hostedClusterNamespace(c.hostedClusterNSEnvID, cluster.ServiceProviderProperties.ClusterServiceID.ID())

	// Check the CRR mirror to decide whether to keep building desires or
	// switch to teardown.
	crr, err := maestrohelpers.GetCachedCertificateRevocationRequestForCluster(
		ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, revokeSuffix)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get mirrored CRR: %w", err))
	}
	if crr != nil && crrPreviousCertificatesRevoked(crr) {
		return c.runRevocationTeardown(ctx, logger, kaClient, revocation, revocationCRUD)
	}

	// Creation branch: ensure the four ApplyDesires + the CRR ReadDesire exist.
	crrObj, err := systemadmincredential.BuildRevocationRequest(clusterRID, revokeSuffix, hcpNamespace)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build CRR: %w", err))
	}
	revocationPermRole, revocationPermRoleBinding, err := systemadmincredential.BuildRBACRevocation(clusterRID, revokeSuffix, hcpNamespace)
	if err != nil {
		return utils.TrackError(fmt.Errorf("build RBAC revocation-perm: %w", err))
	}

	crrName := systemadmincredential.CRRNamePrefix + "-" + revokeSuffix
	revocationRoleName := systemadmincredential.RBACRevocationPermNamePrefix + "-" + revokeSuffix + "-role"
	revocationRoleBindingName := systemadmincredential.RBACRevocationPermNamePrefix + "-" + revokeSuffix + "-rolebinding"

	plans := []struct {
		name string
		obj  runtime.Object
	}{
		{revocationRoleName, revocationPermRole},
		{revocationRoleBindingName, revocationPermRoleBinding},
		{crrName, crrObj},
	}

	applyCRUD, err := kaClient.ApplyDesiresForCluster(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ApplyDesires CRUD: %w", err))
	}
	readCRUD, err := kaClient.ReadDesiresForCluster(clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("get ReadDesires CRUD: %w", err))
	}

	updated := revocation.DeepCopy()
	mutated := false
	for _, plan := range plans {
		if hasOutstandingRevocationDesire(updated, api.SystemAdminRevocationDesireKindApply, plan.name) {
			continue
		}
		raw, err := json.Marshal(plan.obj)
		if err != nil {
			return utils.TrackError(fmt.Errorf("marshal %s: %w", plan.name, err))
		}
		ad := &kubeapplier.ApplyDesire{
			CosmosMetadata: buildScopedDesireMetadata(clusterRID, plan.name, kubeapplier.ApplyDesireResourceTypeName),
			Spec: kubeapplier.ApplyDesireSpec{
				ManagementCluster: mcRID,
				TargetItem:        targetItemFor(plan.obj),
				KubeContent:       &runtime.RawExtension{Raw: raw},
			},
		}
		if _, err := applyCRUD.Create(ctx, ad, nil); err != nil && !database.IsConflictError(err) {
			return utils.TrackError(fmt.Errorf("create ApplyDesire %s: %w", plan.name, err))
		}
		updated.Status.OutstandingDesires = append(updated.Status.OutstandingDesires, api.SystemAdminRevocationDesireRef{
			Kind: api.SystemAdminRevocationDesireKindApply, Name: plan.name,
		})
		mutated = true
	}

	if !hasOutstandingRevocationDesire(updated, api.SystemAdminRevocationDesireKindRead, crrName) {
		rd := &kubeapplier.ReadDesire{
			CosmosMetadata: buildScopedDesireMetadata(clusterRID, crrName, kubeapplier.ReadDesireResourceTypeName),
			Spec: kubeapplier.ReadDesireSpec{
				ManagementCluster: mcRID,
				TargetItem:        targetItemFor(crrObj),
			},
		}
		if _, err := readCRUD.Create(ctx, rd, nil); err != nil && !database.IsConflictError(err) {
			return utils.TrackError(fmt.Errorf("create ReadDesire %s: %w", crrName, err))
		}
		updated.Status.OutstandingDesires = append(updated.Status.OutstandingDesires, api.SystemAdminRevocationDesireRef{
			Kind: api.SystemAdminRevocationDesireKindRead, Name: crrName,
		})
		mutated = true
	}

	if !mutated {
		return nil
	}
	if _, err := revocationCRUD.Replace(ctx, updated, nil); database.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("persist revocation OutstandingDesires: %w", err))
	}
	logger.Info("revocation OutstandingDesires updated", "revocation", revokeSuffix, "count", len(updated.Status.OutstandingDesires))
	return nil
}

// runRevocationTeardown drives the per-revocation desire teardown helper.
// On completion (OutstandingDesires empty) it flips
// SystemAdminRevocationCompleteConditionType to True so
// operationRevokeCredentialsPoll can promote the ARM op to Succeeded.
func (c *revocationDesiresCreator) runRevocationTeardown(
	ctx context.Context,
	logger logr.Logger,
	kaClient database.KubeApplierDBClient,
	revocation *api.SystemAdminRevocation,
	revocationCRUD database.SystemAdminRevocationsCRUD,
) error {
	updated := revocation.DeepCopy()
	remaining, err := teardownRevocationOutstandingDesires(ctx, kaClient, updated)
	if err != nil {
		return utils.TrackError(fmt.Errorf("teardown revocation: %w", err))
	}
	if remaining == 0 {
		apimeta.SetStatusCondition(&updated.Status.Conditions, metav1.Condition{
			Type:    api.SystemAdminRevocationCompleteConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  "AllDesiresTornDown",
			Message: "CRR confirmed PreviousCertificatesRevoked and every kube-applier desire has been torn down.",
		})
	}
	if equalRevocationTeardownState(revocation, updated) {
		return nil
	}
	if _, err := revocationCRUD.Replace(ctx, updated, nil); database.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("persist revocation teardown: %w", err))
	}
	logger.Info("revocation teardown progressed", "outstanding", remaining)
	return nil
}

func hasOutstandingRevocationDesire(revocation *api.SystemAdminRevocation, kind api.SystemAdminRevocationDesireKind, name string) bool {
	for _, ref := range revocation.Status.OutstandingDesires {
		if ref.Kind == kind && ref.Name == name {
			return true
		}
	}
	return false
}

func isRevocationConditionTrue(revocation *api.SystemAdminRevocation, conditionType string) bool {
	c := apimeta.FindStatusCondition(revocation.Status.Conditions, conditionType)
	if c == nil {
		return false
	}
	return c.Status == metav1.ConditionTrue
}

// equalRevocationTeardownState reports whether the teardown-relevant
// fields (OutstandingDesires + the RevocationComplete condition) are
// identical between the cached revocation and the post-sweep copy.
func equalRevocationTeardownState(a, b *api.SystemAdminRevocation) bool {
	if len(a.Status.OutstandingDesires) != len(b.Status.OutstandingDesires) {
		return false
	}
	for i := range a.Status.OutstandingDesires {
		if a.Status.OutstandingDesires[i] != b.Status.OutstandingDesires[i] {
			return false
		}
	}
	aCond := apimeta.FindStatusCondition(a.Status.Conditions, api.SystemAdminRevocationCompleteConditionType)
	bCond := apimeta.FindStatusCondition(b.Status.Conditions, api.SystemAdminRevocationCompleteConditionType)
	if (aCond == nil) != (bCond == nil) {
		return false
	}
	if aCond != nil && aCond.Status != bCond.Status {
		return false
	}
	return true
}
