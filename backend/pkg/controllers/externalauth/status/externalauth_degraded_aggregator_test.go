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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

// mustMakeCAPEM generates a self-signed CA certificate valid from notBefore to
// notAfter and returns it as a PEM-encoded string. The certificate's Subject CN
// is set to cn. Fails the test on any generation error (test helper).
func mustMakeCAPEM(t *testing.T, cn string, notBefore, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "generate test CA key")
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err, "create test CA certificate")
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// newTestExternalAuthForAggregator builds a minimal
// HCPOpenShiftClusterExternalAuth suitable for the aggregator tests.
func newTestExternalAuthForAggregator(opts ...func(*coreapi.HCPOpenShiftClusterExternalAuth)) *coreapi.HCPOpenShiftClusterExternalAuth {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName,
	))
	ea := &coreapi.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ProxyResource: coreapi.ProxyResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: statusutils.TestExternalAuthName,
				Type: resourceID.ResourceType.String(),
			},
		},
	}
	for _, opt := range opts {
		opt(ea)
	}
	return ea
}

func TestExternalAuthDegradedAggregator_SyncOnce(t *testing.T) {
	parentResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName +
			"/externalAuths/" + statusutils.TestExternalAuthName,
	))
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	thirtySecondInertia := statusutils.MustNewInertia(30 * time.Second).Inertia
	fiveMinuteOverrideInertia := statusutils.MustNewInertia(
		30*time.Second,
		statusutils.InertiaController{ControllerNameMatcher: regexp.MustCompile(`^SlowController$`), Duration: 5 * time.Minute},
	).Inertia

	tests := []struct {
		name string

		controllers []*coreapi.Controller
		inertia     statusutils.Inertia
		// initialConditions, if set, is layered onto the external auth before
		// SyncOnce runs.
		initialConditions []metav1.Condition

		expectStatus  metav1.ConditionStatus
		expectReason  string
		expectMessage string
	}{
		{
			name:          "no controllers under the external auth -> False/AsExpected (all healthy)",
			controllers:   nil,
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "All is well",
		},
		{
			name: "all controllers healthy -> no degraded sources -> False/AsExpected",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			inertia: thirtySecondInertia,
			// Healthy controllers are not emitted as sources, so UnionCondition
			// sees zero sources and reports the good default (False/AsExpected).
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "All is well",
		},
		{
			name: "bad controller within 30s inertia stays hidden",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 5*time.Second),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "AController: boom",
		},
		{
			name: "bad controller past 30s inertia flips aggregate",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Minute),
			},
			inertia:       thirtySecondInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "per-controller override delays SlowController",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "settling", 2*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "SlowController: settling",
		},
		{
			name: "per-controller override: SlowController past 5m flips",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "SlowController", metav1.ConditionTrue, "Failed", "stuck", 6*time.Minute),
			},
			inertia:       fiveMinuteOverrideInertia,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "SlowController_Failed",
			expectMessage: "SlowController: stuck",
		},
		{
			name: "nil inertia propagates immediately",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionTrue, "Failed", "boom", 1*time.Second),
			},
			inertia:       nil,
			expectStatus:  metav1.ConditionTrue,
			expectReason:  "AController_Failed",
			expectMessage: "AController: boom",
		},
		{
			name: "no-op when conditions unchanged",
			controllers: []*coreapi.Controller{
				statusutils.ControllerUnder(parentResourceID, "AController", metav1.ConditionFalse, "NoErrors", "fine", 1*time.Minute),
			},
			inertia: thirtySecondInertia,
			// The single healthy controller yields zero sources -> False/AsExpected;
			// pre-seeding that exercises the no-op (skip Replace) path.
			initialConditions: []metav1.Condition{
				{
					Type:    statusutils.DegradedConditionType,
					Status:  metav1.ConditionFalse,
					Reason:  "AsExpected",
					Message: "All is well",
				},
			},
			expectStatus:  metav1.ConditionFalse,
			expectReason:  "AsExpected",
			expectMessage: "All is well",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			existing := newTestExternalAuthForAggregator(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				if len(tc.initialConditions) > 0 {
					ea.Status.Conditions = append([]metav1.Condition{}, tc.initialConditions...)
				}
			})
			parentCluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, existing}
			for _, ctrl := range tc.controllers {
				seed = append(seed, ctrl)
			}
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			clock := clocktesting.NewFakePassiveClock(statusutils.FixedNow)
			syncer := &externalAuthDegradedAggregator{
				externalAuthLister: &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				controllerLister:   &corelistertesting.DBControllerLister{ResourcesDBClient: mockDB},
				resourcesDBClient:  mockDB,
				inertia:            tc.inertia,
				clock:              clock,
				firstObservedBad:   statusutils.NewFirstObservedBadCache(clock),
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
				SubscriptionID:      statusutils.TestSubscriptionID,
				ResourceGroupName:   statusutils.TestResourceGroupName,
				HCPClusterName:      statusutils.TestClusterName,
				HCPExternalAuthName: statusutils.TestExternalAuthName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName).ExternalAuth(statusutils.TestClusterName).Get(ctx, statusutils.TestExternalAuthName)
			require.NoError(t, err)

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, statusutils.DegradedConditionType)
			require.NotNil(t, cond, "aggregator must set the Degraded condition on the external auth")
			assert.Equal(t, tc.expectStatus, cond.Status, "status")
			assert.Equal(t, tc.expectReason, cond.Reason, "reason")
			assert.Equal(t, tc.expectMessage, cond.Message, "message")
		})
	}
}

