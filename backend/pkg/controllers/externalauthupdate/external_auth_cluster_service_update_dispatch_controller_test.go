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

package externalauthupdate

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testExternalAuthName    = "test-externalauth"
	testExternalAuthCSIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123/external_auth_config/external_auths/" + testExternalAuthName
)

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

type neverSyncCooldownChecker struct{}

func (c *neverSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return false
}

func TestExternalAuthClusterServiceUpdateDispatchSyncer_SyncOnce(t *testing.T) {
	externalAuthCSID := api.Must(api.NewInternalID(testExternalAuthCSIDStr))

	testKey := controllerutils.HCPExternalAuthKey{
		SubscriptionID:      testSubscriptionID,
		ResourceGroupName:   testResourceGroupName,
		HCPClusterName:      testClusterName,
		HCPExternalAuthName: testExternalAuthName,
	}

	newExternalAuthWithConfigDiff := func() *api.HCPOpenShiftClusterExternalAuth {
		return newTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
			ea.Properties.Issuer.URL = "https://changed.example.com"
		})
	}

	newFakeOCMParentClusterNotUpdatableError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("ExternalAuths can only be updated on clusters in an updatable state. The cluster requested is in 'updating' state.").
			Build()
		return e
	}

	newFakeOCMNoReadyNodePoolError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("console clients cannot be configured without a ready node pool. Please ensure at least one node pool is in 'ready' state before configuring console authentication").
			Build()
		return e
	}

	newFakeOCMUnrelatedBadRequestError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("some other validation error").
			Build()
		return e
	}

	testCases := []struct {
		name                                string
		existingExternalAuth                *api.HCPOpenShiftClusterExternalAuth
		setupMockCSClient                   func(mock *ocm.MockClusterServiceClientSpec)
		minimumReconcileTimeCooldownChecker controllerutil.CooldownChecker
		wantErr                             bool
		wantErrContain                      string
	}{
		{
			name: "skip without CS call when no CSID",
			existingExternalAuth: newTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.ClusterServiceID = nil
				ea.Properties.Issuer.URL = "https://changed.example.com"
			}),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                                "no-op when config matches",
			existingExternalAuth:                newTestExternalAuth(),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), externalAuthCSID).
					Return(mustBuildCSExternalAuthFromRP(t, newTestExternalAuth()), nil)
			},
		},
		{
			name:                                "dispatches CS call when config differs",
			existingExternalAuth:                newExternalAuthWithConfigDiff(),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), externalAuthCSID).
					Return(mustBuildCSExternalAuthFromRP(t, newTestExternalAuth()), nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), externalAuthCSID, gomock.Any()).
					Return(nil, nil)
			},
		},
		{
			name:                                "when CS update returns parent cluster not updatable no error is returned",
			existingExternalAuth:                newExternalAuthWithConfigDiff(),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), externalAuthCSID).
					Return(mustBuildCSExternalAuthFromRP(t, newTestExternalAuth()), nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), externalAuthCSID, gomock.Any()).
					Return(nil, newFakeOCMParentClusterNotUpdatableError())
			},
		},
		{
			name:                                "when CS update returns no ready node pool no error is returned",
			existingExternalAuth:                newExternalAuthWithConfigDiff(),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), externalAuthCSID).
					Return(mustBuildCSExternalAuthFromRP(t, newTestExternalAuth()), nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), externalAuthCSID, gomock.Any()).
					Return(nil, newFakeOCMNoReadyNodePoolError())
			},
		},
		{
			name:                                "when CS update returns unhandled error error is propagated",
			existingExternalAuth:                newExternalAuthWithConfigDiff(),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), externalAuthCSID).
					Return(mustBuildCSExternalAuthFromRP(t, newTestExternalAuth()), nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), externalAuthCSID, gomock.Any()).
					Return(nil, errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to update cluster-service ExternalAuth",
		},
		{
			name:                                "when CS update returns unrelated bad request error error is propagated",
			existingExternalAuth:                newExternalAuthWithConfigDiff(),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), externalAuthCSID).
					Return(mustBuildCSExternalAuthFromRP(t, newTestExternalAuth()), nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), externalAuthCSID, gomock.Any()).
					Return(nil, newFakeOCMUnrelatedBadRequestError())
			},
			wantErr:        true,
			wantErrContain: "failed to update cluster-service ExternalAuth",
		},
		{
			name:                                "when CS GetExternalAuth fails error is propagated",
			existingExternalAuth:                newExternalAuthWithConfigDiff(),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), externalAuthCSID).
					Return(nil, errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to get external auth from Cluster Service",
		},
		{
			name:                                "external auth not found no-op is performed",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                                "minimum reconcile cooldown prevents sync",
			existingExternalAuth:                newTestExternalAuth(),
			minimumReconcileTimeCooldownChecker: &neverSyncCooldownChecker{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingExternalAuth != nil {
				resources = append(resources, tc.existingExternalAuth)
			}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			externalAuthsForLister := []*api.HCPOpenShiftClusterExternalAuth{}
			if tc.existingExternalAuth != nil {
				externalAuthsForLister = append(externalAuthsForLister, tc.existingExternalAuth)
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			syncer := &externalAuthClusterServiceUpdateDispatchSyncer{
				cooldownChecker:                     &alwaysSyncCooldownChecker{},
				minimumReconcileTimeCooldownChecker: tc.minimumReconcileTimeCooldownChecker,
				externalAuthLister:                  &listertesting.SliceExternalAuthLister{ExternalAuths: externalAuthsForLister},
				resourcesDBClient:                   mockResourcesDBClient,
				clusterServiceClient:                mockCSClient,
			}

			_, err = syncer.SyncOnce(ctx, testKey)
			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrContain != "" {
					assert.Contains(t, err.Error(), tc.wantErrContain)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNeedsWork(t *testing.T) {
	csID := api.Must(api.NewInternalID(testExternalAuthCSIDStr))
	now := metav1.Now()

	tests := []struct {
		name         string
		externalAuth *api.HCPOpenShiftClusterExternalAuth
		want         bool
	}{
		{
			name: "proceed when CSID set",
			externalAuth: &api.HCPOpenShiftClusterExternalAuth{
				ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
					ClusterServiceID: &csID,
				},
			},
			want: true,
		},
		{
			name: "skip when deletion timestamp is set",
			externalAuth: &api.HCPOpenShiftClusterExternalAuth{
				ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
					DeletionTimestamp: &now,
					ClusterServiceID:  &csID,
				},
			},
			want: false,
		},
		{
			name: "skip when no CSID",
			externalAuth: &api.HCPOpenShiftClusterExternalAuth{
				ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, needsWork(tt.externalAuth))
		})
	}
}

func mustBuildCSExternalAuthFromRP(t *testing.T, ea *api.HCPOpenShiftClusterExternalAuth) *arohcpv1alpha1.ExternalAuth {
	t.Helper()

	csBuilder, err := ocm.BuildCSExternalAuth(context.Background(), ea, true)
	require.NoError(t, err)

	csExternalAuth, err := csBuilder.Build()
	require.NoError(t, err)
	return csExternalAuth
}

func newTestExternalAuth(opts ...func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/" + testExternalAuthName,
	))
	externalAuthInternalID := api.Must(api.NewInternalID(testExternalAuthCSIDStr))

	ea := &api.HCPOpenShiftClusterExternalAuth{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		ProxyResource: arm.ProxyResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testExternalAuthName,
				Type: api.ExternalAuthResourceType.String(),
			},
		},
		Properties: api.HCPOpenShiftClusterExternalAuthProperties{
			Issuer: api.TokenIssuerProfile{
				URL:       "https://issuer.example.com",
				Audiences: []string{"aud1", "aud2"},
				CA:        "test-ca-cert",
			},
			Clients: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "console",
						AuthClientNamespace: "openshift-console",
					},
					ClientID:    "client-id-1",
					ExtraScopes: []string{"email", "profile"},
					Type:        api.ExternalAuthClientTypePublic,
				},
			},
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: api.UsernameClaimProfile{
						Claim:        "email",
						PrefixPolicy: api.UsernameClaimPrefixPolicyNoPrefix,
					},
					Groups: &api.GroupClaimProfile{
						Claim:  "groups",
						Prefix: "oidc:",
					},
				},
				ValidationRules: []api.TokenClaimValidationRule{
					{
						Type: api.TokenValidationRuleTypeRequiredClaim,
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "hd",
							RequiredValue: "example.com",
						},
					},
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID: &externalAuthInternalID,
		},
	}

	for _, opt := range opts {
		opt(ea)
	}

	return ea
}
