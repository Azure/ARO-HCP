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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
)

func TestCsrDenied(t *testing.T) {
	tests := []struct {
		name   string
		csr    *certificatesv1.CertificateSigningRequest
		expect string
	}{
		{
			name:   "no conditions returns empty",
			csr:    &certificatesv1.CertificateSigningRequest{},
			expect: "",
		},
		{
			name: "CertificateDenied with reason returns reason",
			csr: &certificatesv1.CertificateSigningRequest{
				Status: certificatesv1.CertificateSigningRequestStatus{
					Conditions: []certificatesv1.CertificateSigningRequestCondition{
						{Type: certificatesv1.CertificateDenied, Status: "True", Reason: "PolicyViolation"},
					},
				},
			},
			expect: "PolicyViolation",
		},
		{
			name: "CertificateDenied without reason returns Denied",
			csr: &certificatesv1.CertificateSigningRequest{
				Status: certificatesv1.CertificateSigningRequestStatus{
					Conditions: []certificatesv1.CertificateSigningRequestCondition{
						{Type: certificatesv1.CertificateDenied, Status: "True"},
					},
				},
			},
			expect: "Denied",
		},
		{
			name: "CertificateFailed with reason returns reason",
			csr: &certificatesv1.CertificateSigningRequest{
				Status: certificatesv1.CertificateSigningRequestStatus{
					Conditions: []certificatesv1.CertificateSigningRequestCondition{
						{Type: certificatesv1.CertificateFailed, Status: "True", Reason: "SignerError"},
					},
				},
			},
			expect: "SignerError",
		},
		{
			name: "CertificateDenied with Status=False is not matched",
			csr: &certificatesv1.CertificateSigningRequest{
				Status: certificatesv1.CertificateSigningRequestStatus{
					Conditions: []certificatesv1.CertificateSigningRequestCondition{
						{Type: certificatesv1.CertificateDenied, Status: "False", Reason: "Ignored"},
					},
				},
			},
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := csrDenied(tt.csr)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestIssuanceObserver_SyncOnce(t *testing.T) {
	certBytes := []byte("signed-certificate-data")

	csrWithCert := &certificatesv1.CertificateSigningRequest{
		Status: certificatesv1.CertificateSigningRequestStatus{
			Certificate: certBytes,
		},
	}

	csrDeniedObj := &certificatesv1.CertificateSigningRequest{
		Status: certificatesv1.CertificateSigningRequestStatus{
			Conditions: []certificatesv1.CertificateSigningRequestCondition{
				{Type: certificatesv1.CertificateDenied, Status: "True", Reason: "PolicyViolation"},
			},
		},
	}

	csrPending := &certificatesv1.CertificateSigningRequest{
		Status: certificatesv1.CertificateSigningRequestStatus{},
	}

	marshalCSR := func(csr *certificatesv1.CertificateSigningRequest) []byte {
		data, _ := json.Marshal(csr)
		return data
	}

	tests := []struct {
		name      string
		cred      *api.SystemAdminCredential
		csrJSON   []byte
		wantErr   bool
		verifyDB  func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:    "CSR with certificate flips credential to Issued",
			cred:    testCredential(api.SystemAdminCredentialPhaseRequested),
			csrJSON: marshalCSR(csrWithCert),
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Equal(t, api.SystemAdminCredentialPhaseIssued, cred.Status.Phase)
				assert.NotEmpty(t, cred.Status.SignedCertificate)
			},
		},
		{
			name:    "CSR denied flips credential to Failed",
			cred:    testCredential(api.SystemAdminCredentialPhaseRequested),
			csrJSON: marshalCSR(csrDeniedObj),
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Equal(t, api.SystemAdminCredentialPhaseFailed, cred.Status.Phase)
				// Should have a CSRDenied condition
				found := false
				for _, c := range cred.Status.Conditions {
					if c.Type == "CSRDenied" {
						found = true
						assert.Equal(t, metav1.ConditionTrue, c.Status)
					}
				}
				assert.True(t, found, "CSRDenied condition should be present")
			},
		},
		{
			name:    "CSR pending is a no-op",
			cred:    testCredential(api.SystemAdminCredentialPhaseRequested),
			csrJSON: marshalCSR(csrPending),
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Equal(t, api.SystemAdminCredentialPhaseRequested, cred.Status.Phase)
			},
		},
		{
			name: "non-Requested credential is skipped",
			cred: testCredential(api.SystemAdminCredentialPhaseIssued),
			// CSR data doesn't matter — the credential won't be checked
			csrJSON: marshalCSR(csrWithCert),
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Equal(t, api.SystemAdminCredentialPhaseIssued, cred.Status.Phase)
			},
		},
		{
			name:    "no CSR mirrored yet is a no-op",
			cred:    testCredential(api.SystemAdminCredentialPhaseRequested),
			csrJSON: nil, // no ReadDesire content
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Equal(t, api.SystemAdminCredentialPhaseRequested, cred.Status.Phase)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testCtx(t)

			cluster := testCluster()
			db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster})
			require.NoError(t, err)
			createCredentialInDB(ctx, t, tt.cred, db)

			// Set up kube-applier DB with the CSR ReadDesire
			kaClient := databasetesting.NewMockKubeApplierDBClient()
			readDesireName := systemadmincredential.CSRNamePrefix + "-" + testCredentialName
			if tt.csrJSON != nil {
				rd := testReadDesireWithKubeContent(testClusterRID(), readDesireName, tt.csrJSON)
				_, err = databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{rd})
				if err != nil {
					// If that fails, add it directly
					readCRUD, _ := kaClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
					readCRUD.Create(ctx, rd, nil)
				} else {
					// Re-create with the resource
					kaClient, err = databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{rd})
					require.NoError(t, err)
				}
			}

			rdLister := newMockReadDesireLister(kaClient)

			syncer := &issuanceObserver{
				resourcesDBClient: db,
				readDesireLister:  rdLister,
			}

			err = syncer.SyncOnce(ctx, testClusterKey())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, db)
			}
		})
	}
}
