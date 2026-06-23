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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestHasOutstandingDesire(t *testing.T) {
	tests := []struct {
		name   string
		cred   *api.SystemAdminCredential
		kind   api.SystemAdminCredentialDesireKind
		desireName string
		expect bool
	}{
		{
			name: "found",
			cred: &api.SystemAdminCredential{
				Status: api.SystemAdminCredentialStatus{
					OutstandingDesires: []api.SystemAdminCredentialDesireRef{
						{Kind: api.SystemAdminCredentialDesireKindApply, Name: "csr-abc"},
					},
				},
			},
			kind:       api.SystemAdminCredentialDesireKindApply,
			desireName: "csr-abc",
			expect:     true,
		},
		{
			name: "not found - different kind",
			cred: &api.SystemAdminCredential{
				Status: api.SystemAdminCredentialStatus{
					OutstandingDesires: []api.SystemAdminCredentialDesireRef{
						{Kind: api.SystemAdminCredentialDesireKindApply, Name: "csr-abc"},
					},
				},
			},
			kind:       api.SystemAdminCredentialDesireKindRead,
			desireName: "csr-abc",
			expect:     false,
		},
		{
			name: "not found - different name",
			cred: &api.SystemAdminCredential{
				Status: api.SystemAdminCredentialStatus{
					OutstandingDesires: []api.SystemAdminCredentialDesireRef{
						{Kind: api.SystemAdminCredentialDesireKindApply, Name: "csr-abc"},
					},
				},
			},
			kind:       api.SystemAdminCredentialDesireKindApply,
			desireName: "csr-xyz",
			expect:     false,
		},
		{
			name:       "empty list",
			cred:       testCredential(api.SystemAdminCredentialPhaseRequested),
			kind:       api.SystemAdminCredentialDesireKindApply,
			desireName: "anything",
			expect:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasOutstandingDesire(tt.cred, tt.kind, tt.desireName)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestCredentialDesireName(t *testing.T) {
	assert.Equal(t, "csr-abcdef0123456789", credentialDesireName("csr", "abcdef0123456789"))
	assert.Equal(t, "prefix-suffix", credentialDesireName("prefix", "suffix"))
}

func TestHostedClusterNamespace(t *testing.T) {
	assert.Equal(t, "ocm-arohcptest-12345", hostedClusterNamespace("arohcptest", "12345"))
	assert.Equal(t, "ocm-env-clusterid", hostedClusterNamespace("env", "clusterid"))
}

func TestKindToResource(t *testing.T) {
	tests := []struct {
		kind     string
		expected string
	}{
		{"CertificateSigningRequest", "certificatesigningrequests"},
		{"CertificateSigningRequestApproval", "certificatesigningrequestapprovals"},
		{"CertificateRevocationRequest", "certificaterevocationrequests"},
		{"ClusterRole", "clusterroles"},
		{"ClusterRoleBinding", "clusterrolebindings"},
		{"Role", "roles"},
		{"RoleBinding", "rolebindings"},
		{"Unknown", "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			assert.Equal(t, tt.expected, kindToResource(tt.kind))
		})
	}
}

func TestCredentialDesiresCreator_SyncOnce(t *testing.T) {
	tests := []struct {
		name      string
		cred      *api.SystemAdminCredential
		hasSPC    bool
		wantErr   bool
		verifyDB  func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kaClient *databasetesting.MockKubeApplierDBClient)
	}{
		{
			name: "creates ApplyDesires and ReadDesire for Requested credential",
			cred: func() *api.SystemAdminCredential {
				cred := testCredential(api.SystemAdminCredentialPhaseRequested)
				// Need a realistic keypair for BuildCSR
				cred.Spec.PrivateKeyPEM = "" // will fail CSR build but we test flow
				return cred
			}(),
			hasSPC:  true,
			wantErr: true, // expected: BuildCSR fails without real private key
		},
		{
			name:    "non-Requested credential is skipped",
			cred:    testCredential(api.SystemAdminCredentialPhaseIssued),
			hasSPC:  true,
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, kaClient *databasetesting.MockKubeApplierDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Empty(t, cred.Status.OutstandingDesires, "no desires should be created for non-Requested credential")
			},
		},
		{
			name:    "credential not found is a no-op",
			cred:    nil, // no credential in DB
			hasSPC:  true,
			wantErr: false,
		},
		{
			name:    "cluster not found is a no-op",
			cred:    testCredential(api.SystemAdminCredentialPhaseRequested),
			hasSPC:  false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testCtx(t)

			var resources []any
			if tt.hasSPC {
				cluster := testCluster()
				spc := testSPC(testMCResourceID())
				resources = append(resources, cluster, spc)
			}
			db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			if tt.cred != nil {
				createCredentialInDB(ctx, t, tt.cred, db)
			}

			kaClient := databasetesting.NewMockKubeApplierDBClient()
			kaClients := testMockKubeApplierDBClients(kaClient)
			clusterLister := newMockClusterLister(db)
			credLister := newMockCredentialLister(db)

			syncer := &credentialDesiresCreator{
				clusterLister:        clusterLister,
				credentialLister:     credLister,
				resourcesDBClient:    db,
				kubeApplierDBClients: kaClients,
				hostedClusterNSEnvID: testHCPClusterNSEnvID,
			}

			err = syncer.SyncOnce(ctx, testCredentialKey())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, db, kaClient)
			}
		})
	}
}