// TestCACertValidityConditions exercises the caCertValidityConditions helper in isolation,
// covering both the expiry and not-yet-valid cases.
func TestCACertValidityConditions(t *testing.T) {
	now := statusutils.FixedNow

	validCA := mustMakeCAPEM(t, "valid-ca", now.Add(-time.Hour), now.Add(90*24*time.Hour))
	expiredCA := mustMakeCAPEM(t, "expired-ca", now.Add(-48*time.Hour), now.Add(-time.Hour))
	futureCA := mustMakeCAPEM(t, "future-ca", now.Add(24*time.Hour), now.Add(90*24*time.Hour))

	tests := []struct {
		name                 string
		ca                   string
		expectExpiryNil      bool
		expectExpiryReason   string
		expectExpiryMessage  string
		expectNotYetValidNil bool
		expectNYVReason      string
		expectNYVMessage     string
	}{
		{
			name:                 "empty CA — both conditions nil",
			ca:                   "",
			expectExpiryNil:      true,
			expectNotYetValidNil: true,
		},
		{
			name:                 "currently valid CA — both conditions nil",
			ca:                   validCA,
			expectExpiryNil:      true,
			expectNotYetValidNil: true,
		},
		{
			name:                 "CA valid starting exactly now — both nil",
			ca:                   mustMakeCAPEM(t, "boundary-start", now, now.Add(time.Hour)),
			expectExpiryNil:      true,
			expectNotYetValidNil: true,
		},
		{
			name:                 "CA valid until exactly now — both nil",
			ca:                   mustMakeCAPEM(t, "boundary-end", now.Add(-time.Hour), now),
			expectExpiryNil:      true,
			expectNotYetValidNil: true,
		},
		{
			name:                 "expired CA — expiry set, notYetValid nil",
			ca:                   expiredCA,
			expectExpiryNil:      false,
			expectExpiryReason:   "Expired",
			expectExpiryMessage:  "have expired",
			expectNotYetValidNil: true,
		},
		{
			name:                 "future CA — expiry nil, notYetValid set",
			ca:                   futureCA,
			expectExpiryNil:      true,
			expectNotYetValidNil: false,
			expectNYVReason:      "NotYetValid",
			expectNYVMessage:     "are not yet valid",
		},
		{
			name:                 "bundle: one valid, one expired — expiry set",
			ca:                   validCA + expiredCA,
			expectExpiryNil:      false,
			expectExpiryReason:   "Expired",
			expectExpiryMessage:  "have expired",
			expectNotYetValidNil: true,
		},
		{
			name:                 "bundle: one valid, one future — notYetValid set",
			ca:                   validCA + futureCA,
			expectExpiryNil:      true,
			expectNotYetValidNil: false,
			expectNYVReason:      "NotYetValid",
			expectNYVMessage:     "are not yet valid",
		},
		{
			name:                 "bundle: expired and future — both set",
			ca:                   expiredCA + futureCA,
			expectExpiryNil:      false,
			expectExpiryReason:   "Expired",
			expectExpiryMessage:  "have expired",
			expectNotYetValidNil: false,
			expectNYVReason:      "NotYetValid",
			expectNYVMessage:     "are not yet valid",
		},
		{
			name:                 "unparseable PEM — both nil",
			ca:                   "NOT A PEM",
			expectExpiryNil:      true,
			expectNotYetValidNil: true,
		},
	}

	agg := &externalAuthDegradedAggregator{
		clock: clocktesting.NewFakePassiveClock(now),
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expiry, notYetValid := agg.getCaCertValidityConditions(tc.ca)

			if tc.expectExpiryNil {
				assert.Nil(t, expiry, "expected no expiry condition")
			} else {
				require.NotNil(t, expiry, "expected expiry condition")
				assert.Equal(t, CACertificateExpiredConditionType, expiry.Type)
				assert.Equal(t, metav1.ConditionTrue, expiry.Status)
				assert.Equal(t, tc.expectExpiryReason, expiry.Reason)
				assert.Contains(t, expiry.Message, tc.expectExpiryMessage)
			}

			if tc.expectNotYetValidNil {
				assert.Nil(t, notYetValid, "expected no notYetValid condition")
			} else {
				require.NotNil(t, notYetValid, "expected notYetValid condition")
				assert.Equal(t, CACertificateNotYetValidConditionType, notYetValid.Type)
				assert.Equal(t, metav1.ConditionTrue, notYetValid.Status)
				assert.Equal(t, tc.expectNYVReason, notYetValid.Reason)
				assert.Contains(t, notYetValid.Message, tc.expectNYVMessage)
			}
		})
	}
}

