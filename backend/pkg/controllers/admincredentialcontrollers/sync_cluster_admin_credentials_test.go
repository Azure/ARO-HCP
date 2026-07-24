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

package admincredentialcontrollers

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID            = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName         = "test-rg"
	testClusterName               = "test-cluster"
	testClusterServiceIDStr       = "/api/clusters_mgmt/v1/clusters/abc123"
	testBreakGlassCredentialIDStr = "/api/clusters_mgmt/v1/clusters/abc123/break_glass_credentials/bgc123"
	testOperationName             = "test-operation-id"
)

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

type neverSyncCooldownChecker struct{}

func (c *neverSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return false
}

func fakeOCMNotFoundError() error {
	e, _ := ocmerrors.NewError().Status(http.StatusNotFound).Reason("not found").Build()
	return e
}

func TestSyncClusterAdminCredentials_SyncOnce(t *testing.T) {
	t.Parallel()

	expiration := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	breakGlassID := api.Must(api.NewInternalID(testBreakGlassCredentialIDStr))
	key := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	tests := []struct {
		name           string
		cluster        *api.HCPOpenShiftCluster
		cred           *api.ClusterAdminCredential
		includeCred    bool
		cooldown       controllerutil.CooldownChecker
		setupMockCS    func(mock *ocm.MockClusterServiceClientSpec)
		expectError    bool
		wantErrContain string
		verify         func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:        "cooldown active skips sync",
			cluster:     newTestCluster(),
			includeCred: true,
			cred:        mustNewAdminCredential(t, breakGlassID, api.ClusterAdminCredentialStatusCreated, "", time.Time{}),
			cooldown:    &neverSyncCooldownChecker{},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).AdminCredentials(testClusterName).Get(ctx, "bgc123")
				require.NoError(t, err)
				assert.Equal(t, api.ClusterAdminCredentialStatusCreated, stored.Status)
				assert.Empty(t, stored.Kubeconfig)
			},
		},
		{
			name: "cluster not found is a no-op",
			setupMockCS: func(mock *ocm.MockClusterServiceClientSpec) {
				// no CS calls expected
			},
		},
		{
			name: "cluster with deletion timestamp is a no-op",
			cluster: newTestCluster(func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now().UTC()}
			}),
			includeCred: true,
			cred:        mustNewAdminCredential(t, breakGlassID, api.ClusterAdminCredentialStatusCreated, "", time.Time{}),
		},
		{
			name:        "CS 404 deletes cosmos admin credential",
			cluster:     newTestCluster(),
			includeCred: true,
			cred:        mustNewAdminCredential(t, breakGlassID, api.ClusterAdminCredentialStatusIssued, "old-kubeconfig", expiration),
			setupMockCS: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetBreakGlassCredential(gomock.Any(), breakGlassID).
					Return(nil, fakeOCMNotFoundError())
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).AdminCredentials(testClusterName).Get(ctx, "bgc123")
				require.True(t, database.IsNotFoundError(err), "expected not found, got %v", err)
			},
		},
		{
			name:        "CS issued credential updates status kubeconfig and expiration",
			cluster:     newTestCluster(),
			includeCred: true,
			cred:        mustNewAdminCredential(t, breakGlassID, api.ClusterAdminCredentialStatusCreated, "", time.Time{}),
			setupMockCS: func(mock *ocm.MockClusterServiceClientSpec) {
				csCred, err := cmv1.NewBreakGlassCredential().
					HREF(testBreakGlassCredentialIDStr).
					Status(cmv1.BreakGlassCredentialStatusIssued).
					ExpirationTimestamp(expiration).
					Kubeconfig("kubeconfig-data").
					Build()
				require.NoError(t, err)
				mock.EXPECT().
					GetBreakGlassCredential(gomock.Any(), breakGlassID).
					Return(csCred, nil)
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).AdminCredentials(testClusterName).Get(ctx, "bgc123")
				require.NoError(t, err)
				assert.Equal(t, api.ClusterAdminCredentialStatusIssued, stored.Status)
				assert.Equal(t, "kubeconfig-data", stored.Kubeconfig)
				assert.True(t, stored.ExpirationTimestamp.Equal(expiration), "expiration: got %v want %v", stored.ExpirationTimestamp, expiration)
			},
		},
		{
			name:        "unchanged CS state is a no-op replace",
			cluster:     newTestCluster(),
			includeCred: true,
			cred:        mustNewAdminCredential(t, breakGlassID, api.ClusterAdminCredentialStatusIssued, "kubeconfig-data", expiration),
			setupMockCS: func(mock *ocm.MockClusterServiceClientSpec) {
				csCred, err := cmv1.NewBreakGlassCredential().
					HREF(testBreakGlassCredentialIDStr).
					Status(cmv1.BreakGlassCredentialStatusIssued).
					ExpirationTimestamp(expiration).
					Kubeconfig("kubeconfig-data").
					Build()
				require.NoError(t, err)
				mock.EXPECT().
					GetBreakGlassCredential(gomock.Any(), breakGlassID).
					Return(csCred, nil)
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).AdminCredentials(testClusterName).Get(ctx, "bgc123")
				require.NoError(t, err)
				assert.Equal(t, api.ClusterAdminCredentialStatusIssued, stored.Status)
				assert.Equal(t, "kubeconfig-data", stored.Kubeconfig)
			},
		},
		{
			name:        "empty ClusterServiceInternalID returns error",
			cluster:     newTestCluster(),
			includeCred: true,
			cred: func() *api.ClusterAdminCredential {
				cred := mustNewAdminCredential(t, breakGlassID, api.ClusterAdminCredentialStatusCreated, "", time.Time{})
				cred.ClusterServiceInternalID = api.InternalID{}
				return cred
			}(),
			expectError:    true,
			wantErrContain: "unexpected empty ClusterServiceInternalID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			resources := []any{}
			if tt.cluster != nil {
				resources = append(resources, tt.cluster)
			}
			if tt.includeCred {
				resources = append(resources, tt.cred)
			}

			mockResourcesDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tt.setupMockCS != nil {
				tt.setupMockCS(mockCSClient)
			}

			cooldown := tt.cooldown
			if cooldown == nil {
				cooldown = &alwaysSyncCooldownChecker{}
			}

			syncer := &syncClusterAdminCredentialsSyncer{
				resourcesDBClient:                   mockResourcesDB,
				clusterLister:                       &listertesting.DBClusterLister{ResourcesDBClient: mockResourcesDB},
				adminCredentialLister:               &listertesting.DBAdminCredentialLister{ResourcesDBClient: mockResourcesDB},
				clustersServiceClient:               mockCSClient,
				minimumReconcileTimeCooldownChecker: cooldown,
			}

			err = syncer.SyncOnce(ctx, key)
			if tt.expectError {
				require.Error(t, err)
				if tt.wantErrContain != "" {
					assert.Contains(t, err.Error(), tt.wantErrContain)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDB)
			}
		})
	}
}

func newTestCluster(opts ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	cluster := &api.HCPOpenShiftCluster{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: api.Ptr(api.Must(api.NewInternalID(testClusterServiceIDStr))),
		},
	}
	for _, opt := range opts {
		opt(cluster)
	}
	return cluster
}

func mustNewAdminCredential(t *testing.T, breakGlassID api.InternalID, status api.ClusterAdminCredentialStatus, kubeconfig string, expiration time.Time) *api.ClusterAdminCredential {
	t.Helper()
	clusterResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName,
	))
	cred, err := database.NewClusterAdminCredential(clusterResourceID, breakGlassID, testOperationName)
	require.NoError(t, err)
	cred.Status = status
	cred.Kubeconfig = kubeconfig
	cred.ExpirationTimestamp = expiration
	return cred
}
