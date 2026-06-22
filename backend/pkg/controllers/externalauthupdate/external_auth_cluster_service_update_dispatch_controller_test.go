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
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testExternalAuthName    = "test-externalauth"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testExternalAuthCSIDStr = testClusterServiceIDStr + "/external_auth_config/external_auths/" + testExternalAuthName
)

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

func TestExternalAuthShouldProceed(t *testing.T) {
	csID := api.Must(api.NewInternalID(testExternalAuthCSIDStr))
	now := metav1.Now()

	tests := []struct {
		name string
		ea   *api.HCPOpenShiftClusterExternalAuth
		want bool
	}{
		{
			name: "proceed when CSID set",
			ea: &api.HCPOpenShiftClusterExternalAuth{
				ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
					ClusterServiceID: &csID,
				},
			},
			want: true,
		},
		{
			name: "skip when deletion timestamp is set",
			ea: &api.HCPOpenShiftClusterExternalAuth{
				ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
					DeletionTimestamp: &now,
					ClusterServiceID:  &csID,
				},
			},
			want: false,
		},
		{
			name: "skip when no CSID",
			ea: &api.HCPOpenShiftClusterExternalAuth{
				ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, externalAuthShouldProceed(tt.ea))
		})
	}
}

func newFakeOCMParentClusterNotUpdatableError() error {
	e, _ := ocmerrors.NewError().
		Status(http.StatusBadRequest).
		Reason("ExternalAuths can only be updated on clusters in an updatable state, cluster requested is in 'updating' state.").
		Build()
	return e
}

func newFakeOCMNoReadyNodePoolError() error {
	e, _ := ocmerrors.NewError().
		Status(http.StatusBadRequest).
		Reason("console clients cannot be configured without a ready node pool. Please ensure at least one node pool is in 'ready' state before configuring console authentication").
		Build()
	return e
}

func newFakeOCMUnrelatedBadRequestError() error {
	e, _ := ocmerrors.NewError().
		Status(http.StatusBadRequest).
		Reason("some other validation error").
		Build()
	return e
}

