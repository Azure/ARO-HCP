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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	certificatesv1 "k8s.io/api/certificates/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestIssuanceObserver_SyncOnce(t *testing.T) {
	fixedTime := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(fixedTime)

	testKey := controllerutils.SystemAdminCredentialRequestKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		CredentialName:    testCredentialName,
	}

	tests := []struct {
		name             string
		setupDB          func(db *databasetesting.MockResourcesDBClient)
		readDesireLister *listertesting.SliceReadDesireLister
		expectError      bool
		verify           func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "transitions credential to Issued when CSR has certificate",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName)
			},
			readDesireLister: makeReadDesireListerWithCSR(t,
				testCredentialName,
				&certificatesv1.CertificateSigningRequest{
					Status: certificatesv1.CertificateSigningRequestStatus{
						Certificate: []byte("signed-cert-data"),
					},
				},
			),
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				cred, err := credCRUD.Get(ctx, testCredentialName)
				require.NoError(t, err)
				assert.True(t, cred.Status.IsIssued(), "credential should be in Issued state")
				assert.NotEmpty(t, cred.Status.SignedCertificate, "SignedCertificate should be set")
			},
		},
		{
			name: "transitions credential to Failed when CSR is denied",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName)
			},
			readDesireLister: makeReadDesireListerWithCSR(t,
				testCredentialName,
				&certificatesv1.CertificateSigningRequest{
					Status: certificatesv1.CertificateSigningRequestStatus{
						Conditions: []certificatesv1.CertificateSigningRequestCondition{
							{
								Type:    certificatesv1.CertificateDenied,
								Status:  "True",
								Reason:  "Denied",
								Message: "CSR was denied by the signer",
							},
						},
					},
				},
			),
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				cred, err := credCRUD.Get(ctx, testCredentialName)
				require.NoError(t, err)
				assert.True(t, cred.Status.IsFailed(), "credential should be in Failed state")
			},
		},
		{
			name: "no-op when CSR has no certificate and no denial",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName)
			},
			readDesireLister: makeReadDesireListerWithCSR(t,
				testCredentialName,
				&certificatesv1.CertificateSigningRequest{
					Status: certificatesv1.CertificateSigningRequestStatus{},
				},
			),
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				cred, err := credCRUD.Get(ctx, testCredentialName)
				require.NoError(t, err)
				assert.True(t, cred.Status.IsPending(), "credential should remain in Pending state")
			},
		},
		{
			name: "skips non-Pending credentials",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName,
					withCondition(api.SystemAdminCredentialRequestConditionIssued))
			},
			readDesireLister: &listertesting.SliceReadDesireLister{},
			expectError:      false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				cred, err := credCRUD.Get(ctx, testCredentialName)
				require.NoError(t, err)
				assert.True(t, cred.Status.IsIssued(), "credential should remain in Issued state")
			},
		},
		{
			name: "no-op when ReadDesire not yet created",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName)
			},
			readDesireLister: &listertesting.SliceReadDesireLister{},
			expectError:      false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				cred, err := credCRUD.Get(ctx, testCredentialName)
				require.NoError(t, err)
				assert.True(t, cred.Status.IsPending(), "credential should remain in Pending state")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := databasetesting.NewMockResourcesDBClient()
			tc.setupDB(db)

			syncer := &issuanceObserver{
				cooldownChecker:   &alwaysSyncCooldownChecker{},
				clock:             fakeClock,
				resourcesDBClient: db,
				readDesireLister:  tc.readDesireLister,
			}

			err := syncer.SyncOnce(ctx, testKey)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.verify != nil {
				tc.verify(t, ctx, db)
			}
		})
	}
}

func makeReadDesireListerWithCSR(t *testing.T, credName string, csr *certificatesv1.CertificateSigningRequest) *listertesting.SliceReadDesireLister {
	t.Helper()

	csrBytes, err := json.Marshal(csr)
	require.NoError(t, err)

	desireName := maestrohelpers.ReadDesireNameForSystemAdminCredentialRequestCSR(credName)
	resourceIDStr := kubeapplier.ToCredentialRequestScopedReadDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, credName, desireName,
	)
	resourceID := api.Must(azcorearm.ParseResourceID(resourceIDStr))

	return &listertesting.SliceReadDesireLister{
		Desires: []*kubeapplier.ReadDesire{
			{
				CosmosMetadata: api.CosmosMetadata{
					ResourceID: resourceID,
				},
				Status: kubeapplier.ReadDesireStatus{
					KubeContent: &kruntime.RawExtension{
						Raw: csrBytes,
					},
				},
			},
		},
	}
}
