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

package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/google/uuid"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	// This will invoke the init() function in each
	// API version package so it can register itself.
	"github.com/Azure/ARO-HCP/internal/api"
	_ "github.com/Azure/ARO-HCP/internal/api/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/mocks"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

var dummyExternalAuthHREF = ocm.GenerateExternalAuthHREF(dummyClusterHREF, api.TestExternalAuthName)

var dummyURL = "https://redhat.com"
var dummyCA = `-----BEGIN CERTIFICATE-----
MIICMzCCAZygAwIBAgIJALiPnVsvq8dsMA0GCSqGSIb3DQEBBQUAMFMxCzAJBgNV
BAYTAlVTMQwwCgYDVQQIEwNmb28xDDAKBgNVBAcTA2ZvbzEMMAoGA1UEChMDZm9v
MQwwCgYDVQQLEwNmb28xDDAKBgNVBAMTA2ZvbzAeFw0xMzAzMTkxNTQwMTlaFw0x
ODAzMTgxNTQwMTlaMFMxCzAJBgNVBAYTAlVTMQwwCgYDVQQIEwNmb28xDDAKBgNV
BAcTA2ZvbzEMMAoGA1UEChMDZm9vMQwwCgYDVQQLEwNmb28xDDAKBgNVBAMTA2Zv
bzCBnzANBgkqhkiG9w0BAQEFAAOBjQAwgYkCgYEAzdGfxi9CNbMf1UUcvDQh7MYB
OveIHyc0E0KIbhjK5FkCBU4CiZrbfHagaW7ZEcN0tt3EvpbOMxxc/ZQU2WN/s/wP
xph0pSfsfFsTKM4RhTWD2v4fgk+xZiKd1p0+L4hTtpwnEw0uXRVd0ki6muwV5y/P
+5FHUeldq+pgTcgzuK8CAwEAAaMPMA0wCwYDVR0PBAQDAgLkMA0GCSqGSIb3DQEB
BQUAA4GBAJiDAAtY0mQQeuxWdzLRzXmjvdSuL9GoyT3BF/jSnpxz5/58dba8pWen
v3pj4P3w5DoOso0rzkZy2jEsEitlVM2mLSbQpMM+MUVQCQoiG6W9xuCFuxSrwPIS
pAqEAuV4DNoxQKKWmhVv+J0ptMWD25Pnpxeq5sXzghfJnslJlQND
-----END CERTIFICATE-----
`
var dummyAudiences = []string{"audience1", "audience2"}
var dummyClaim = "4.18.0"

