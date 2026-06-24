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

package systemadmincredentialcontrollers

import (
	"context"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestIssuanceObserverSyncer_SyncOnce(t *testing.T) {
	testCredName := "testcred111111aa"
	desireName := systemadmincredhelpers.DesireNameCSR(testCredName)

	tests := []struct {
		name        string
		credentials []*api.SystemAdminCredential
		readDesires []*kubeapplier.ReadDesire
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
		wantErr     bool
	}{
		{
			name: "CSR certificate issued transitions to Issued",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseRequested),
			},
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				csr := &certificatesv1.CertificateSigningRequest{
					Status: certificatesv1.CertificateSigningRequestStatus{
						Certificate: []byte("test-signed-cert"),
					},
				}
				csrJSON, _ := json.Marshal(csr)
				rd.Status.KubeContent = &runtime.RawExtension{Raw: csrJSON}
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Equal(t, api.SystemAdminCredentialPhaseIssued, cred.Status.Phase)
					assert.NotEmpty(t, cred.Status.SignedCertificate)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name: "CSR denied transitions to Failed",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseRequested),
			},
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				csr := &certificatesv1.CertificateSigningRequest{
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{
							{
								Type:   certificatesv1.CertificateDenied,
								Status: "True",
								Reason: "test-denied",
							},
						},
					},
				}
				csrJSON, _ := json.Marshal(csr)
				rd.Status.KubeContent = &runtime.RawExtension{Raw: csrJSON}
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Equal(t, api.SystemAdminCredentialPhaseFailed, cred.Status.Phase)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name: "CSR Failed condition transitions to Failed",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseRequested),
			},
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				csr := &certificatesv1.CertificateSigningRequest{
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{
							{
								Type:    "Failed",
								Status:  "True",
								Reason:  "test-failed",
								Message: "test failure",
							},
						},
					},
				}
				csrJSON, _ := json.Marshal(csr)
				rd.Status.KubeContent = &runtime.RawExtension{Raw: csrJSON}
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Equal(t, api.SystemAdminCredentialPhaseFailed, cred.Status.Phase)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name: "CSR not yet observed does nothing",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseRequested),
			},
			readDesires: func() []*kubeapplier.ReadDesire {
				rd := newTestClusterScopedReadDesire(desireName)
				// nil KubeContent
				return []*kubeapplier.ReadDesire{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Equal(t, api.SystemAdminCredentialPhaseRequested, cred.Status.Phase)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name: "ReadDesire not found does nothing",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseRequested),
			},
			readDesires: nil,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Equal(t, api.SystemAdminCredentialPhaseRequested, cred.Status.Phase)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name: "skips non-Requested credentials",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseIssued),
			},
			readDesires: nil,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Equal(t, api.SystemAdminCredentialPhaseIssued, cred.Status.Phase)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name:        "no credentials does nothing",
			credentials: nil,
			readDesires: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{newTestCluster()}
			for _, cred := range tt.credentials {
				resources = append(resources, cred)
			}

			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			syncer := &issuanceObserverSyncer{
				cooldownChecker:   &alwaysSyncCooldownChecker{},
				resourcesDBClient: mockDB,
				readDesireLister: &listertesting.SliceReadDesireLister{
					Desires: tt.readDesires,
				},
			}

			err = syncer.SyncOnce(ctx, newTestClusterKey())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB)
			}
		})
	}
}

// Ensure the import for metav1 is used.
var _ metav1.Time
