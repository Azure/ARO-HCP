// Copyright 2025 Microsoft Corporation
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

package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	_ "embed"

	"github.com/google/go-cmp/cmp"
	securityv1beta1api "istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	securityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

//go:embed testdata/test-private-key.pem
var testPrivateKeyPEM string
var testPrivateKey = func() *rsa.PrivateKey {
	block, _ := pem.Decode([]byte(testPrivateKeyPEM))
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		panic(err)
	}
	return privateKey
}()

var secretWithTestPrivateKey = &corev1.Secret{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-session",
		Namespace: "test-namespace",
	},
	Data: map[string][]byte{
		"privateKey": pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(testPrivateKey),
		}),
	},
}

var secretWithFullTestCredentials = &corev1.Secret{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-session",
		Namespace: "test-namespace",
	},
	Data: map[string][]byte{
		"privateKey": pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(testPrivateKey),
		}),
		"certificate": []byte("-----BEGIN CERTIFICATE-----\nMIICertificateData\n-----END CERTIFICATE-----"),
	},
}

// mockManagementClusterProvider implements ManagementClusterProvider for testing
type mockManagementClusterQuerier struct {
	hostedControlPlane      *hypershiftv1beta1.HostedControlPlane
	hostedControlPlaneError error
	csr                     *certificatesv1.CertificateSigningRequest
	csrErr                  error
	csrApproval             *certificatesv1alpha1.CertificateSigningRequestApproval
}

func (m *mockManagementClusterQuerier) GetHostedControlPlane(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedControlPlane, error) {
	if m.hostedControlPlaneError != nil {
		return nil, m.hostedControlPlaneError
	}
	if m.hostedControlPlane == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "hostedcontrolplanes"}, namespace)
	}
	return m.hostedControlPlane, nil
}

func (m *mockManagementClusterQuerier) GetCSR(ctx context.Context, name string) (*certificatesv1.CertificateSigningRequest, error) {
	if m.csrErr != nil {
		return nil, m.csrErr
	}
	if m.csr == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, name)
	}
	return m.csr, nil
}

func (m *mockManagementClusterQuerier) GetCSRApproval(ctx context.Context, namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	if m.csrErr != nil {
		return nil, m.csrErr
	}
	if m.csrApproval == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequestapprovals"}, name)
	}
	return m.csrApproval, nil
}

// mockEndpointProvider implements SessionEndpointProvider for testing
type mockEndpointProvider struct {
	endpoint string
}

func (m *mockEndpointProvider) GetSessionEndpoint(sessionID string) string {
	if m.endpoint != "" {
		return m.endpoint
	}
	return "https://sessiongate.example.com/sessiongate/" + sessionID + "/kas"
}

// fixture
var fixedTime = time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
var nowFunc = func() time.Time { return fixedTime }

var sampleSession = &sessiongatev1alpha1.Session{
	ObjectMeta: metav1.ObjectMeta{
		Name:              "test-session",
		Namespace:         "test-namespace",
		UID:               types.UID("test-uid"),
		CreationTimestamp: metav1.Time{Time: fixedTime},
	},
	Spec: sessiongatev1alpha1.SessionSpec{
		TTL: metav1.Duration{Duration: 24 * time.Hour},
		Owner: sessiongatev1alpha1.Principal{
			Type: sessiongatev1alpha1.PrincipalTypeUser,
			UserPrincipal: &sessiongatev1alpha1.UserPrincipal{
				Name:  "user@example.com",
				Claim: "upn",
			},
		},
		AccessLevel: sessiongatev1alpha1.AccessLevel{
			Group: "break-glass",
		},
		HostedControlPlane: sessiongatev1alpha1.HostedControlPlane{
			Namespace:  "clusters-test-hcp",
			ResourceID: "/subscriptions/test/resourceGroups/test/providers/Microsoft.ContainerService/managedClusters/test",
		},
		ManagementCluster: sessiongatev1alpha1.ManagementCluster{
			ResourceID: "/subscriptions/test/resourceGroups/test/providers/Microsoft.ContainerService/managedClusters/mgmt",
		},
	},
}

