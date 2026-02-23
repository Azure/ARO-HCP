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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	_ "embed"

	"github.com/google/go-cmp/cmp"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"

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
		Name:      "sessiongate-9b1f64c3",
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
		Name:      "sessiongate-9b1f64c3",
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

func buildHCP(available bool) *hypershiftv1beta1.HostedControlPlane {
	status := metav1.ConditionTrue
	if !available {
		status = metav1.ConditionFalse
	}
	return &hypershiftv1beta1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-hcp",
			Namespace: "clusters-test-hcp",
		},
		Spec: hypershiftv1beta1.HostedControlPlaneSpec{
			KubeAPIServerDNSName: "api.test-hcp.example.com",
		},
		Status: hypershiftv1beta1.HostedControlPlaneStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Available",
					Status: status,
				},
			},
		},
	}
}

// mockManagementClusterProvider implements ManagementClusterProvider for testing
type mockManagementClusterQuerier struct {
	hostedControlPlane       *hypershiftv1beta1.HostedControlPlane
	getHostedControlPlaneErr error
	csr                      *certificatesv1.CertificateSigningRequest
	getCSRErr                error
	csrApproval              *certificatesv1alpha1.CertificateSigningRequestApproval
	getCSRApprovalErr        error
}

func (m *mockManagementClusterQuerier) GetHostedControlPlane(namespace string) (*hypershiftv1beta1.HostedControlPlane, error) {
	if m.getHostedControlPlaneErr != nil {
		return nil, m.getHostedControlPlaneErr
	}
	if m.hostedControlPlane == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "hostedcontrolplanes"}, namespace)
	}
	return m.hostedControlPlane, nil
}

func (m *mockManagementClusterQuerier) GetCSR(name string) (*certificatesv1.CertificateSigningRequest, error) {
	if m.getCSRErr != nil {
		return nil, m.getCSRErr
	}
	if m.csr == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, name)
	}
	return m.csr, nil
}

