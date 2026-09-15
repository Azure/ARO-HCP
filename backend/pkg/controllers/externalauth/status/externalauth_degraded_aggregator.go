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
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// CACertificateExpiredConditionType is the user-facing condition type set when the
	// CA certificate has expired. Written to Status.UserFacingConditions only while expired;
	CACertificateExpiredConditionType = "CACertificateExpired"

	// CACertificateNotYetValidConditionType is the user-facing condition type set when the
	// CA certificate has a NotBefore in the future. A future NotBefore is accepted at
	// admission time but surfaced here so operators are aware.
	CACertificateNotYetValidConditionType = "CACertificateNotYetValid"
)

// externalAuthDegradedAggregator rolls per-controller Degraded conditions
// up onto HCPOpenShiftClusterExternalAuth.Status.Conditions. See the
// package and clusterDegradedAggregator docs for the overall design.
type externalAuthDegradedAggregator struct {
	externalAuthLister corelisters.ExternalAuthLister
	controllerLister   corelisters.ControllerLister
	resourcesDBClient  corecosmosstorage.ResourcesDBClient
	inertia            statusutils.Inertia
	clock              utilsclock.PassiveClock
	firstObservedBad   *statusutils.FirstObservedBadCache
}

var _ controllerutils.ExternalAuthSyncer = (*externalAuthDegradedAggregator)(nil)

// externalAuthDegradedAggregatorInertia is the inertia config used by the
// external-auth aggregator. Kept independent of the cluster / node-pool
// variants so external-auth-specific controllers can be tuned in
// isolation.
func externalAuthDegradedAggregatorInertia() statusutils.Inertia {
	return statusutils.MustNewInertia(statusutils.DefaultInertia).Inertia
}

// NewExternalAuthDegradedAggregatorController creates a controller that
// aggregates the Degraded condition from every api.Controller under a
// given HCPOpenShiftClusterExternalAuth onto the external auth's
// Status.Conditions.
//
// See NewClusterDegradedAggregatorController for the clock semantics —
// they are identical across the three aggregators.
func NewExternalAuthDegradedAggregatorController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	externalAuthLister corelisters.ExternalAuthLister,
	controllerLister corelisters.ControllerLister,
	informers coreinformers.BackendInformers,
	clock utilsclock.PassiveClock,
) controllerutils.Controller {
	if clock == nil {
		clock = utilsclock.RealClock{}
	}
	syncer := &externalAuthDegradedAggregator{
		externalAuthLister: externalAuthLister,
		controllerLister:   controllerLister,
		resourcesDBClient:  resourcesDBClient,
		inertia:            externalAuthDegradedAggregatorInertia(),
		clock:              clock,
		firstObservedBad:   statusutils.NewFirstObservedBadCache(clock),
	}
	return controllerutils.NewExternalAuthWatchingController(
		"ExternalAuthDegradedAggregator",
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *externalAuthDegradedAggregator) SyncOnce(ctx context.Context, key controllerutils.HCPExternalAuthKey) error {
	existing, err := c.externalAuthLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ExternalAuth from cache: %w", err))
	}

	controllers, err := c.controllerLister.ListForExternalAuth(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPExternalAuthName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list Controllers from cache: %w", err))
	}

	// With report-only-degraded collection an all-healthy external auth produces
	// zero sources and UnionCondition returns the good default (Degraded=False/AsExpected).
	sources := statusutils.CollectDegradedConditions(
		controllers, statusutils.ConditionsOfKnown, "", c.firstObservedBad)
	aggregated := statusutils.UnionCondition(
		statusutils.DegradedConditionType,
		metav1.ConditionFalse,
		c.inertia,
		c.clock.Now(),
		sources...,
	)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, aggregated)

	// CACertificateExpiry is user-facing only when the CA has expired.
	// CACertificateNotYetValid is user-facing only when the CA has a start date in the future.
	expiryCond, notYetValidCond := c.getCaCertValidityConditions(existing.Properties.Issuer.CA)
	if expiryCond != nil {
		apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, *expiryCond)
	} else {
		apimeta.RemoveStatusCondition(&replacement.Status.UserFacingConditions, CACertificateExpiredConditionType)
	}
	if notYetValidCond != nil {
		apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, *notYetValidCond)
	} else {
		apimeta.RemoveStatusCondition(&replacement.Status.UserFacingConditions, CACertificateNotYetValidConditionType)
	}

	conditionsChanged := !equality.Semantic.DeepEqual(existing.Status.Conditions, replacement.Status.Conditions)
	userFacingChanged := !equality.Semantic.DeepEqual(existing.Status.UserFacingConditions, replacement.Status.UserFacingConditions)
	if !conditionsChanged && !userFacingChanged {
		return nil
	}

	externalAuthCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName)
	_, err = externalAuthCRUD.Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	}
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ExternalAuth: %w", err))
	}
	return nil
}

// caCertValidityConditions inspects all CERTIFICATE PEM blocks in ca and returns
// two conditions: expiryCond (CACertificateExpired) and notYetValidCond (CACertificateNotYetValid).
// Each is nil when not applicable. Both are derived from a single pass over the PEM bundle:
//   - expiryCond is set when any cert has expired;
//   - notYetValidCond is set when any cert has a start date in the future;
//
// Missing, empty, or entirely unparseable input causes both to return nil.
func (c *externalAuthDegradedAggregator) getCaCertValidityConditions(ca string) (expiryCond, notYetValidCond *metav1.Condition) {
	if ca == "" {
		return nil, nil
	}

	var expired []*x509.Certificate
	var notYetValid []*x509.Certificate
	now := c.clock.Now()

	rest := []byte(ca)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if now.After(cert.NotAfter) {
			expired = append(expired, cert)
		}
		if now.Before(cert.NotBefore) {
			notYetValid = append(notYetValid, cert)
		}
	}

	if len(expired) > 0 {
		expiryCond = &metav1.Condition{
			Type:    CACertificateExpiredConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  "Expired",
			Message: formatCACertExpiredMessage(expired),
		}
	}

	if len(notYetValid) > 0 {
		notYetValidCond = &metav1.Condition{
			Type:    CACertificateNotYetValidConditionType,
			Status:  metav1.ConditionTrue,
			Reason:  "NotYetValid",
			Message: formatCACertNotYetValidMessage(notYetValid),
		}
	}

	return expiryCond, notYetValidCond
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