var samplePolicy = &securityv1beta1.AuthorizationPolicy{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-session",
		Namespace: "test-namespace",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "sessiongate-controller",
		},
		OwnerReferences: []metav1.OwnerReference{
			{
				APIVersion: sessiongatev1alpha1.SchemeGroupVersion.String(),
				Kind:       "Session",
				Name:       "test-session",
				UID:        types.UID("test-uid"),
			},
		},
	},
	Spec: securityv1beta1api.AuthorizationPolicy{
		Selector: &typev1beta1.WorkloadSelector{
			MatchLabels: map[string]string{
				"app.kubernetes.io/name": "sessiongate",
			},
		},
		Action: securityv1beta1api.AuthorizationPolicy_ALLOW,
		Rules: []*securityv1beta1api.Rule{
			{
				To: []*securityv1beta1api.Rule_To{
					{
						Operation: &securityv1beta1api.Operation{
							Paths: []string{"/sessiongate/test-session/kas/*"},
						},
					},
				},
				When: []*securityv1beta1api.Condition{
					{
						Key:    "request.auth.claims[upn]",
						Values: []string{"user@example.com"},
					},
				},
			},
		},
	},
}

var authPolicyAvailableCondition = metav1.Condition{
	Type:               string(sessiongatev1alpha1.SessionConditionTypeAuthorizationPolicyAvailable),
	Status:             metav1.ConditionTrue,
	Reason:             "AuthorizationPolicyAvailable",
	Message:            "Authorization policy available",
	ObservedGeneration: sampleSession.Generation,
}

var sessionNotReadyCondition = metav1.Condition{
	Type:               string(sessiongatev1alpha1.SessionConditionTypeReady),
	Status:             metav1.ConditionFalse,
	Reason:             "NotReady",
	Message:            "Session is not ready",
	ObservedGeneration: sampleSession.Generation,
}

var credentialsAvailableCondition = metav1.Condition{
	Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
	Status:             metav1.ConditionTrue,
	Reason:             "CredentialsAvailable",
	Message:            "Credentials available",
	ObservedGeneration: sampleSession.Generation,
}

var networkPathAvailableCondition = metav1.Condition{
	Type:               string(sessiongatev1alpha1.SessionConditionTypeNetworkPathAvailable),
	Status:             metav1.ConditionTrue,
	Reason:             "NetworkPathAvailable",
	Message:            "Network path available via public endpoint",
	ObservedGeneration: sampleSession.Generation,
}

func TestSessionController_processSession_handleExpiration(t *testing.T) {
	tests := []struct {
		name          string
		creationTime  time.Time
		sessionStatus sessiongatev1alpha1.SessionStatus
		expectAction  bool
		expectedErr   bool
	}{
		{
			name:          "session without expiration timestamp",
			sessionStatus: sessiongatev1alpha1.SessionStatus{},
			expectAction:  true,
		},
		{
			name:         "expired session",
			creationTime: fixedTime.Add(-48 * time.Hour),
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt: &metav1.Time{Time: fixedTime.Add(-24 * time.Hour)},
			},
			expectAction: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := sampleSession.DeepCopy()
			testSession.Status = tt.sessionStatus
			if !tt.creationTime.IsZero() {
				testSession.CreationTimestamp = metav1.Time{Time: tt.creationTime}
			}

			// Setup controller with mock getters
			controller := &SessionController{
				fieldManager:     "test-controller",
				endpointProvider: &mockEndpointProvider{},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
				},
				getAuthorizationPolicy: func(namespace, name string) (*securityv1beta1.AuthorizationPolicy, error) {
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "authorizationpolicies"}, name)
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return nil, errors.New("not implemented")
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, &mockManagementClusterQuerier{}, nowFunc)

			// Verify error expectation
			if tt.expectedErr && err == nil {
				t.Errorf("expected error but got none")
			} else if !tt.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}

			// Verify action
			if !tt.expectAction && action != nil {
				t.Errorf("expected no action but got: %+v", action)
			} else if tt.expectAction && action == nil {
				t.Errorf("expected action but got none")
			} else if action != nil {
				// Validate action
				if err := action.validate(); err != nil {
					t.Errorf("action validation failed: %v", err)
				}
				// Compare with golden fixture
				CompareWithFixture(t, action, compareActions()...)
			}
		})
	}
}