func TestExternalAuthUpdateDispatchSyncer_SyncOnce(t *testing.T) {
	csID := api.Must(api.NewInternalID(testExternalAuthCSIDStr))

	defaultExistingCSExternalAuth := api.Must(arohcpv1alpha1.NewExternalAuth().
		Issuer(arohcpv1alpha1.NewTokenIssuer().
			URL("https://original.example.com").
			Audiences("audience1")).
		Build())

	newExternalAuthWithConfigDiff := func() *api.HCPOpenShiftClusterExternalAuth {
		return newTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
			ea.Properties.Issuer.URL = "https://changed.example.com"
		})
	}

	testCases := []struct {
		name              string
		externalAuth      *api.HCPOpenShiftClusterExternalAuth
		existingCSEA      *arohcpv1alpha1.ExternalAuth
		setupMockCSClient func(mock *ocm.MockClusterServiceClientSpec)
		wantErr           bool
		wantErrContain    string
	}{
		{
			name: "skip without CS call when no CSID",
			externalAuth: newTestExternalAuth(func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.ClusterServiceID = nil
			}),
		},
		{
			name:         "dispatches CS call when config differs",
			externalAuth: newExternalAuthWithConfigDiff(),
			existingCSEA: defaultExistingCSExternalAuth,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), csID).
					Return(defaultExistingCSExternalAuth, nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), csID, gomock.Any()).
					Return(nil, nil)
			},
		},
		{
			name:         "no-op when config matches",
			externalAuth: newTestExternalAuth(nil),
			existingCSEA: mustBuildCSExternalAuthFromRP(t, newTestExternalAuth(nil)),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), csID).
					Return(mustBuildCSExternalAuthFromRP(t, newTestExternalAuth(nil)), nil)
			},
		},
		{
			name:         "when CS external auth update returns parent cluster not updatable no error is returned",
			externalAuth: newExternalAuthWithConfigDiff(),
			existingCSEA: defaultExistingCSExternalAuth,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), csID).
					Return(defaultExistingCSExternalAuth, nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMParentClusterNotUpdatableError())
			},
		},
		{
			name:         "when CS external auth update returns no ready node pool no error is returned",
			externalAuth: newExternalAuthWithConfigDiff(),
			existingCSEA: defaultExistingCSExternalAuth,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), csID).
					Return(defaultExistingCSExternalAuth, nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMNoReadyNodePoolError())
			},
		},
		{
			name:         "when CS external auth update returns unhandled error error is propagated",
			externalAuth: newExternalAuthWithConfigDiff(),
			existingCSEA: defaultExistingCSExternalAuth,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), csID).
					Return(defaultExistingCSExternalAuth, nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), csID, gomock.Any()).
					Return(nil, errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to update cluster-service ExternalAuth",
		},
		{
			name:         "when CS external auth update returns unrelated bad request error error is propagated",
			externalAuth: newExternalAuthWithConfigDiff(),
			existingCSEA: defaultExistingCSExternalAuth,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetExternalAuth(gomock.Any(), csID).
					Return(defaultExistingCSExternalAuth, nil)
				mock.EXPECT().
					UpdateExternalAuth(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMUnrelatedBadRequestError())
			},
			wantErr:        true,
			wantErrContain: "failed to update cluster-service ExternalAuth",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockResourcesDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.externalAuth})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			syncer := &externalAuthClusterServiceUpdateDispatchSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				externalAuthLister:   &listertesting.SliceExternalAuthLister{ExternalAuths: []*api.HCPOpenShiftClusterExternalAuth{tc.externalAuth}},
				resourcesDBClient:    mockResourcesDB,
				clusterServiceClient: mockCSClient,
			}

			key := controllerutils.HCPExternalAuthKey{
				SubscriptionID:      testSubscriptionID,
				ResourceGroupName:   testResourceGroupName,
				HCPClusterName:      testClusterName,
				HCPExternalAuthName: testExternalAuthName,
			}

			err = syncer.SyncOnce(ctx, key)
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

func mustBuildCSExternalAuthFromRP(t *testing.T, ea *api.HCPOpenShiftClusterExternalAuth) *arohcpv1alpha1.ExternalAuth {
	t.Helper()

	builder, err := ocm.BuildCSExternalAuth(context.Background(), ea, true)
	require.NoError(t, err)

	csExternalAuth, err := builder.Build()
	require.NoError(t, err)
	return csExternalAuth
}

func newTestExternalAuth(opts func(*api.HCPOpenShiftClusterExternalAuth)) *api.HCPOpenShiftClusterExternalAuth {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/externalAuths/" + testExternalAuthName,
	))

	csID := api.Must(api.NewInternalID(testExternalAuthCSIDStr))
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
				URL:       "https://example.com",
				Audiences: []string{"audience1"},
			},
			Clients: []api.ExternalAuthClientProfile{
				{
					Component: api.ExternalAuthClientComponentProfile{
						Name:                "my-component",
						AuthClientNamespace: "my-namespace",
					},
					ClientID:    "client-id-1",
					ExtraScopes: []string{"email", "profile"},
					Type:        api.ExternalAuthClientTypeConfidential,
				},
			},
			Claim: api.ExternalAuthClaimProfile{
				Mappings: api.TokenClaimMappingsProfile{
					Username: api.UsernameClaimProfile{
						Claim:        "sub",
						PrefixPolicy: api.UsernameClaimPrefixPolicyNoPrefix,
					},
					Groups: &api.GroupClaimProfile{
						Claim:  "groups",
						Prefix: "oidc:",
					},
				},
				ValidationRules: []api.TokenClaimValidationRule{
					{
						RequiredClaim: api.TokenRequiredClaim{
							Claim:         "iss",
							RequiredValue: "https://example.com",
						},
					},
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterExternalAuthServiceProviderProperties{
			ClusterServiceID: &csID,
		},
	}

	if opts != nil {
		opts(ea)
	}

	return ea
}
