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
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	utilsclock "k8s.io/utils/clock"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
)

// operationRevokeCredentialsPoll is controller #5. Three-phase state
// machine:
//
//	R-1 — wait for CRR PreviousCertificatesRevoked=True. If denied/
//	      failed, surface ProvisioningStateFailed.
//	R-2 — drive per-credential teardown (only credentials whose
//	      Phase=Requested still have OutstandingDesires; Phase=Issued
//	      were already swept by controller #7), plus the CRR
//	      ApplyDesire/ReadDesire teardown.
//	R-3 — clear cluster sentinel, flip operation to Succeeded.
//
// Each reconcile reads everything fresh; never assumes prior intent.
type operationRevokeCredentialsPoll struct {
	clock                utilsclock.PassiveClock
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
	readDesireLister     dblisters.ReadDesireLister
	notificationClient   *http.Client
}

func NewOperationRevokeCredentialsPollController(
	clock utilsclock.PassiveClock,
	clusterLister listers.ClusterLister,
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	readDesireLister dblisters.ReadDesireLister,
	notificationClient *http.Client,
	activeOperationInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &operationRevokeCredentialsPoll{
		clock:                clock,
		clusterLister:        clusterLister,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
		readDesireLister:     readDesireLister,
		notificationClient:   notificationClient,
	}
	return operationcontrollers.NewGenericOperationController(
		"SystemAdminCredentialRevokePoll",
		syncer,
		10*time.Second,
		activeOperationInformer,
		resourcesDBClient,
	)
}

func (c *operationRevokeCredentialsPoll) ShouldProcess(ctx context.Context, op *api.Operation) bool {
	if op.Status.IsTerminal() {
		return false
	}
	if op.Request != database.OperationRequestRevokeCredentials {
		return false
	}
	if op.Status != arm.ProvisioningStateDeleting {
		return false
	}
	return true
}