func TestSessionController_processSession_ensureAuthorizationPolicy(t *testing.T) {
	tests := []struct {
		name            string
		sessionStatus   sessiongatev1alpha1.SessionStatus
		existingSecret  *corev1.Secret
		existingPolicy  *securityv1beta1.AuthorizationPolicy
		expectAction    bool
		expectedRequeue bool
		expectedErr     bool
	}{
		{
			name: "session with expiration but no auth policy",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt: &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
			},
			existingPolicy: nil, // No existing policy
			expectAction:   true,
		},
		{
			name: "session with policy but no status update",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt: &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				// AuthorizationPolicyRef is empty and no condition
			},
			existingPolicy: samplePolicy,
			expectAction:   true,
		},
		{
			name: "session with policy drift",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
				},
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
				},
				Spec: securityv1beta1api.AuthorizationPolicy{
					Selector: &typev1beta1.WorkloadSelector{
						MatchLabels: map[string]string{
							"app.kubernetes.io/name": "sessiongate",
						},
					},
					Action: securityv1beta1api.AuthorizationPolicy_ALLOW,
					Rules: []*securityv1beta1api.Rule{
						{
							To: []*securityv1beta1api.Rule_To{
								{
									Operation: &securityv1beta1api.Operation{
										Paths: []string{"/here-is-the-drift"},
									},
								},
							},
						},
					},
				},
			},
			expectAction: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := sampleSession.DeepCopy()
			testSession.Status = tt.sessionStatus

			// Setup controller with mock getters
			controller := &SessionController{
				fieldManager:     "test-controller",
				endpointProvider: &mockEndpointProvider{},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
				},
				getAuthorizationPolicy: func(namespace, name string) (*securityv1beta1.AuthorizationPolicy, error) {
					return tt.existingPolicy, nil
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return nil, errors.New("not implemented")
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, &mockManagementClusterQuerier{}, nowFunc)

			// Verify error expectation
			if tt.expectedErr && err == nil {
				t.Errorf("expected error but got none")
			} else if !tt.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}

			// Verify action
			if !tt.expectAction && action != nil {
				t.Errorf("expected no action but got: %+v", action)
			} else if tt.expectAction && action == nil {
				t.Errorf("expected action but got none")
			} else if action != nil {
				// Validate action
				if err := action.validate(); err != nil {
					t.Errorf("action validation failed: %v", err)
				}
				// Compare with golden fixture
				CompareWithFixture(t, action, compareActions()...)
			}
		})
	}
}

