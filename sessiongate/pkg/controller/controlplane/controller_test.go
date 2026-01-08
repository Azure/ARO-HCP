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

package controlplane

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/testing/protocmp"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	certapplyv1 "k8s.io/client-go/applyconfigurations/certificates/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	certificatesv1alpha1apply "github.com/openshift/hypershift/client/applyconfiguration/certificates/v1alpha1"
	securityv1beta1api "istio.io/api/security/v1beta1"
	typev1beta1 "istio.io/api/type/v1beta1"
	securityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
	"github.com/Azure/ARO-HCP/sessiongate/pkg/controller"
	sessiongatv1alpha1applyconfigurations "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/applyconfiguration/sessiongate/v1alpha1"
)

// mockManagementClusterProvider implements mc.ManagementClusterProvider for testing
type mockManagementClusterProvider struct {
	hostedCluster      *hypershiftv1beta1.HostedCluster
	hostedClusterError error
	csr                *certificatesv1.CertificateSigningRequest
	csrErr             error
	csrApproval        *certificatesv1alpha1.CertificateSigningRequestApproval
}

func (m *mockManagementClusterProvider) GetHostedCluster(ctx context.Context, namespace string) (*hypershiftv1beta1.HostedCluster, error) {
	if m.hostedClusterError != nil {
		return nil, m.hostedClusterError
	}
	if m.hostedCluster == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "hostedclusters"}, namespace)
	}
	return m.hostedCluster, nil
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

func (m *mockManagementClusterProvider) GetCSR(ctx context.Context, name string) (*certificatesv1.CertificateSigningRequest, error) {
	if m.csrErr != nil {
		return nil, m.csrErr
	}
	if m.csr == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, name)
	}
	return m.csr, nil
}

func (m *mockManagementClusterProvider) GetCSRApproval(ctx context.Context, namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
	if m.csrErr != nil {
		return nil, m.csrErr
	}
	if m.csrApproval == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequestapprovals"}, name)
	}
	return m.csrApproval, nil
}

