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
	// CACertificateExpiryConditionType is the user-facing condition type set when the issuer
	// CA certificate has expired. Written to Status.UserFacingConditions only while expired;
	CACertificateExpiryConditionType = "CACertificateExpiry"
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

	aggregated := statusutils.UnionCondition(
		statusutils.DegradedConditionType,
		metav1.ConditionFalse,
		c.inertia,
		c.clock.Now(),
		statusutils.CollectDegradedConditions(controllers, c.firstObservedBad)...,
	)

	replacement := existing.DeepCopy()
	apimeta.SetStatusCondition(&replacement.Status.Conditions, aggregated)

	// CACertificateExpiry is user-facing only when the CA has expired.
	if caCond := caCertExpiryCondition(existing.Properties.Issuer.CA, c.clock.Now()); caCond != nil {
		apimeta.SetStatusCondition(&replacement.Status.UserFacingConditions, *caCond)
	} else {
		apimeta.RemoveStatusCondition(&replacement.Status.UserFacingConditions, CACertificateExpiryConditionType)
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

// caCertExpiryCondition inspects all CERTIFICATE PEM blocks in ca and returns a
// CACertificateExpiry condition only when the CA has expired. Healthy, missing, or
// unparseable CAs return nil so the condition is omitted from user-facing status.
// When a bundle contains multiple certs the cert with the earliest NotAfter
// drives the condition.
func caCertExpiryCondition(ca string, now time.Time) *metav1.Condition {
	if ca == "" {
		return nil
	}

	// Walk every PEM block; only CERTIFICATE blocks are relevant.
	var earliest *x509.Certificate
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
		if earliest == nil || cert.NotAfter.Before(earliest.NotAfter) {
			earliest = cert
		}
	}

	if earliest == nil {
		return nil
	}

	// If the CA is not expired, return nil.
	if !now.After(earliest.NotAfter) {
		return nil
	}

	notAfter := earliest.NotAfter.UTC().Format(time.RFC3339)
	return &metav1.Condition{
		Type:    CACertificateExpiryConditionType,
		Status:  metav1.ConditionTrue,
		Reason:  "Expired",
		Message: fmt.Sprintf("issuer CA certificate (CN=%s) expired at %s", earliest.Subject.CommonName, notAfter),
	}
}
