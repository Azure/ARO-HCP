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

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/msi-dataplane/pkg/dataplane"
)

// fakeManagedIdentitiesDataplaneClient and fakeFPAMIDataplaneClientBuilder are defined in
// smi_client_builder_test.go and shared by this file.

func TestMIDataplaneBasedIdentityAccessTokenRetriever_GetToken(t *testing.T) {
	const identityResourceIDStr = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/operator"
	identityResourceID, err := azcorearm.ParseResourceID(identityResourceIDStr)
	require.NoError(t, err, "failed to parse test identity resource ID")

	missingFieldsCredential := dataplane.UserAssignedIdentityCredentials{
		ResourceID: ptr.To(identityResourceIDStr),
		// ClientID, ClientSecret, TenantID, AuthenticationEndpoint are all nil, as the
		// Managed Identities Data Plane returns them when the identity no longer exists.
	}
	completeCredential := dataplane.UserAssignedIdentityCredentials{
		ResourceID:             ptr.To(identityResourceIDStr),
		ClientID:               ptr.To("client-id"),
		ClientSecret:           ptr.To("client-secret"),
		TenantID:               ptr.To("tenant-id"),
		AuthenticationEndpoint: ptr.To("https://login.microsoftonline.com/tenant-id"),
	}

	tests := []struct {
		name         string
		client       ManagedIdentitiesDataplaneClient
		wantNotFound bool
	}{
		{
			name: "returns ErrMIDataplaneBasedIdentityNotFound when the dataplane response has nil required fields",
			client: &fakeManagedIdentitiesDataplaneClient{
				creds: &dataplane.ManagedIdentityCredentials{ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{missingFieldsCredential}},
			},
			wantNotFound: true,
		},
		{
			// completeCredential's ClientSecret isn't a real certificate, so dataplane.GetCredential
			// still fails trying to decode/parse it. This asserts that the failure comes from that
			// deeper step, not from our own required-fields check short-circuiting the call as
			// ErrMIDataplaneBasedIdentityNotFound.
			name: "does not short-circuit as identity not found when the dataplane response has all required fields set",
			client: &fakeManagedIdentitiesDataplaneClient{
				creds: &dataplane.ManagedIdentityCredentials{ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{completeCredential}},
			},
			wantNotFound: false,
		},
		{
			name:         "does not classify a generic dataplane transport error as identity not found",
			client:       &fakeManagedIdentitiesDataplaneClient{err: errors.New("simulated transport failure")},
			wantNotFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retriever := &miDataplaneBasedIdentityAccessTokenRetriever{
				miDataplaneClient:  tt.client,
				clientOptions:      &azcore.ClientOptions{},
				identityResourceID: identityResourceID,
			}

			token, err := retriever.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{"https://example.com/.default"}})

			require.Error(t, err)
			assert.Equal(t, tt.wantNotFound, errors.Is(err, ErrMIDataplaneBasedIdentityNotFound),
				"errors.Is(err, ErrMIDataplaneBasedIdentityNotFound) mismatch, got err: %v", err)
			assert.Equal(t, azcore.AccessToken{}, token)
		})
	}
}

func TestMIDataplaneBasedIdentityAccessTokenRetrieverBuilder_Build(t *testing.T) {
	const identityResourceIDStr = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/operator"
	identityResourceID, err := azcorearm.ParseResourceID(identityResourceIDStr)
	require.NoError(t, err, "failed to parse test identity resource ID")

	missingFieldsCredential := dataplane.UserAssignedIdentityCredentials{
		ResourceID: ptr.To(identityResourceIDStr),
		// ClientID, ClientSecret, TenantID, AuthenticationEndpoint are all nil, as the
		// Managed Identities Data Plane returns them when the identity no longer exists.
	}
	buildErr := errors.New("simulated FPA client builder failure")

	tests := []struct {
		name          string
		fpaBuilder    *fakeFPAMIDataplaneClientBuilder
		validateBuild func(t *testing.T, retriever MIDataplaneBasedIdentityAccessTokenRetriever, err error)
	}{
		{
			name:       "propagates a wrapped error when the FPA MI dataplane client builder fails",
			fpaBuilder: &fakeFPAMIDataplaneClientBuilder{buildErr: buildErr},
			validateBuild: func(t *testing.T, retriever MIDataplaneBasedIdentityAccessTokenRetriever, err error) {
				require.Error(t, err)
				assert.True(t, errors.Is(err, buildErr), "expected the underlying FPA client builder error to survive Build's wrapping, got: %v", err)
				assert.Nil(t, retriever)
			},
		},
		{
			name: "wires the retriever so a not-found identity is still detected after Build",
			fpaBuilder: &fakeFPAMIDataplaneClientBuilder{
				client: &fakeManagedIdentitiesDataplaneClient{
					creds: &dataplane.ManagedIdentityCredentials{ExplicitIdentities: []dataplane.UserAssignedIdentityCredentials{missingFieldsCredential}},
				},
			},
			validateBuild: func(t *testing.T, retriever MIDataplaneBasedIdentityAccessTokenRetriever, err error) {
				require.NoError(t, err, "failed to build retriever")

				_, tokenErr := retriever.GetToken(context.Background(), policy.TokenRequestOptions{Scopes: []string{"https://example.com/.default"}})

				require.Error(t, tokenErr)
				assert.True(t, errors.Is(tokenErr, ErrMIDataplaneBasedIdentityNotFound), "expected error to satisfy errors.Is(err, ErrMIDataplaneBasedIdentityNotFound) after going through Build, got: %v", tokenErr)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := &miDataplaneBasedIdentityAccessTokenRetrieverBuilder{
				fpaMIdataplaneClientBuilder: tt.fpaBuilder,
				clientOptions:               &azcore.ClientOptions{},
			}

			retriever, err := builder.Build("https://example.com", identityResourceID)

			tt.validateBuild(t, retriever, err)
		})
	}
}