func (m *mockManagementClusterQuerier) GetCSRApproval(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	if m.getCSRApprovalErr != nil {
		return nil, m.getCSRApprovalErr
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
			Type: sessiongatev1alpha1.PrincipalTypeAzureUser,
			Name: "user@example.com",
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

var sessionNotReadyCondition = metav1.Condition{
	Type:               string(sessiongatev1alpha1.SessionConditionTypeReady),
	Status:             metav1.ConditionFalse,
	Reason:             sessiongatev1alpha1.SessionNotReadyReason,
	Message:            "Session is not ready",
	ObservedGeneration: sampleSession.Generation,
	LastTransitionTime: metav1.Time{Time: fixedTime},
}

var hostedControlPlaneAvailableCondition = metav1.Condition{
	Type:               string(sessiongatev1alpha1.SessionConditionTypeHostedControlPlaneAvailable),
	Status:             metav1.ConditionTrue,
	Reason:             sessiongatev1alpha1.HostedControlPlaneAvailableReason,
	Message:            "HostedControlPlane is available and ready",
	ObservedGeneration: sampleSession.Generation,
	LastTransitionTime: metav1.Time{Time: fixedTime},
}

var credentialsAvailableCondition = metav1.Condition{
	Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
	Status:             metav1.ConditionTrue,
	Reason:             sessiongatev1alpha1.CredentialsAvailableReason,
	Message:            "Credentials available",
	ObservedGeneration: sampleSession.Generation,
	LastTransitionTime: metav1.Time{Time: fixedTime},
}

var networkPathAvailableCondition = metav1.Condition{
	Type:               string(sessiongatev1alpha1.SessionConditionTypeNetworkPathAvailable),
	Status:             metav1.ConditionTrue,
	Reason:             sessiongatev1alpha1.NetworkPathAvailableReason,
	Message:            "Network path available via public endpoint",
	ObservedGeneration: sampleSession.Generation,
	LastTransitionTime: metav1.Time{Time: fixedTime},
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
				clock:            clocktesting.NewFakeClock(fixedTime),
				endpointProvider: &mockEndpointProvider{},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return nil, errors.New("not implemented")
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, &mockManagementClusterQuerier{})

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
		SignerName: CSRSignerName(sampleSession.Spec.HostedControlPlane.Namespace),
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
	differentPrivateKey, err := rsa.GenerateKey(rand.Reader, RSAKeySize)
	if err != nil {
		t.Fatalf("failed to generate different private key: %v", err)
	}

	tests := []struct {
		name             string
		sessionStatus    sessiongatev1alpha1.SessionStatus
		existingSecret   *corev1.Secret
		getSecretErr     error
		newPrivateKeyErr error
		mq               ManagementClusterQuerier
		expectAction     bool
		expectedRequeue  bool
		expectedErr      bool
	}{
		{
			name: "session without secret and credentials secret ref",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt: &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				// CredentialsSecretRef missing
				Conditions: []metav1.Condition{
					hostedControlPlaneAvailableCondition,
					sessionNotReadyCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
				csr:                nil,
				getCSRErr:          apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			expectAction: true,
		},
		{
			name: "session without secret",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
				csr:                nil,
				getCSRErr:          apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			expectAction: true,
		},
		{
			name: "session with private key but no private keyconditions",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
					// Credentials conditions missing
				},
				// CredentialsSecretRef missing
			},
			existingSecret: secretWithTestPrivateKey,
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
				csr:                nil, // No CSR exists yet
				getCSRErr:          apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			expectAction: true,
		},
		{
			name: "session with private key but no CSR",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                nil, // No CSR exists yet
				getCSRErr:          apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			expectAction: true,
		},
		{
			name: "session with CSR but missing status updates",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                unapprovedCSR,
				getCSRErr:          nil,
			},
			expectAction: true,
		},

		{
			name: "session with CSR but missing CSR approval",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                unapprovedCSR,
				getCSRErr:          nil,
			},
			expectAction: true,
		},
		{
			name: "session with private key mismatch in CSR",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
					Name:      "sessiongate-9b1f64c3",
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
				hostedControlPlane: buildHCP(true),
				csr:                unapprovedCSR,
			},
			expectAction: true,
		},
		{
			name: "session with approval but unsigned CSR",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                approvedCSR,
				csrApproval:        csrApproval,
			},
			expectAction: false,
		},
		{
			name: "session with signed CSR but no certificate in secret",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                signedCSR,
				csrApproval:        csrApproval,
			},
			expectAction: true,
		},
		{
			name: "transient error retrieving credential secret",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
				},
			},
			getSecretErr: apierrors.NewTimeoutError("timeout", 5),
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
			},
			expectAction: false,
			expectedErr:  true,
		},
		{
			name: "forbidden error retrieving credential secret sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
				},
			},
			getSecretErr: apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "sessiongate-9b1f64c3", errors.New("access denied")),
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
			},
			expectAction: true,
			expectedErr:  false,
		},
		{
			name: "transient error retrieving CSR",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
				getCSRErr:          apierrors.NewServiceUnavailable("service unavailable"),
			},
			expectAction: false,
			expectedErr:  true,
		},
		{
			name: "forbidden error retrieving CSR sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
				getCSRErr:          apierrors.NewForbidden(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session", errors.New("access denied")),
			},
			expectAction: true,
			expectedErr:  false,
		},
		{
			name: "infrastructure error retrieving CSR sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
				getCSRErr:          apierrors.NewInternalError(errors.New("internal server error")),
			},
			expectAction: true,
			expectedErr:  false,
		},
		{
			name: "private key generation failure sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
				getCSRErr:          apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			newPrivateKeyErr: errors.New("entropy source exhausted"),
			expectAction:     true,
			expectedErr:      false,
		},
		{
			name: "infrastructure error retrieving credential secret sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
				},
			},
			getSecretErr: apierrors.NewInternalError(errors.New("etcd leader changed")),
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
			},
			expectAction: true,
			expectedErr:  false,
		},
		{
			name: "transient error retrieving CSR with pending condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeCredentialsAvailable),
						Status:             metav1.ConditionFalse,
						Reason:             "CertificateSigningRequestPending",
						Message:            "Certificate signing request pending, waiting for approval",
						ObservedGeneration: sampleSession.Generation,
					},
				},
			},
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
				getCSRErr:          apierrors.NewTimeoutError("timeout", 5),
			},
			expectAction: false,
			expectedErr:  true,
		},
		{
			name: "transient error retrieving CSR approval",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                unapprovedCSR,
				getCSRApprovalErr:  apierrors.NewTimeoutError("timeout", 5),
			},
			expectAction: false,
			expectedErr:  true,
		},
		{
			name: "forbidden error retrieving CSR approval sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                unapprovedCSR,
				getCSRApprovalErr:  apierrors.NewForbidden(schema.GroupResource{Resource: "certificatesigningrequestapprovals"}, "test-session", errors.New("access denied")),
			},
			expectAction: true,
			expectedErr:  false,
		},
		{
			name: "infrastructure error retrieving CSR approval sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                unapprovedCSR,
				getCSRApprovalErr:  apierrors.NewInternalError(errors.New("internal server error")),
			},
			expectAction: true,
			expectedErr:  false,
		},
		{
			name: "session with credentials in secret but no status update",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
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
				hostedControlPlane: buildHCP(true),
				csr:                signedCSR,
				csrApproval:        csrApproval,
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
				clock:            clocktesting.NewFakeClock(fixedTime),
				endpointProvider: &mockEndpointProvider{},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					if tt.getSecretErr != nil {
						return nil, tt.getSecretErr
					}
					if tt.existingSecret != nil && tt.existingSecret.Namespace == namespace && tt.existingSecret.Name == name {
						return tt.existingSecret, nil
					}
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					if tt.newPrivateKeyErr != nil {
						return nil, tt.newPrivateKeyErr
					}
					return testPrivateKey, nil
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, tt.mq)

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
		mq            ManagementClusterQuerier
		expectAction  bool
		expectedErr   bool
	}{
		{
			name: "session with credentials but no backend URL",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
					credentialsAvailableCondition,
				},
				// BackendKASURL missing
			},
			mq: &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
			},
			expectAction: true,
		},
		{
			name: "transient error retrieving HostedControlPlane",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
					credentialsAvailableCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				getHostedControlPlaneErr: apierrors.NewServiceUnavailable("service unavailable"),
			},
			expectAction: false,
			expectedErr:  true,
		},
		{
			name: "HostedControlPlane not found sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
					credentialsAvailableCondition,
				},
			},
			mq:           &mockManagementClusterQuerier{},
			expectAction: true,
			expectedErr:  false,
		},
		{
			name: "forbidden error retrieving HostedControlPlane sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
					credentialsAvailableCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				getHostedControlPlaneErr: apierrors.NewForbidden(schema.GroupResource{Resource: "hostedcontrolplanes"}, "clusters-test-hcp", errors.New("access denied")),
			},
			expectAction: true,
			expectedErr:  false,
		},
		{
			name: "infrastructure error retrieving HostedControlPlane sets condition",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
					credentialsAvailableCondition,
				},
			},
			mq: &mockManagementClusterQuerier{
				getHostedControlPlaneErr: apierrors.NewInternalError(errors.New("internal server error")),
			},
			expectAction: true,
			expectedErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := sampleSession.DeepCopy()
			testSession.Status = tt.sessionStatus

			// Setup controller with mock getters
			controller := &SessionController{
				clock:            clocktesting.NewFakeClock(fixedTime),
				endpointProvider: &mockEndpointProvider{},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					if secretWithFullTestCredentials.Namespace == namespace && secretWithFullTestCredentials.Name == name {
						return secretWithFullTestCredentials, nil
					}
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return nil, errors.New("should not be called in these tests")
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, tt.mq)

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
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				BackendKASURL:        "https://api.test-hcp.example.com",
				Conditions: []metav1.Condition{
					sessionNotReadyCondition,
					hostedControlPlaneAvailableCondition,
					credentialsAvailableCondition,
					networkPathAvailableCondition,
				},
				// Endpoint missing
			},
			expectAction: true,
		},
		{
			name: "session already ready",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:            &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				CredentialsSecretRef: "sessiongate-9b1f64c3",
				BackendKASURL:        "https://api.test-hcp.example.com",
				Conditions: []metav1.Condition{
					hostedControlPlaneAvailableCondition,
					credentialsAvailableCondition,
					networkPathAvailableCondition,
					{
						Type:               string(sessiongatev1alpha1.SessionConditionTypeReady),
						Status:             metav1.ConditionTrue,
						Reason:             "Ready",
						Message:            "Session is ready",
						ObservedGeneration: sampleSession.Generation,
						LastTransitionTime: metav1.Time{Time: fixedTime},
					},
				},
				Endpoint: "https://localhost:8080/sessiongate/test-session/kas",
			},
			expectAction: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := sampleSession.DeepCopy()
			testSession.Status = tt.sessionStatus

			// Setup controller with mock getters
			controller := &SessionController{
				clock: clocktesting.NewFakeClock(fixedTime),
				endpointProvider: &mockEndpointProvider{
					endpoint: "https://localhost:8080/sessiongate/test-session/kas",
				},
				getSecret: func(namespace, name string) (*corev1.Secret, error) {
					if secretWithFullTestCredentials.Namespace == namespace && secretWithFullTestCredentials.Name == name {
						return secretWithFullTestCredentials, nil
					}
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return nil, errors.New("should not be called in these tests")
				},
			}

			// Execute
			action, err := controller.processSession(t.Context(), testSession, &mockManagementClusterQuerier{
				hostedControlPlane: buildHCP(true),
			})

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
