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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type helperCredentialRetrieverFunc func() (azcore.TokenCredential, error)

func (retrieve helperCredentialRetrieverFunc) RetrieveCredential() (azcore.TokenCredential, error) {
	return retrieve()
}

type helperCredentialFunc func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)

func (credential helperCredentialFunc) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return credential(ctx, options)
}

type helperTransportFunc func(*http.Request) (*http.Response, error)

func (transport helperTransportFunc) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestARMPermissionsManagerClientBuilder(t *testing.T) {
	credentialReads := 0
	tokenRequests := 0
	retriever := helperCredentialRetrieverFunc(func() (azcore.TokenCredential, error) {
		credentialReads++
		return helperCredentialFunc(func(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
			tokenRequests++
			require.Equal(t, []string{"https://arm.example.test/.default"}, options.Scopes)
			return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
		}), nil
	})
	options := &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{ActiveDirectoryAuthorityHost: cloud.AzurePublic.ActiveDirectoryAuthorityHost,
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{cloud.ResourceManager: {Endpoint: "https://arm.example.test", Audience: "https://arm.example.test"}}},
		Transport: helperTransportFunc(func(request *http.Request) (*http.Response, error) {
			require.Equal(t, "arm.example.test", request.URL.Host)
			return &http.Response{StatusCode: http.StatusOK, Request: request, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"properties":{}}`))}, nil
		}),
	}}
	builder := NewInsecureARMPermissionsManagerClientBuilder(retriever, options)
	for range 2 {
		client, err := builder.RoleAssignmentsClient("00000000-0000-0000-0000-000000000000")
		require.NoError(t, err)
		_, err = client.GetByID(context.Background(), "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/mrg/providers/Microsoft.Authorization/roleAssignments/00000000-0000-0000-0000-000000000001", nil)
		require.NoError(t, err)
	}
	require.Equal(t, 2, credentialReads, "each build must read the rotating credential retriever")
	require.Equal(t, 2, tokenRequests, "the helper credential must authenticate requests")
}

func TestARMPermissionsManagerClientBuilderCredentialError(t *testing.T) {
	wantError := errors.New("certificate unavailable")
	builder := NewInsecureARMPermissionsManagerClientBuilder(helperCredentialRetrieverFunc(func() (azcore.TokenCredential, error) {
		return nil, wantError
	}), nil)
	client, err := builder.RoleAssignmentsClient("subscription")
	require.ErrorIs(t, err, wantError)
	require.Nil(t, client)
}