func TestSessionController_processSession_generateCredentials(t *testing.T) {
	csrRequestBody, err := createCSRRequestBody(sampleSession, testPrivateKey)
	if err != nil {
		t.Fatalf("failed to create CSR request body: %v", err)
	}
	csrSpec := certificatesv1.CertificateSigningRequestSpec{
		Request:    csrRequestBody,
		SignerName: "hypershift.openshift.io/clusters-test-hcp.sre-break-glass",
	}
	unapprovedCSR := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-session",
		},
		Spec: csrSpec,
	}
	approvedCSR := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-session",
		},
		Spec: csrSpec,
		Status: certificatesv1.CertificateSigningRequestStatus{
			Conditions: []certificatesv1.CertificateSigningRequestCondition{
				{
					Type:   "Approved",
					Status: "True",
				},
			},
		},
	}
	signedCSR := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-session",
		},
		Spec: csrSpec,
		Status: certificatesv1.CertificateSigningRequestStatus{
			Certificate: []byte("-----BEGIN CERTIFICATE-----\nMIICertificateData\n-----END CERTIFICATE-----"),
		},
	}
	csrApproval := &certificatesv1alpha1.CertificateSigningRequestApproval{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-session",
			Namespace: "clusters-test-hcp",
			Labels: map[string]string{
				"api.openshift.com/type": "break-glass-credential",
			},
		},
	}
	differentPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate different private key: %v", err)
	}

	tests := []struct {
		name            string
		sessionStatus   sessiongatev1alpha1.SessionStatus
		existingSecret  *corev1.Secret
		mq              ManagementClusterQuerier
		expectAction    bool
		expectedRequeue bool
		expectedErr     bool
	}{
		{
			name: "session without secret",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				csr:    nil, // No CSR exists yet
				csrErr: apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			expectAction: true,
		},
		{
			name: "session with secret but no credentials status ref and conditions",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					// Credentials conditions missing
				},
				// CredentialsSecretRef missing
			},
			existingSecret: secretWithTestPrivateKey,
			mq: &mockManagementClusterQuerier{
				csr:    nil, // No CSR exists yet
				csrErr: apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			expectAction: true,
		},
		{
			name: "session with private key but no CSR",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
						Status:             metav1.ConditionFalse,
						Reason:             "PrivateKeyCreated",
						Message:            "Private key created, waiting for CSR to be created",
						ObservedGeneration: sampleSession.Generation,
					},
				},
			},
			existingSecret: secretWithTestPrivateKey,
			mq: &mockManagementClusterQuerier{
				csr:    nil, // No CSR exists yet
				csrErr: apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			expectAction: true,
		},
		{
			name: "session with CSR but missing status updates",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
						Status:             metav1.ConditionFalse,
						Reason:             "PrivateKeyCreated",
						Message:            "Private key created, waiting for CSR to be created",
						ObservedGeneration: sampleSession.Generation,
					},
				},
			},
			existingSecret: secretWithTestPrivateKey,
			mq: &mockManagementClusterQuerier{
				csr:    unapprovedCSR,
				csrErr: nil,
			},
			expectAction: true,
		},

		{
			name: "session with CSR but missing CSR approval",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
						Status:             metav1.ConditionFalse,
						Reason:             "CertificateSigningRequestPending",
						Message:            "Certificate signing request pending, waiting for approval",
						ObservedGeneration: sampleSession.Generation,
					},
				},
			},
			existingSecret: secretWithTestPrivateKey,
			mq: &mockManagementClusterQuerier{
				csr:    unapprovedCSR,
				csrErr: nil,
			},
			expectAction: true,
		},
		{
			name: "session with private key mismatch in CSR",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
						Status:             metav1.ConditionFalse,
						Reason:             "CertificateSigningRequestPending",
						Message:            "Certificate signing request pending, waiting for approval",
						ObservedGeneration: sampleSession.Generation,
					},
				},
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"privateKey": pem.EncodeToMemory(&pem.Block{
						Type:  "RSA PRIVATE KEY",
						Bytes: x509.MarshalPKCS1PrivateKey(differentPrivateKey),
					}),
				},
			},
			mq: &mockManagementClusterQuerier{
				csr: unapprovedCSR,
			},
			expectAction: true,
		},
		{
			name: "session with approval but unsigned CSR",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
						Status:             metav1.ConditionFalse,
						Reason:             "CertificateSigningRequestPending",
						Message:            "Certificate signing request pending, waiting for approval",
						ObservedGeneration: sampleSession.Generation,
					},
				},
			},
			existingSecret: secretWithTestPrivateKey,
			mq: &mockManagementClusterQuerier{
				csr:         approvedCSR,
				csrApproval: csrApproval,
			},
			expectAction: false,
		},
		{
			name: "session with signed CSR but no certificate in secret",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
						Status:             metav1.ConditionFalse,
						Reason:             "CertificateSigningRequestPending",
						Message:            "Certificate signing request pending, waiting for approval",
						ObservedGeneration: sampleSession.Generation,
					},
				},
			},
			existingSecret: secretWithTestPrivateKey,
			mq: &mockManagementClusterQuerier{
				csr:         signedCSR,
				csrApproval: csrApproval,
			},
			expectAction: true,
		},
		{
			name: "session with credentials in secret but no status update",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
						Status:             metav1.ConditionFalse,
						Reason:             "CertificateSigningRequestPending",
						Message:            "Certificate signing request pending, waiting for approval",
						ObservedGeneration: sampleSession.Generation,
					},
				},
			},
			existingSecret: secretWithFullTestCredentials,
			mq: &mockManagementClusterQuerier{
				csr:         signedCSR,
				csrApproval: csrApproval,
			},
			expectAction: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := sampleSession.DeepCopy()
			testSession.Status = tt.sessionStatus

			// Setup controller with mock getters
			controller := &SessionController{
				fieldManager:     "test-controller",
				endpointProvider: &mockEndpointProvider{},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					if tt.existingSecret != nil && tt.existingSecret.Namespace == namespace && tt.existingSecret.Name == name {
						return tt.existingSecret, nil
					}
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
				},
				getAuthorizationPolicy: func(namespace, name string) (*securityv1beta1.AuthorizationPolicy, error) {
					return samplePolicy, nil
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return testPrivateKey, nil
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, tt.mq, nowFunc)

			// Verify error expectation
			if tt.expectedErr && err == nil {
				t.Errorf("expected error but got none")
			} else if !tt.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}

			// Verify action
			if !tt.expectAction && action != nil {
				t.Errorf("expected no action but got: %+v", action)
			} else if tt.expectAction && action == nil {
				t.Errorf("expected action but got none")
			} else if action != nil {
				// Validate action
				if err := action.validate(); err != nil {
					t.Errorf("action validation failed: %v", err)
				}
				// Compare with golden fixture
				CompareWithFixture(t, action, compareActions()...)
			}
		})
	}
}