// TestExternalAuthDegradedAggregator_SyncOnce_CACertConditions verifies that SyncOnce
// correctly sets/clears CACertificateExpiry and CACertificateNotYetValid conditions.
func TestExternalAuthDegradedAggregator_SyncOnce_CACertConditions(t *testing.T) {
	now := statusutils.FixedNow

	validCA := mustMakeCAPEM(t, "valid-ca", now.Add(-time.Hour), now.Add(90*24*time.Hour))
	expiredCA := mustMakeCAPEM(t, "expired-ca", now.Add(-48*time.Hour), now.Add(-time.Hour))
	futureCA := mustMakeCAPEM(t, "future-ca", now.Add(24*time.Hour), now.Add(90*24*time.Hour))

	tests := []struct {
		name            string
		ca              string
		wantExpiry      bool
		wantNotYetValid bool
	}{
		{
			name: "no CA — neither condition written",
			ca:   "",
		},
		{
			name: "valid CA — neither condition written",
			ca:   validCA,
		},
		{
			name:       "expired CA — expiry condition set",
			ca:         expiredCA,
			wantExpiry: true,
		},
		{
			name:            "future CA — notYetValid condition set",
			ca:              futureCA,
			wantNotYetValid: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
				"/subscriptions/" + statusutils.TestSubscriptionID +
					"/resourceGroups/" + statusutils.TestResourceGroupName +
					"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
			))
			existing := newTestExternalAuthForAggregator(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
				ea.Properties.Issuer.CA = tc.ca
			})
			parentCluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{parentCluster, existing})
			require.NoError(t, err)

			clock := clocktesting.NewFakePassiveClock(now)
			syncer := &externalAuthDegradedAggregator{
				externalAuthLister: &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				controllerLister:   &corelistertesting.DBControllerLister{ResourcesDBClient: mockDB},
				resourcesDBClient:  mockDB,
				inertia:            externalAuthDegradedAggregatorInertia(),
				clock:              clock,
				firstObservedBad:   statusutils.NewFirstObservedBadCache(clock),
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
				SubscriptionID:      statusutils.TestSubscriptionID,
				ResourceGroupName:   statusutils.TestResourceGroupName,
				HCPClusterName:      statusutils.TestClusterName,
				HCPExternalAuthName: statusutils.TestExternalAuthName,
			})
			require.NoError(t, err)

			updated, err := mockDB.HCPClusters(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName).ExternalAuth(statusutils.TestClusterName).Get(ctx, statusutils.TestExternalAuthName)
			require.NoError(t, err)

			expiryCond := apimeta.FindStatusCondition(updated.Status.UserFacingConditions, CACertificateExpiredConditionType)
			if tc.wantExpiry {
				require.NotNil(t, expiryCond, "expected CACertificateExpiry condition")
				assert.Equal(t, metav1.ConditionTrue, expiryCond.Status)
				assert.Equal(t, "Expired", expiryCond.Reason)
			} else {
				assert.Nil(t, expiryCond, "expected no CACertificateExpiry condition")
			}

			nyvCond := apimeta.FindStatusCondition(updated.Status.UserFacingConditions, CACertificateNotYetValidConditionType)
			if tc.wantNotYetValid {
				require.NotNil(t, nyvCond, "expected CACertificateNotYetValid condition")
				assert.Equal(t, metav1.ConditionTrue, nyvCond.Status)
				assert.Equal(t, "NotYetValid", nyvCond.Reason)
			} else {
				assert.Nil(t, nyvCond, "expected no CACertificateNotYetValid condition")
			}
		})
	}
}