func TestSessionController_processSession(t *testing.T) {
	fixedTime := time.Date(2025, 1, 7, 12, 0, 0, 0, time.UTC)
	nowFunc := func() time.Time { return fixedTime }

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	session := &sessiongatev1alpha1.Session{
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

	tests := []struct {
		name            string
		creationTime    time.Time
		sessionStatus   sessiongatev1alpha1.SessionStatus
		existingSecret  *corev1.Secret
		existingPolicy  *securityv1beta1.AuthorizationPolicy
		mc              *mockManagementClusterProvider
		expectedAction  *actions
		expectedRequeue bool
		expectedErr     bool
	}{
		{
			name:          "new session without expiration timestamp - should set expiration",
			sessionStatus: sessiongatev1alpha1.SessionStatus{},
			expectedAction: &actions{
				session: sessiongatv1alpha1applyconfigurations.Session("test-session", "test-namespace").
					WithStatus(sessiongatv1alpha1applyconfigurations.SessionStatus().
						WithExpiresAt(metav1.NewTime(fixedTime.Add(24 * time.Hour)))),
			},
		},
		{
			name: "session with expiration but no auth policy - should create auth policy",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt: &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
			},
			existingPolicy: nil, // No existing policy
			expectedAction: &actions{
				authPolicy: buildAuthorizationPolicy(session),
				event: &eventInfo{
					reason:     "AuthorizationPolicyGeneration",
					messageFmt: "Creating authorization policy for %s/%s.",
					args:       []any{"test-namespace", "test-session"},
				},
			},
		},
		{
			name: "session with policy but no secret - should create private key",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
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
			},
			existingSecret: nil,
			expectedAction: &actions{
				event: &eventInfo{
					reason:     "PrivateKeyGeneration",
					messageFmt: "Generating private key for %s/%s.",
					args:       []any{"test-namespace", "test-session"},
				},
				secret: corev1apply.Secret("test-session", "test-namespace").
					WithLabels(map[string]string{
						controller.LabelManagedBy: "test-controller",
					}).
					WithOwnerReferences(
						metav1apply.OwnerReference().
							WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
							WithKind("Session").
							WithName("test-session").
							WithUID(types.UID("test-uid")).
							WithController(true).
							WithBlockOwnerDeletion(true),
					).
					WithType(corev1.SecretTypeOpaque).
					WithData(map[string][]byte{
						"certificate": nil,
						"privateKey":  controller.EncodePrivateKey(privateKey),
					}),
			},
		},
		{
			name: "session with policy but no status ref - should update status",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt: &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				// AuthorizationPolicyRef is empty - should be updated
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
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
			},
			expectedAction: &actions{
				session: sessiongatv1alpha1applyconfigurations.Session("test-session", "test-namespace").
					WithStatus(sessiongatv1alpha1applyconfigurations.SessionStatus().
						WithAuthorizationPolicyRef("test-session")),
			},
		},
		{
			name: "session with secret but no credentials status ref - should update status",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				// CredentialsSecretRef is empty - should be updated
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
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
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"privateKey": pem.EncodeToMemory(&pem.Block{
						Type:  "RSA PRIVATE KEY",
						Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
					}),
				},
			},
			expectedAction: &actions{
				session: sessiongatv1alpha1applyconfigurations.Session("test-session", "test-namespace").
					WithStatus(sessiongatv1alpha1applyconfigurations.SessionStatus().
						WithCredentialsSecretRef("test-session")),
			},
		},
		{
			name: "session with private key in secret but no CSR - should create CSR",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
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
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"privateKey": pem.EncodeToMemory(&pem.Block{
						Type:  "RSA PRIVATE KEY",
						Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
					}),
				},
			},
			mc: &mockManagementClusterProvider{
				csr:    nil, // No CSR exists yet
				csrErr: apierrors.NewNotFound(schema.GroupResource{Resource: "certificatesigningrequests"}, "test-session"),
			},
			expectedAction: &actions{
				event: &eventInfo{
					reason:     "CSRGeneration",
					messageFmt: "Creating CSR for %s/%s on management cluster.",
					args:       []any{"test-namespace", "test-session"},
				},
				csr: certapplyv1.CertificateSigningRequest("test-session").
					WithSpec(certapplyv1.CertificateSigningRequestSpec().
						// Request field will be ignored in comparison
						WithSignerName("hypershift.openshift.io/clusters-test-hcp.sre-break-glass").
						WithExpirationSeconds(86353).
						WithUsages(
							certificatesv1.UsageClientAuth,
							certificatesv1.UsageDigitalSignature,
						)),
			},
		},
		{
			name: "session without CSR approval - should create approval",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
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
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"privateKey": pem.EncodeToMemory(&pem.Block{
						Type:  "RSA PRIVATE KEY",
						Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
					}),
				},
			},
			mc: func() *mockManagementClusterProvider {
				csrCfg, _ := createCSRApplyConfiguration(
					"test-session",
					"clusters-test-hcp",
					privateKey,
					"user@example.com",
					"break-glass",
				)
				validCSR := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-session",
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						Request:    csrCfg.Spec.Request,
						SignerName: *csrCfg.Spec.SignerName,
					},
				}
				return &mockManagementClusterProvider{
					csr:         validCSR,
					csrApproval: nil,
				}
			}(),
			expectedAction: &actions{
				event: &eventInfo{
					reason:     "CertificateApprovalGeneration",
					messageFmt: "Creating/updating CSR approval for %s/%s in management cluster namespace %s.",
					args:       []any{"test-namespace", "test-session", "clusters-test-hcp"},
				},
				csrApproval: &csrApprovalAction{
					namespace: "clusters-test-hcp",
					approval: certificatesv1alpha1apply.CertificateSigningRequestApproval("test-session", "clusters-test-hcp").
						WithLabels(map[string]string{
							"api.openshift.com/type": "break-glass-credential",
						}),
				},
			},
		},
		{
			name: "session with mismatched CSR - should delete and recreate",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
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
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"privateKey": pem.EncodeToMemory(&pem.Block{
						Type:  "RSA PRIVATE KEY",
						Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
					}),
				},
			},
			mc: func() *mockManagementClusterProvider {
				// Generate a different private key to simulate mismatch
				differentKey, _ := rsa.GenerateKey(rand.Reader, 2048)
				csrCfg, _ := createCSRApplyConfiguration(
					"test-session",
					"clusters-test-hcp",
					differentKey, // Wrong key
					"user@example.com",
					"break-glass",
				)
				mismatchedCSR := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-session",
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						Request:    csrCfg.Spec.Request,
						SignerName: *csrCfg.Spec.SignerName,
					},
				}
				return &mockManagementClusterProvider{
					csr: mismatchedCSR,
				}
			}(),
			expectedAction: &actions{
				event: &eventInfo{
					reason:     "CSRInvalid",
					messageFmt: "CSR for %s/%s is invalid (mismatched key or subject), deleting and recreating.",
					args:       []any{"test-namespace", "test-session"},
				},
				deleteCSR: true,
			},
		},
		{
			name:         "expired session - should delete",
			creationTime: fixedTime.Add(-48 * time.Hour),
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt: &metav1.Time{Time: fixedTime.Add(-24 * time.Hour)},
			},
			expectedAction: &actions{
				deleteSession: true,
				event: &eventInfo{
					reason:     "SessionExpiration",
					messageFmt: "Session has expired, deleting %s/%s.",
					args:       []interface{}{"test-namespace", "test-session"},
				},
			},
		},
		{
			name: "session with signed CSR - should extract certificate",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
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
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"privateKey": pem.EncodeToMemory(&pem.Block{
						Type:  "RSA PRIVATE KEY",
						Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
					}),
				},
			},
			mc: func() *mockManagementClusterProvider {
				csrCfg, _ := createCSRApplyConfiguration(
					"test-session",
					"clusters-test-hcp",
					privateKey,
					"user@example.com",
					"break-glass",
				)
				// CSR with signed certificate
				signedCSR := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-session",
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						Request:    csrCfg.Spec.Request,
						SignerName: *csrCfg.Spec.SignerName,
					},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Certificate: []byte("-----BEGIN CERTIFICATE-----\nMIICertificateData\n-----END CERTIFICATE-----"),
					},
				}
				existingApproval := &certificatesv1alpha1.CertificateSigningRequestApproval{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-session",
						Namespace: "clusters-test-hcp",
						Labels: map[string]string{
							"api.openshift.com/type": "break-glass-credential",
						},
					},
				}
				return &mockManagementClusterProvider{
					csr:         signedCSR,
					csrApproval: existingApproval,
				}
			}(),
			expectedAction: &actions{
				event: &eventInfo{
					reason:     "CertificateExtraction",
					messageFmt: "Extracting certificate from CSR for %s/%s.",
					args:       []any{"test-namespace", "test-session"},
				},
				secret: corev1apply.Secret("test-session", "test-namespace").
					WithLabels(map[string]string{
						"app.kubernetes.io/managed-by": "test-controller",
					}).
					WithOwnerReferences(metav1apply.OwnerReference().
						WithAPIVersion(sessiongatev1alpha1.SchemeGroupVersion.String()).
						WithKind("Session").
						WithName("test-session").
						WithUID(types.UID("test-uid")).
						WithController(true).
						WithBlockOwnerDeletion(true)).
					WithType(corev1.SecretTypeOpaque).
					WithData(map[string][]byte{
						"certificate": []byte("-----BEGIN CERTIFICATE-----\nMIICertificateData\n-----END CERTIFICATE-----"),
					}),
			},
		},
		{
			name: "session with certificate but no endpoint/backend URL - should update status",
			sessionStatus: sessiongatev1alpha1.SessionStatus{
				ExpiresAt:              &metav1.Time{Time: fixedTime.Add(24 * time.Hour)},
				AuthorizationPolicyRef: "test-session",
				CredentialsSecretRef:   "test-session",
				// Endpoint and BackendKASURL are empty - should be populated
			},
			existingPolicy: &securityv1beta1.AuthorizationPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
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
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-session",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"privateKey": pem.EncodeToMemory(&pem.Block{
						Type:  "RSA PRIVATE KEY",
						Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
					}),
					"certificate": []byte("-----BEGIN CERTIFICATE-----\nMIICertificateData\n-----END CERTIFICATE-----"),
				},
			},
			mc: func() *mockManagementClusterProvider {
				csrCfg, _ := createCSRApplyConfiguration(
					"test-session",
					"clusters-test-hcp",
					privateKey,
					"user@example.com",
					"break-glass",
				)
				signedCSR := &certificatesv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-session",
					},
					Spec: certificatesv1.CertificateSigningRequestSpec{
						Request:    csrCfg.Spec.Request,
						SignerName: *csrCfg.Spec.SignerName,
					},
					Status: certificatesv1.CertificateSigningRequestStatus{
						Certificate: []byte("-----BEGIN CERTIFICATE-----\nMIICertificateData\n-----END CERTIFICATE-----"),
					},
				}
				existingApproval := &certificatesv1alpha1.CertificateSigningRequestApproval{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-session",
						Namespace: "clusters-test-hcp",
						Labels: map[string]string{
							"api.openshift.com/type": "break-glass-credential",
						},
					},
				}
				hcp := &hypershiftv1beta1.HostedCluster{
					Spec: hypershiftv1beta1.HostedClusterSpec{
						KubeAPIServerDNSName: "api.test-hcp.example.com",
					},
				}
				return &mockManagementClusterProvider{
					csr:           signedCSR,
					csrApproval:   existingApproval,
					hostedCluster: hcp,
				}
			}(),
			expectedAction: &actions{
				event: &eventInfo{
					reason:     "SessionFinalization",
					messageFmt: "Finalizing session %s/%s with endpoint and backend URL.",
					args:       []any{"test-namespace", "test-session"},
				},
				session: sessiongatv1alpha1applyconfigurations.Session("test-session", "test-namespace").
					WithStatus(sessiongatv1alpha1applyconfigurations.SessionStatus().
						WithBackendKASURL("https://api.test-hcp.example.com").
						WithEndpoint("https://sessiongate.example.com/sessiongate/test-session/kas")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSession := session.DeepCopy()
			testSession.Status = tt.sessionStatus
			if !tt.creationTime.IsZero() {
				testSession.CreationTimestamp = metav1.Time{Time: tt.creationTime}
			}

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
					if tt.existingPolicy != nil && tt.existingPolicy.Namespace == namespace && tt.existingPolicy.Name == name {
						return tt.existingPolicy, nil
					}
					return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "authorizationpolicies"}, name)
				},
				newPrivateKey: func(size int) (*rsa.PrivateKey, error) {
					return privateKey, nil
				},
			}

			// Execute
			action, _, err := controller.processSession(t.Context(), testSession, tt.mc, nowFunc)

			// Verify error expectation
			if tt.expectedErr && err == nil {
				t.Errorf("expected error but got none")
			} else if !tt.expectedErr && err != nil {
				t.Errorf("expected no error but got: %v", err)
			}

			// Verify requeue expectation
			/*if requeue != tt.expectedRequeue {
				t.Errorf("expected requeue=%v but got %v", tt.expectedRequeue, requeue)
			}*/

			// Verify action
			if tt.expectedAction == nil && action != nil {
				t.Errorf("expected no action but got: %+v", action)
			} else if tt.expectedAction != nil && action == nil {
				t.Errorf("expected action but got none")
			} else if tt.expectedAction != nil && action != nil {
				// Validate action
				if err := action.validate(); err != nil {
					t.Errorf("action validation failed: %v", err)
				}
				if diff := cmp.Diff(tt.expectedAction, action, compareActions()...); diff != "" {
					t.Errorf("unexpected action (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func compareActions() []cmp.Option {
	return []cmp.Option{
		cmp.AllowUnexported(actions{}, eventInfo{}, csrApprovalAction{}),
		cmpopts.IgnoreUnexported(rsa.PrivateKey{}),
		// Ignore the CSR request bytes since they're derived from the private key
		// and we test private key generation separately
		cmpopts.IgnoreFields(certapplyv1.CertificateSigningRequestSpecApplyConfiguration{}, "Request"),
		protocmp.Transform(),
	}
}