func TestCreateExternalAuth(t *testing.T) {
	clusterResourceID, _ := azcorearm.ParseResourceID(api.TestClusterResourceID)
	clusterDoc := database.NewResourceDocument(clusterResourceID)
	clusterDoc.InternalID, _ = ocm.NewInternalID(dummyClusterHREF)

	externalAuthResourceID, _ := azcorearm.ParseResourceID(api.TestExternalAuthResourceID)
	externalAuthDoc := database.NewResourceDocument(externalAuthResourceID)
	externalAuthDoc.InternalID, _ = ocm.NewInternalID(dummyExternalAuthHREF)

	requestBody := generated.ExternalAuth{
		Properties: &generated.ExternalAuthProperties{
			Issuer: &generated.TokenIssuerProfile{
				URL:       &dummyURL,
				CA:        &dummyCA,
				Audiences: api.StringSliceToStringPtrSlice(dummyAudiences),
			},
			Claim: &generated.ExternalAuthClaimProfile{
				Mappings: &generated.TokenClaimMappingsProfile{
					Username: &generated.UsernameClaimProfile{
						Claim: &dummyClaim,
					},
				},
			},
		},
	}
	expectedCSExternalAuth, _ := arohcpv1alpha1.NewExternalAuth().
		ID(strings.ToLower(api.TestExternalAuthName)).
		Issuer(arohcpv1alpha1.NewTokenIssuer().
			URL(dummyURL).
			CA(dummyCA).
			Audiences(dummyAudiences...),
		).
		Claim(arohcpv1alpha1.NewExternalAuthClaim().
			Mappings(arohcpv1alpha1.NewTokenClaimMappings().
				UserName(arohcpv1alpha1.NewUsernameClaim().
					Claim(dummyClaim).
					Prefix("").
					PrefixPolicy(""),
				).
				Groups(nil),
			).
			ValidationRules(),
		).
		Clients().
		Build()
	tests := []struct {
		name                   string
		urlPath                string
		subscription           *arm.Subscription
		systemData             *arm.SystemData
		subDoc                 *arm.Subscription
		clusterDoc             *database.ResourceDocument
		externalAuthDoc        *database.ResourceDocument
		expectedCSExternalAuth *arohcpv1alpha1.ExternalAuth
		expectedStatusCode     int
	}{
		{
			name:    "PUT External Auth - Create a new External Auth",
			urlPath: api.TestExternalAuthResourceID + "?api-version=2024-06-10-preview",
			subDoc: &arm.Subscription{
				State:            arm.SubscriptionStateRegistered,
				RegistrationDate: api.Ptr(time.Now().String()),
				Properties:       nil,
			},
			clusterDoc:             clusterDoc,
			externalAuthDoc:        externalAuthDoc,
			expectedCSExternalAuth: expectedCSExternalAuth,
			expectedStatusCode:     http.StatusCreated,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockDBClient := mocks.NewMockDBClient(ctrl)
			mockDBTransaction := mocks.NewMockDBTransaction(ctrl)
			mockDBTransactionResult := mocks.NewMockDBTransactionResult(ctrl)
			mockCSClient := mocks.NewMockClusterServiceClientSpec(ctrl)
			pk := database.NewPartitionKey(api.TestSubscriptionID)
			reg := prometheus.NewRegistry()

			f := NewFrontend(
				api.NewTestLogger(),
				nil,
				nil,
				reg,
				mockDBClient,
				mockCSClient,
				newNoopAuditClient(t),
			)

			requestHeader := make(http.Header)
			requestHeader.Add(arm.HeaderNameHomeTenantID, api.TestTenantID)

			body, _ := json.Marshal(requestBody)

			subs := map[string]*arm.Subscription{api.TestSubscriptionID: test.subDoc}
			ts := newHTTPServer(f, ctrl, mockDBClient, subs)

			// CreateOrUpdateExternalAuth
			mockCSClient.EXPECT().
				PostExternalAuth(gomock.Any(), clusterDoc.InternalID, test.expectedCSExternalAuth).
				DoAndReturn(
					func(ctx context.Context, clusterInternalID ocm.InternalID, externalAuth *arohcpv1alpha1.ExternalAuth) (*arohcpv1alpha1.ExternalAuth, error) {
						builder := arohcpv1alpha1.NewExternalAuth().
							Copy(externalAuth).
							HREF(dummyExternalAuthHREF)
						return builder.Build()
					},
				)

			// MiddlewareLockSubscription
			mockDBClient.EXPECT().
				GetLockClient()
			// MiddlewareValidateSubscriptionState
			mockDBClient.EXPECT().
				GetSubscriptionDoc(gomock.Any(), api.TestSubscriptionID).
				Return(test.subDoc, nil).
				Times(1)
			// CreateOrUpdateExternalAuth
			mockDBClient.EXPECT().
				GetResourceDoc(gomock.Any(), equalResourceID(test.externalAuthDoc.ResourceID)).
				Return("", nil, &azcore.ResponseError{StatusCode: http.StatusNotFound})
			// CheckForProvisioningStateConflict and CreateOrUpdateExternalAuth
			mockDBClient.EXPECT().
				GetResourceDoc(gomock.Any(), equalResourceID(test.clusterDoc.ResourceID)).
				Return("itemID", test.clusterDoc, nil).
				Times(2)
			// CreateOrUpdateExternalAuth
			mockDBClient.EXPECT().
				NewTransaction(pk).
				Return(mockDBTransaction)
			// CreateOrUpdateExternalAuth
			operationID := uuid.New().String()
			mockDBTransaction.EXPECT().
				CreateOperationDoc(gomock.Any(), nil).
				Return(operationID)

			// ExposeOperation
			mockDBTransaction.EXPECT().
				PatchOperationDoc(operationID, gomock.Any(), nil)
			// ExposeOperation
			mockDBTransaction.EXPECT().
				OnSuccess(gomock.Any())

			// CreateOrUpdateExternalAuth
			externalAuthItemID := uuid.New().String()
			mockDBTransaction.EXPECT().
				CreateResourceDoc(test.externalAuthDoc, nil).
				Return(externalAuthItemID)
			// CreateOrUpdateExternalAuth
			mockDBTransaction.EXPECT().
				PatchResourceDoc(externalAuthItemID, gomock.Any(), nil)
			// CreateOrUpdateExternalAuth
			mockDBTransaction.EXPECT().
				Execute(gomock.Any(), &azcosmos.TransactionalBatchOptions{
					EnableContentResponseOnWrite: true}).
				Return(mockDBTransactionResult, nil)
			// CreateOrUpdateExternalAuth
			mockDBTransactionResult.EXPECT().
				GetResourceDoc(externalAuthItemID).
				Return(test.externalAuthDoc, nil)

			req, err := http.NewRequest(http.MethodPut, ts.URL+test.urlPath, bytes.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(arm.HeaderNameARMResourceSystemData, "{}")

			rs, err := ts.Client().Do(req)
			t.Log(rs)
			require.NoError(t, err)

			if !assert.Equal(t, test.expectedStatusCode, rs.StatusCode) {
				defer rs.Body.Close()
				body, err := io.ReadAll(rs.Body)
				require.NoError(t, err)

				t.Log(string(body))
			}

			lintMetrics(t, reg)
			assertHTTPMetrics(t, reg, test.subDoc)
		})
	}
}
