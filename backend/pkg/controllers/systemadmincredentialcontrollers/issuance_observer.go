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
	"encoding/base64"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	certificatesv1 "k8s.io/api/certificates/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// issuanceObserver is controller #3. A ClusterWatchingController that
// fires on per-credential CSR ReadDesire events; when the mirrored CSR
// reports status.certificate it flips the credential's Phase to Issued
// and copies the cert into Status.SignedCertificate. On
// CertificateDenied / Failed it flips Phase to Failed.
//
// It does not run the per-credential teardown — that is controller #7's
// job, which also keys off Phase moving to Issued/Failed.
type issuanceObserver struct {
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

func NewIssuanceObserverController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &issuanceObserver{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient: resourcesDBClient,
		readDesireLister:  readDesireLister,
	}
	return controllerutils.NewClusterWatchingController(
		"SystemAdminCredentialIssuanceObserver",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *issuanceObserver) CooldownChecker() controllerutil.CooldownChecker { return c.cooldownChecker }

func (c *issuanceObserver) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	credentialsCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		SystemAdminCredentials(key.HCPClusterName)
	iter, err := credentialsCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("list credentials: %w", err))
	}

	for _, credential := range iter.Items(ctx) {
		if credential == nil {
			continue
		}
		// Only credentials still in Requested are eligible for an
		// issuance transition. Issued/Failed/Revoked are terminal as
		// far as this controller is concerned.
		if credential.Status.Phase != api.SystemAdminCredentialPhaseRequested {
			continue
		}
		credName := credential.GetResourceID().Name

		csr, err := maestrohelpers.GetCachedCSRForSystemAdminCredential(
			ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, credName)
		if err != nil {
			return utils.TrackError(fmt.Errorf("get mirrored CSR %q: %w", credName, err))
		}
		if csr == nil {
			// kube-applier has not yet observed the CSR — no-op.
			continue
		}

		if len(csr.Status.Certificate) > 0 {
			replacement := credential.DeepCopy()
			replacement.Status.Phase = api.SystemAdminCredentialPhaseIssued
			replacement.Status.SignedCertificate = base64.StdEncoding.EncodeToString(csr.Status.Certificate)
			if _, err := credentialsCRUD.Replace(ctx, replacement, nil); err != nil {
				return utils.TrackError(fmt.Errorf("flip credential to Issued: %w", err))
			}
			logger.Info("credential issued", "credential", credName)
			continue
		}

		if denied := csrDenied(csr); denied != "" {
			replacement := credential.DeepCopy()
			replacement.Status.Phase = api.SystemAdminCredentialPhaseFailed
			meta := replacement.Status.Conditions
			meta = append(meta, metav1.Condition{
				Type:               "CSRDenied",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(time.Now()),
				Reason:             denied,
				Message:            "HyperShift signer denied the CertificateSigningRequest",
			})
			replacement.Status.Conditions = meta
			if _, err := credentialsCRUD.Replace(ctx, replacement, nil); err != nil {
				return utils.TrackError(fmt.Errorf("flip credential to Failed: %w", err))
			}
			logger.Info("credential signing denied", "credential", credName, "reason", denied)
		}
	}
	return iter.GetError()
}

func csrDenied(csr *certificatesv1.CertificateSigningRequest) string {
	for _, cond := range csr.Status.Conditions {
		if cond.Type == certificatesv1.CertificateDenied && cond.Status == "True" {
			if cond.Reason != "" {
				return cond.Reason
			}
			return "Denied"
		}
		if cond.Type == certificatesv1.CertificateFailed && cond.Status == "True" {
			if cond.Reason != "" {
				return cond.Reason
			}
			return "Failed"
		}
	}
	return ""
}
