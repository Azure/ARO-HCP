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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/statusutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

// mustMakeCAPEM creates a self-signed CA PEM for testing with the specified
// subject CN and validity window.
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

// TestGetCaCertValidityCondition exercises the getCaCertValidityCondition helper
// in isolation, covering expired, not-yet-valid, mixed, and healthy cases.
// Certs use wide margins around time.Now() so assertions are not sensitive to
// wall-clock drift.
func TestGetCaCertValidityCondition(t *testing.T) {
	now := time.Now()

	validCA := mustMakeCAPEM(t, "valid-ca", now.Add(-24*time.Hour), now.Add(90*24*time.Hour))
	expiredCA := mustMakeCAPEM(t, "expired-ca", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	futureCA := mustMakeCAPEM(t, "future-ca", now.Add(24*time.Hour), now.Add(90*24*time.Hour))

	// invalidCertPEM is a PEM block typed CERTIFICATE but containing garbage DER bytes,
	// which will cause x509.ParseCertificate to fail.
	invalidCertPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-valid-der")}))

	c := &externalAuthOIDCCACertValidityController{}

	tests := []struct {
		name         string
		ca           string
		expectError  bool
		expectStatus metav1.ConditionStatus
		expectReason string
		expectMsg    string
	}{
		{
			name:         "empty CA — False",
			ca:           "",
			expectStatus: metav1.ConditionFalse,
			expectReason: coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected,
		},
		{
			name:         "currently valid CA — False",
			ca:           validCA,
			expectStatus: metav1.ConditionFalse,
			expectReason: coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected,
		},
		{
			name:         "expired CA — True with expiry message",
			ca:           expiredCA,
			expectStatus: metav1.ConditionTrue,
			expectReason: coreapi.ExternalAuthOIDCCACertificateDegradationReason,
			expectMsg:    "have expired",
		},
		{
			name:         "future CA — True with not-yet-valid message",
			ca:           futureCA,
			expectStatus: metav1.ConditionTrue,
			expectReason: coreapi.ExternalAuthOIDCCACertificateDegradationReason,
			expectMsg:    "are not yet valid",
		},
		{
			name:         "bundle: one valid, one expired — True",
			ca:           validCA + expiredCA,
			expectStatus: metav1.ConditionTrue,
			expectReason: coreapi.ExternalAuthOIDCCACertificateDegradationReason,
			expectMsg:    "have expired",
		},
		{
			name:         "bundle: one valid, one future — True",
			ca:           validCA + futureCA,
			expectStatus: metav1.ConditionTrue,
			expectReason: coreapi.ExternalAuthOIDCCACertificateDegradationReason,
			expectMsg:    "are not yet valid",
		},
		{
			name:         "bundle: expired and future — True with both messages",
			ca:           expiredCA + futureCA,
			expectStatus: metav1.ConditionTrue,
			expectReason: coreapi.ExternalAuthOIDCCACertificateDegradationReason,
			expectMsg:    "have expired",
		},
		{
			name:         "non-PEM input (no CERTIFICATE block) — False, no error",
			ca:           "NOT A PEM",
			expectStatus: metav1.ConditionFalse,
			expectReason: coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected,
		},
		{
			name:        "CERTIFICATE block with invalid DER — error returned",
			ca:          invalidCertPEM,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cond, err := c.getCACertValidityCondition(tc.ca)
			if tc.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, cond.Type)
			assert.Equal(t, tc.expectStatus, cond.Status, "condition status")
			assert.Equal(t, tc.expectReason, cond.Reason, "condition reason")
			if tc.expectMsg != "" {
				assert.Contains(t, cond.Message, tc.expectMsg, "condition message")
			}
		})
	}
}