func TestSessionController_processSession_ensureNetworkPath(t *testing.T) {
	tests := []struct {
		name          string
		sessionStatus sessiongatev1alpha1.SessionStatus
		expectAction  bool
		expectedErr   bool
	}{
		{
			name: "session with credentials but no backend URL",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					credentialsAvailableCondition,
				},
				// BackendKASURL missing
			},
			expectAction: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := sampleSession.DeepCopy()
			testSession.Status = tt.sessionStatus

			// Setup controller with mock getters
			controller := &SessionController{
				fieldManager:     "test-controller",
				endpointProvider: &mockEndpointProvider{},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					return secretWithFullTestCredentials, nil
				},
				getAuthorizationPolicy: func(namespace, name string) (*securityv1beta1.AuthorizationPolicy, error) {
					return samplePolicy, nil
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return nil, errors.New("should not be called in these tests")
				},
			}

			mq := &mockManagementClusterQuerier{
				hostedControlPlane: &hypershiftv1beta1.HostedControlPlane{
					Spec: hypershiftv1beta1.HostedControlPlaneSpec{
						KubeAPIServerDNSName: "api.test-hcp.example.com",
					},
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, mq, nowFunc)

			// Verify error expectation
			if tt.expectedErr && err == nil {
				t.Errorf("expected error but got none")
			} else if !tt.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}

			// Verify action
			if !tt.expectAction && action != nil {
				t.Errorf("expected no action but got: %+v", action)
			} else if tt.expectAction && action == nil {
				t.Errorf("expected action but got none")
			} else if action != nil {
				// Validate action
				if err := action.validate(); err != nil {
					t.Errorf("action validation failed: %v", err)
				}
				// Compare with golden fixture
				CompareWithFixture(t, action, compareActions()...)
			}
		})
	}
}

func TestSessionController_processSession_finalize(t *testing.T) {
	tests := []struct {
		name          string
		sessionStatus sessiongatev1alpha1.SessionStatus
		expectAction  bool
		expectedErr   bool
	}{
		{
			name: "session without endpoint",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				BackendKASURL:          "https://api.test-hcp.example.com",
				Conditions: []metav1.Condition{
					authPolicyAvailableCondition,
					sessionNotReadyCondition,
					credentialsAvailableCondition,
					networkPathAvailableCondition,
				},
				// Endpoint missing
			},
			expectAction: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := sampleSession.DeepCopy()
			testSession.Status = tt.sessionStatus

			// Setup controller with mock getters
			controller := &SessionController{
				fieldManager: "test-controller",
				endpointProvider: &mockEndpointProvider{
					endpoint: "https://localhost:8080/sessiongate/test-session/kas",
				},

				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					return secretWithFullTestCredentials, nil
				},
				getAuthorizationPolicy: func(namespace, name string) (*securityv1beta1.AuthorizationPolicy, error) {
					return samplePolicy, nil
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return nil, errors.New("should not be called in these tests")
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, &mockManagementClusterQuerier{}, nowFunc)

			// Verify error expectation
			if tt.expectedErr && err == nil {
				t.Errorf("expected error but got none")
			} else if !tt.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}

			// Verify action
			if !tt.expectAction && action != nil {
				t.Errorf("expected no action but got: %+v", action)
			} else if tt.expectAction && action == nil {
				t.Errorf("expected action but got none")
			} else if action != nil {
				// Validate action
				if err := action.validate(); err != nil {
					t.Errorf("action validation failed: %v", err)
				}
				// Compare with golden fixture
				CompareWithFixture(t, action, compareActions()...)
			}
		})
	}
}

func compareActions() []cmp.Option {
	return []cmp.Option{}
}
