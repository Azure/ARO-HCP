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

package status

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// ExternalAuthOIDCCACertValidityControllerName is the controller name used for
	// metrics labels, ctx values, log fields, and the Controller document name.
	ExternalAuthOIDCCACertValidityControllerName = "ExternalAuthOIDCCACertValidityController"
)

// externalAuthOIDCCACertValidityController reads the CA certificate PEM from the
// ExternalAuth and produces  OIDCIssuerCACertificateInvalid
// condition on ServiceProviderExternalAuth.Status.Conditions:
//   - True when any cert in the bundle has expired or has a NotBefore in the future.
//   - False when all certs are within their validity window (or the CA is empty).
//
// This controller is independent of HostedCluster state. It only inspects the
// CA PEM stored on the ExternalAuth document. The
// ExternalAuthUserFacingDegradedAggregator reads this condition (alongside
// OIDCClientsDegraded) and folds it into the single user-facing Degraded
// condition on ExternalAuth.Status.UserFacingConditions.
//
// IMPORTANT: Messages written to this condition are surfaced verbatim to
// end users through the ARM API. Never include internal implementation
// details or cluster infrastructure paths in condition messages.
type externalAuthOIDCCACertValidityController struct {
	externalAuthLister                corelisters.ExternalAuthLister
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister
	resourcesDBClient                 corecosmosstorage.ResourcesDBClient
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthOIDCCACertValidityController)(nil)

// NewExternalAuthOIDCCACertValidityController creates a controller that inspects
// the CA certificate PEM on the ExternalAuth and writes an
// OIDCIssuerCACertificateInvalid condition onto
// ServiceProviderExternalAuth.Status.Conditions. The
// ExternalAuthUserFacingDegradedAggregator is responsible for promoting it
// into the user-facing Degraded condition.
//
// Messages written to this condition are surfaced to end users through the
// ARM API. Ensure all messages are safe for external consumption.
func NewExternalAuthOIDCCACertValidityController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	externalAuthLister corelisters.ExternalAuthLister,
	serviceProviderExternalAuthLister corelisters.ServiceProviderExternalAuthLister,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &externalAuthOIDCCACertValidityController{
		externalAuthLister:                externalAuthLister,
		serviceProviderExternalAuthLister: serviceProviderExternalAuthLister,
		resourcesDBClient:                 resourcesDBClient,
	}
	return controllerutils.NewExternalAuthWatchingController(
		ExternalAuthOIDCCACertValidityControllerName,
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *externalAuthOIDCCACertValidityController) needsWork(externalAuth *coreapi.ExternalAuth) bool {
	return externalAuth.ServiceProviderProperties.DeletionTimestamp == nil
}

func (c *externalAuthOIDCCACertValidityController) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	existing, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ExternalAuth from cache: %w", err))
	}

	if !c.needsWork(existing) {
		return nil
	}

	serviceProviderExternalAuth, err := c.serviceProviderExternalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderExternalAuth from cache: %w", err))
	}

	caCertInvalidCondition, err := c.getCACertValidityCondition(existing.Properties.Issuer.CA)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to evaluate CA certificate validity: %w", err))
	}

	replacement := serviceProviderExternalAuth.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, caCertInvalidCondition)

	if equality.Semantic.DeepEqual(serviceProviderExternalAuth.Status.Conditions, replacement.Status.Conditions) {
		return nil
	}

	serviceProviderExternalAuthCRUD := c.resourcesDBClient.ServiceProviderExternalAuths(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	_, err = serviceProviderExternalAuthCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderExternalAuth: %w", err))
	}
	return nil
}

// getCACertValidityCondition inspects all CERTIFICATE PEM blocks in ca and returns
// a single OIDCIssuerCACertificateInvalid condition:
//   - True when any cert has expired or has a NotBefore in the future.
//   - False when all certs are within their validity window (or CA is empty/unparseable).
//
// When both expired and not-yet-valid certs exist, the message lists both.
// Returns an error if any PEM block with type CERTIFICATE cannot be parsed.
func (c *externalAuthOIDCCACertValidityController) getCACertValidityCondition(ca string) (metav1.Condition, error) {
	condition := metav1.Condition{
		Type:    coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
		Status:  metav1.ConditionFalse,
		Reason:  coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected,
		Message: coreapi.ExternalAuthMessageOIDCCACertificateValid,
	}

	if ca == "" {
		return condition, nil
	}

	var expiredCACerts []*x509.Certificate
	var notYetValidCACerts []*x509.Certificate
	now := time.Now()

	rest := []byte(ca)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return metav1.Condition{}, fmt.Errorf("unexpected PEM block type: %s", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return metav1.Condition{}, fmt.Errorf("failed to parse CA certificate PEM block: %w", err)
		}
		if now.After(cert.NotAfter) {
			expiredCACerts = append(expiredCACerts, cert)
		}
		if now.Before(cert.NotBefore) {
			notYetValidCACerts = append(notYetValidCACerts, cert)
		}
	}

	if len(expiredCACerts) == 0 && len(notYetValidCACerts) == 0 {
		return condition, nil
	}

	condition.Status = metav1.ConditionTrue
	condition.Reason = coreapi.ExternalAuthOIDCCACertificateDegradationReason

	var messageParts []string
	if len(expiredCACerts) > 0 {
		messageParts = append(messageParts, formatCACertExpiredMessage(expiredCACerts))
	}
	if len(notYetValidCACerts) > 0 {
		messageParts = append(messageParts, formatCACertNotYetValidMessage(notYetValidCACerts))
	}
	condition.Message = strings.Join(messageParts, "; ")

	return condition, nil
}

func formatCACertExpiredMessage(certs []*x509.Certificate) string {
	parts := make([]string, len(certs))
	for i, cert := range certs {
		parts[i] = fmt.Sprintf("CN=%s (NotAfter %s)", cert.Subject.CommonName, cert.NotAfter.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("%d CA certificate(s) in the issuer bundle have expired: %s", len(certs), strings.Join(parts, "; "))
}

func formatCACertNotYetValidMessage(certs []*x509.Certificate) string {
	parts := make([]string, len(certs))
	for i, cert := range certs {
		parts[i] = fmt.Sprintf("CN=%s (NotBefore %s)", cert.Subject.CommonName, cert.NotBefore.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("%d CA certificate(s) in the issuer bundle are not yet valid: %s", len(certs), strings.Join(parts, "; "))
}
