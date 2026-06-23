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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/systemadmincredential"
)

func TestCrrPreviousCertificatesRevoked(t *testing.T) {
	tests := []struct {
		name   string
		crr    *certificatesv1alpha1.CertificateRevocationRequest
		expect bool
	}{
		{
			name:   "no conditions returns false",
			crr:    &certificatesv1alpha1.CertificateRevocationRequest{},
			expect: false,
		},
		{
			name: "PreviousCertificatesRevoked=True returns true",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					Conditions: []metav1.Condition{
						{Type: certificatesv1alpha1.PreviousCertificatesRevokedType, Status: metav1.ConditionTrue},
					},
				},
			},
			expect: true,
		},
		{
			name: "PreviousCertificatesRevoked=False returns false",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					Conditions: []metav1.Condition{
						{Type: certificatesv1alpha1.PreviousCertificatesRevokedType, Status: metav1.ConditionFalse},
					},
				},
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := crrPreviousCertificatesRevoked(tt.crr)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestCrrFailureReason(t *testing.T) {
	tests := []struct {
		name   string
		crr    *certificatesv1alpha1.CertificateRevocationRequest
		expect string
	}{
		{
			name:   "no conditions returns empty",
			crr:    &certificatesv1alpha1.CertificateRevocationRequest{},
			expect: "",
		},
		{
			name: "Failed=True with reason returns reason",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					Conditions: []metav1.Condition{
						{Type: "Failed", Status: metav1.ConditionTrue, Reason: "SignerBusy"},
					},
				},
			},
			expect: "SignerBusy",
		},
		{
			name: "Failed=True without reason returns Failed",
			crr: &certificatesv1alpha1.CertificateRevocationRequest{
				Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
					Conditions: []metav1.Condition{
						{Type: "Failed", Status: metav1.ConditionTrue},
					},
				},
			},
			expect: "Failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := crrFailureReason(tt.crr)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestOperationRevokeCredentialsPoll_ShouldProcess(t *testing.T) {
	tests := []struct {
		name   string
		op     *api.Operation
		expect bool
	}{
		{
			name:   "accepts Deleting RevokeCredentials operation",
			op:     testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateDeleting),
			expect: true,
		},
		{
			name:   "rejects terminal operation",
			op:     testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateSucceeded),
			expect: false,
		},
		{
			name:   "rejects non-RevokeCredentials operation",
			op:     testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateDeleting),
			expect: false,
		},
		{
			name:   "rejects Accepted status (not yet dispatched)",
			op:     testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted),
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &operationRevokeCredentialsPoll{}
			got := syncer.ShouldProcess(context.Background(), tt.op)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestOperationRevokeCredentialsPoll_SynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	revokeSuffix := systemadmincredential.RevokeOpSuffix(testOperationName)

	crrRevoked := &certificatesv1alpha1.CertificateRevocationRequest{
		Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
			Conditions: []metav1.Condition{
				{Type: certificatesv1alpha1.PreviousCertificatesRevokedType, Status: metav1.ConditionTrue},
			},
		},
	}

	crrFailed := &certificatesv1alpha1.CertificateRevocationRequest{
		Status: certificatesv1alpha1.CertificateRevocationRequestStatus{
			Conditions: []metav1.Condition{
				{Type: "Failed", Status: metav1.ConditionTrue, Reason: "CriticalFailure"},
			},
		},
	}

	marshalCRR := func(crr *certificatesv1alpha1.CertificateRevocationRequest) []byte {
		data, _ := json.Marshal(crr)
		return data
	}

	crrReadDesireName := systemadmincredential.CRRNamePrefix + "-" + revokeSuffix

	tests := []struct {
		name     string
		setupDB  func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients, *databasetesting.MockKubeApplierDBClient)
		wantErr  bool
		verifyDB func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "cluster not found succeeds the operation",
			setupDB: func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients, *databasetesting.MockKubeApplierDBClient) {
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateDeleting)
				db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{op})
				require.NoError(t, err)
				kaClient := databasetesting.NewMockKubeApplierDBClient()
				return db, testMockKubeApplierDBClients(kaClient), kaClient
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "CRR failed transitions operation to Failed",
			setupDB: func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients, *databasetesting.MockKubeApplierDBClient) {
				cluster := testCluster()
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName
				spc := testSPC(testMCResourceID())
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateDeleting)
				db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc, op})
				require.NoError(t, err)

				kaClient := databasetesting.NewMockKubeApplierDBClient()
				// Add CRR ReadDesire with Failed condition
				rd := testReadDesireWithKubeContent(testClusterRID(), crrReadDesireName, marshalCRR(crrFailed))
				kaClient, err = databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{rd})
				require.NoError(t, err)
				return db, testMockKubeApplierDBClients(kaClient), kaClient
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
			},
		},
		{
			name: "CRR revoked with no credentials succeeds and clears sentinel",
			setupDB: func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients, *databasetesting.MockKubeApplierDBClient) {
				cluster := testCluster()
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName
				spc := testSPC(testMCResourceID())
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateDeleting)
				db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc, op})
				require.NoError(t, err)

				kaClient := databasetesting.NewMockKubeApplierDBClient()
				rd := testReadDesireWithKubeContent(testClusterRID(), crrReadDesireName, marshalCRR(crrRevoked))
				kaClient, err = databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{rd})
				require.NoError(t, err)
				return db, testMockKubeApplierDBClients(kaClient), kaClient
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Cluster sentinel should be cleared
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Empty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testCtx(t)
			fakeClock := clocktesting.NewFakePassiveClock(fixedTime)

			db, kaClients, kaClient := tt.setupDB(ctx, t)
			clusterLister := newMockClusterLister(db)
			rdLister := newMockReadDesireLister(kaClient)

			syncer := &operationRevokeCredentialsPoll{
				clock:                fakeClock,
				clusterLister:        clusterLister,
				resourcesDBClient:    db,
				kubeApplierDBClients: kaClients,
				readDesireLister:     rdLister,
			}

			err := syncer.SynchronizeOperation(ctx, testOperationKey())
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