// TestGetCaCertValidityCondition_MixedBundle_BothMessages verifies that when the
// bundle has both expired and not-yet-valid certs, the single condition message
// contains information about both issues.
func TestGetCaCertValidityCondition_MixedBundle_BothMessages(t *testing.T) {
	now := time.Now()
	expiredCA := mustMakeCAPEM(t, "expired-ca", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	futureCA := mustMakeCAPEM(t, "future-ca", now.Add(24*time.Hour), now.Add(90*24*time.Hour))

	c := &externalAuthOIDCCACertValidityController{}
	cond, err := c.getCACertValidityCondition(expiredCA + futureCA)
	require.NoError(t, err)

	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, cond.Message, "have expired", "message should mention expired certs")
	assert.Contains(t, cond.Message, "are not yet valid", "message should mention not-yet-valid certs")
}

// TestExternalAuthUserFacingAggregator_SyncOnce_CACertConditions verifies that SyncOnce
// correctly aggregates the CA certificate validity condition from SPEA (set by
// the CA cert validity controller) into the Degraded condition on
// ExternalAuth.Status.UserFacingConditions.
func TestExternalAuthUserFacingAggregator_SyncOnce_CACertConditions(t *testing.T) {
	tests := []struct {
		name                string
		speaConditions      []metav1.Condition
		wantStatus          metav1.ConditionStatus
		wantMessageContains string
	}{
		{
			name:           "no conditions on SPEA — Degraded=Unknown (not yet reported)",
			speaConditions: nil,
			wantStatus:     metav1.ConditionUnknown,
		},
		{
			name: "invalid CA condition on SPEA (expired) — Degraded=True",
			speaConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCCACertificateDegradationReason, Message: "1 CA certificate(s) in the issuer bundle have expired: CN=test-ca"},
			},
			wantStatus:          metav1.ConditionTrue,
			wantMessageContains: "have expired",
		},
		{
			name: "invali CA condition on SPEA (not-yet-valid) — Degraded=True",
			speaConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionTrue, Reason: coreapi.ExternalAuthOIDCCACertificateDegradationReason, Message: "1 CA certificate(s) in the issuer bundle are not yet valid: CN=test-ca"},
			},
			wantStatus:          metav1.ConditionTrue,
			wantMessageContains: "are not yet valid",
		},
		{
			name: "CA cert condition False on SPEA, OIDCClients absent — Degraded=Unknown OIDCClients",
			speaConditions: []metav1.Condition{
				{Type: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition, Status: metav1.ConditionFalse, Reason: coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected, Message: coreapi.ExternalAuthMessageOIDCCACertificateValid},
			},
			wantStatus:          metav1.ConditionUnknown,
			wantMessageContains: "Waiting for condition to be evaluated",
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
			existing := newTestExternalAuthForAggregatorTests()
			serviceProviderExternalAuth := newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = tc.speaConditions
			})
			parentCluster := &coreapi.Cluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{parentCluster, existing, serviceProviderExternalAuth})
			require.NoError(t, err)

			syncer := &externalAuthUserFacingConditionsDegradedAggregator{
				externalAuthLister:                &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				serviceProviderExternalAuthLister: &corelistertesting.DBServiceProviderExternalAuthLister{ResourcesDBClient: mockDB},
				resourcesDBClient:                 mockDB,
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

			degraded := apimeta.FindStatusCondition(updated.Status.UserFacingConditions, statusutils.DegradedConditionType)
			require.NotNil(t, degraded, "Degraded condition must always be present")
			assert.Equal(t, tc.wantStatus, degraded.Status, "Degraded status")
			if tc.wantMessageContains != "" {
				assert.Contains(t, degraded.Message, tc.wantMessageContains, "Degraded message")
			}
		})
	}
}