func (c *operationRevokeCredentialsPoll) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	op, err := c.resourcesDBClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get operation: %w", err)
	}
	if !c.ShouldProcess(ctx, op) {
		return nil
	}

	clusterRID := op.ExternalID
	cluster, err := c.clusterLister.Get(ctx, clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name)
	if database.IsNotFoundError(err) {
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateSucceeded, nil, c.notificationClient)
	}
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}

	spc, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterRID)
	if err != nil {
		return fmt.Errorf("get/create SPC: %w", err)
	}
	if spc.Status.ManagementClusterResourceID == nil {
		return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateSucceeded, nil, c.notificationClient)
	}
	kaClient := c.kubeApplierDBClients.For(ctx, spc.Status.ManagementClusterResourceID)
	if kaClient == nil {
		return nil
	}

	revokeSuffix := systemadmincredential.RevokeOpSuffix(op.OperationID.Name)

	// === Phase R-1 ===
	crr, err := maestrohelpers.GetCachedCertificateRevocationRequestForCluster(
		ctx, c.readDesireLister, clusterRID.SubscriptionID, clusterRID.ResourceGroupName, clusterRID.Name, revokeSuffix)
	if err != nil {
		return fmt.Errorf("get mirrored CRR: %w", err)
	}
	if crr == nil {
		// kube-applier has not yet observed the CRR — wait.
		return nil
	}
	if !crrPreviousCertificatesRevoked(crr) {
		if reason := crrFailureReason(crr); reason != "" {
			return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateFailed, &arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInternalServerError,
				Message: "CertificateRevocationRequest failed: " + reason,
			}, c.notificationClient)
		}
		// Still draining.
		return nil
	}

	// === Phase R-2 ===
	credentialsCRUD := c.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).
		SystemAdminCredentials(clusterRID.Name)
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("list credentials: %w", err)
	}
	stillDraining := false
	for _, cred := range iter.Items(ctx) {
		if cred == nil {
			continue
		}
		// Only AwaitingRevocation needs work here; everything else
		// (already Revoked, Issued, Failed) is handled by #7/#9.
		if cred.Status.Phase != api.SystemAdminCredentialPhaseAwaitingRevocation {
			continue
		}
		remaining, err := teardownCredentialOutstandingDesires(ctx, kaClient, c.resourcesDBClient, cred)
		if err != nil {
			return fmt.Errorf("teardown credential %q: %w", cred.GetResourceID().Name, err)
		}
		if remaining > 0 {
			// Persist updated OutstandingDesires and keep waiting.
			if _, err := credentialsCRUD.Replace(ctx, cred, nil); err != nil {
				return fmt.Errorf("persist credential teardown progress: %w", err)
			}
			stillDraining = true
			continue
		}
		// Per-credential teardown done — flip Phase to Revoked, zero
		// out the private key, set RevokedAt.
		cred.Status.Phase = api.SystemAdminCredentialPhaseRevoked
		cred.Spec.PrivateKeyPEM = ""
		now := metav1.NewTime(c.clock.Now())
		cred.Status.RevokedAt = &now
		if _, err := credentialsCRUD.Replace(ctx, cred, nil); err != nil {
			return fmt.Errorf("flip credential %q to Revoked: %w", cred.GetResourceID().Name, err)
		}
	}
	if err := iter.GetError(); err != nil {
		return fmt.Errorf("iterate credentials: %w", err)
	}

	// CRR teardown. We treat the CRR as a single-credential-like
	// teardown bucket by injecting a synthetic "outstanding" entry on
	// the operation's pseudo-credential. Concretely we just call the
	// same helper with a tiny ad-hoc credential containing only the
	// CRR's refs.
	crrName := systemadmincredential.CRRNamePrefix + "-" + revokeSuffix
	crrSyntheticCred := &api.SystemAdminCredential{
		CosmosMetadata: api.CosmosMetadata{ResourceID: clusterRID}, // parent-only — desire teardown uses Parent
		Status: api.SystemAdminCredentialStatus{
			OutstandingDesires: []api.SystemAdminCredentialDesireRef{
				{Kind: api.SystemAdminCredentialDesireKindApply, Name: crrName},
				{Kind: api.SystemAdminCredentialDesireKindRead, Name: crrName},
			},
		},
	}
	crrRemaining, err := teardownCredentialOutstandingDesires(ctx, kaClient, c.resourcesDBClient, crrSyntheticCred)
	if err != nil {
		return fmt.Errorf("teardown CRR: %w", err)
	}
	if crrRemaining > 0 {
		stillDraining = true
	}

	if stillDraining {
		return nil
	}

	// === Phase R-3 ===
	if cluster.ServiceProviderProperties.RevokeCredentialsOperationID == op.OperationID.Name {
		updated := cluster.DeepCopy()
		updated.ServiceProviderProperties.RevokeCredentialsOperationID = ""
		if _, err := c.resourcesDBClient.HCPClusters(clusterRID.SubscriptionID, clusterRID.ResourceGroupName).Replace(ctx, updated, nil); err != nil {
			return fmt.Errorf("clear RevokeCredentialsOperationID: %w", err)
		}
	}
	return patchOperationStatus(ctx, c.clock, c.resourcesDBClient, op, arm.ProvisioningStateSucceeded, nil, c.notificationClient)
}

func crrPreviousCertificatesRevoked(crr *certificatesv1alpha1.CertificateRevocationRequest) bool {
	for _, c := range crr.Status.Conditions {
		if c.Type == certificatesv1alpha1.PreviousCertificatesRevokedType && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

func crrFailureReason(crr *certificatesv1alpha1.CertificateRevocationRequest) string {
	for _, c := range crr.Status.Conditions {
		if c.Type == "Failed" && c.Status == metav1.ConditionTrue {
			if c.Reason != "" {
				return c.Reason
			}
			return "Failed"
		}
	}
	return ""
}