// TestExternalAuthDegradedAggregator_SyncOnce_CACertExpiry_clearsStaleCondition verifies that
// SyncOnce removes the CACertificateExpiry condition from the ExternalAuth when a CA is
// configured and the condition is stale.
func TestExternalAuthDegradedAggregator_SyncOnce_CACertExpiry_clearsStaleCondition(t *testing.T) {
	now := statusutils.FixedNow
	validCA := mustMakeCAPEM(t, "valid-ca", now.Add(-time.Hour), now.Add(90*24*time.Hour))

	ctx := context.Background()
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))
	existing := newTestExternalAuthForAggregator(func(ea *coreapi.HCPOpenShiftClusterExternalAuth) {
		ea.Properties.Issuer.CA = validCA
		ea.Status.UserFacingConditions = []metav1.Condition{
			{
				Type:    CACertificateExpiredConditionType,
				Status:  metav1.ConditionFalse,
				Reason:  "Valid",
				Message: "stale healthy condition should be removed",
			},
		}
	})
	parentCluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   parentClusterID,
			PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
		},
	}

	mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{parentCluster, existing})
	require.NoError(t, err)

	syncer := &externalAuthDegradedAggregator{
		externalAuthLister: &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
		controllerLister:   &corelistertesting.DBControllerLister{ResourcesDBClient: mockDB},
		resourcesDBClient:  mockDB,
		inertia:            externalAuthDegradedAggregatorInertia(),
		clock:              clocktesting.NewFakePassiveClock(now),
		firstObservedBad:   statusutils.NewFirstObservedBadCache(clocktesting.NewFakePassiveClock(now)),
	}

	err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
		SubscriptionID:      statusutils.TestSubscriptionID,
		ResourceGroupName:   statusutils.TestResourceGroupName,
		HCPClusterName:      statusutils.TestClusterName,
		HCPExternalAuthName: statusutils.TestExternalAuthName,
	})
	require.NoError(t, err)

	updated, err := mockDB.HCPClusters(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName).ExternalAuth(statusutils.TestClusterName).Get(ctx, statusutils.TestExternalAuthName)
	require.NoError(t, err)
	assert.Nil(t, apimeta.FindStatusCondition(updated.Status.UserFacingConditions, CACertificateExpiredConditionType),
		"healthy CA should remove CACertificateExpiry from user-facing conditions")
}