// TestExternalAuthOIDCCACertValidityController_SyncOnce exercises the controller's
// SyncOnce against a mock Cosmos database, covering:
//   - persisting the OIDCIssuerCACertificateInvalid condition for the
//     first time (no CA, then an expired CA),
//   - clearing a previously-True condition once the CA is replaced with a valid one,
//   - leaving unrelated ServiceProviderExternalAuth conditions untouched,
//   - no-op when the condition already matches, and
//   - deletion / missing-SPEA skip behavior.
//
// This complements TestGetCaCertValidityCondition (which only exercises the pure
// helper) by verifying the controller actually reads from and writes to Cosmos
// via SyncOnce, following the pattern of
// TestExternalAuthOIDCClientsDegradedController_SyncOnce in the sibling test file.
func TestExternalAuthOIDCCACertValidityController_SyncOnce(t *testing.T) {
	parentClusterID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + statusutils.TestSubscriptionID +
			"/resourceGroups/" + statusutils.TestResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + statusutils.TestClusterName,
	))

	now := time.Now()
	expiredCA := mustMakeCAPEM(t, "expired-ca", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	validCA := mustMakeCAPEM(t, "valid-ca", now.Add(-24*time.Hour), now.Add(90*24*time.Hour))

	staleInvalidCondition := metav1.Condition{
		Type:    coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.ExternalAuthOIDCCACertificateDegradationReason,
		Message: "1 CA certificate(s) in the issuer bundle have expired: CN=expired-ca",
	}
	unrelatedCondition := metav1.Condition{
		Type:    coreapi.ExternalAuthOIDCClientsDegradedCondition,
		Status:  metav1.ConditionTrue,
		Reason:  coreapi.ExternalAuthOIDCCACertificateDegradationReason,
		Message: "console/openshift-console: " + coreapi.ExternalAuthMessageGenericNotWorking,
	}

	// expectedCondition describes the attributes of a single condition that must
	// be present in the updated ServiceProviderExternalAuth after SyncOnce.
	// Matched by Type; only non-zero fields are asserted.
	type expectedCondition struct {
		condType        string
		status          metav1.ConditionStatus
		reason          string
		messageContains string
	}

	tests := []struct {
		name string

		externalAuth                *coreapi.ExternalAuth
		serviceProviderExternalAuth *coreapi.ServiceProviderExternalAuth

		expectNoWrite      bool
		expectedConditions []expectedCondition
	}{
		{
			name: "skip when external auth is being deleted",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				deletionTimestamp := metav1.Now()
				ea.ServiceProviderProperties.DeletionTimestamp = &deletionTimestamp
				ea.Properties.Issuer.CA = expiredCA
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{staleInvalidCondition}
			}),
			expectNoWrite: true,
		},
		{
			name: "skip when ServiceProviderExternalAuth not yet created",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.Properties.Issuer.CA = expiredCA
			}),
			serviceProviderExternalAuth: nil,
			expectNoWrite:               true,
		},
		{
			name: "persists condition for the first time: empty CA - False/AsExpected",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.Properties.Issuer.CA = ""
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectedConditions: []expectedCondition{
				{
					condType: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
					status:   metav1.ConditionFalse,
					reason:   coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected,
				},
			},
		},
		{
			name: "persists condition for the first time: expired CA - True/Degradation",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.Properties.Issuer.CA = expiredCA
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(),
			expectedConditions: []expectedCondition{
				{
					condType:        coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
					status:          metav1.ConditionTrue,
					reason:          coreapi.ExternalAuthOIDCCACertificateDegradationReason,
					messageContains: "have expired",
				},
			},
		},
		{
			name: "clears degradation after CA replacement: expired - valid",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				// The ExternalAuth's CA has since been replaced with a valid one,
				// but ServiceProviderExternalAuth still carries the stale True
				// condition from when the CA was expired.
				ea.Properties.Issuer.CA = validCA
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{staleInvalidCondition}
			}),
			expectedConditions: []expectedCondition{
				{
					condType: coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
					status:   metav1.ConditionFalse,
					reason:   coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected,
				},
			},
		},
		{
			name: "preserves unrelated conditions on ServiceProviderExternalAuth",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.Properties.Issuer.CA = expiredCA
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{unrelatedCondition}
			}),
			expectedConditions: []expectedCondition{
				{
					condType:        coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
					status:          metav1.ConditionTrue,
					reason:          coreapi.ExternalAuthOIDCCACertificateDegradationReason,
					messageContains: "have expired",
				},
				{
					condType:        unrelatedCondition.Type,
					status:          unrelatedCondition.Status,
					reason:          unrelatedCondition.Reason,
					messageContains: unrelatedCondition.Message,
				},
			},
		},
		{
			name: "no-op when the condition already matches",
			externalAuth: newTestExternalAuthForAggregatorTests(func(ea *coreapi.ExternalAuth) {
				ea.Properties.Issuer.CA = ""
			}),
			serviceProviderExternalAuth: newTestServiceProviderExternalAuth(func(spea *coreapi.ServiceProviderExternalAuth) {
				spea.Status.Conditions = []metav1.Condition{{
					Type:    coreapi.ExternalAuthOIDCIssuerCACertificateInvalidCondition,
					Status:  metav1.ConditionFalse,
					Reason:  coreapi.ExternalAuthOIDCCACertificateDegradedReasonAsExpected,
					Message: coreapi.ExternalAuthMessageOIDCCACertificateValid,
				}}
			}),
			expectNoWrite: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			parentCluster := &coreapi.Cluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   parentClusterID,
					PartitionKey: strings.ToLower(parentClusterID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{ID: parentClusterID, Name: statusutils.TestClusterName, Type: parentClusterID.ResourceType.String()},
				},
			}

			seed := []any{parentCluster, tc.externalAuth}
			if tc.serviceProviderExternalAuth != nil {
				seed = append(seed, tc.serviceProviderExternalAuth)
			}
			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, seed)
			require.NoError(t, err)

			syncer := &externalAuthOIDCCACertValidityController{
				externalAuthLister:                &corelistertesting.DBExternalAuthLister{ResourcesDBClient: mockDB},
				serviceProviderExternalAuthLister: &corelistertesting.DBServiceProviderExternalAuthLister{ResourcesDBClient: mockDB},
				resourcesDBClient:                 mockDB,
			}

			err = syncer.SyncOnce(ctx, controllerutils.HCPExternalAuthKey{
				SubscriptionID:      statusutils.TestSubscriptionID,
				ResourceGroupName:   statusutils.TestResourceGroupName,
				HCPClusterName:      statusutils.TestClusterName,
				HCPExternalAuthName: statusutils.TestExternalAuthName,
			})
			require.NoError(t, err)

			if tc.expectNoWrite {
				if tc.serviceProviderExternalAuth == nil {
					return
				}
				updatedServiceProviderExternalAuth, err := mockDB.ServiceProviderExternalAuths(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName, statusutils.TestExternalAuthName).Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
				require.NoError(t, err)
				assert.Equal(t, tc.serviceProviderExternalAuth.Status.Conditions, updatedServiceProviderExternalAuth.Status.Conditions,
					"conditions should not have changed")
				return
			}

			updatedServiceProviderExternalAuth, err := mockDB.ServiceProviderExternalAuths(statusutils.TestSubscriptionID, statusutils.TestResourceGroupName, statusutils.TestClusterName, statusutils.TestExternalAuthName).Get(ctx, coreapi.ServiceProviderExternalAuthResourceName)
			require.NoError(t, err)

			actualConditions := updatedServiceProviderExternalAuth.Status.Conditions

			// Every expected condition must be present with the right attributes.
			for _, exp := range tc.expectedConditions {
				actual := apimeta.FindStatusCondition(actualConditions, exp.condType)
				require.NotNilf(t, actual, "expected condition type %q not found in result", exp.condType)
				assert.Equalf(t, exp.status, actual.Status, "condition %q: wrong status", exp.condType)
				assert.Equalf(t, exp.reason, actual.Reason, "condition %q: wrong reason", exp.condType)
				if exp.messageContains != "" {
					assert.Containsf(t, actual.Message, exp.messageContains, "condition %q: message does not contain expected substring", exp.condType)
				}
			}

			// No condition type in the result may be absent from expectedConditions.
			expectedTypes := make(map[string]struct{}, len(tc.expectedConditions))
			for _, exp := range tc.expectedConditions {
				expectedTypes[exp.condType] = struct{}{}
			}
			for _, actual := range actualConditions {
				assert.Containsf(t, expectedTypes, actual.Type, "unexpected condition type %q found in result", actual.Type)
			}
		})
	}
}
