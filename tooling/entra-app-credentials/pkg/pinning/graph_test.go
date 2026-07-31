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

package pinning

import (
	"context"
	"crypto/sha1" //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestGraphClientFindApplication(t *testing.T) {
	thumbprint := sha1.Sum([]byte("certificate")) //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer token", request.Header.Get("Authorization"))
		assert.Equal(t, "displayName eq 'app''name'", request.URL.Query().Get("$filter"))
		_ = json.NewEncoder(writer).Encode(graphApplicationCollection{
			Value: []graphApplication{{
				ID:          "object-id",
				AppID:       "client-id",
				DisplayName: "app'name",
				KeyCredentials: []graphKeyCredential{{
					CustomKeyIdentifier: hex.EncodeToString(thumbprint[:]),
					Type:                keyCredentialType,
					Usage:               keyCredentialUsage,
				}},
			}},
		})
	}))
	defer server.Close()

	client := newTestGraphClient(server)
	application, err := client.FindApplication(context.Background(), "app'name")
	require.NoError(t, err)
	assert.Equal(t, "object-id", application.ID)
	assert.Equal(t, "client-id", application.AppID)
	require.Len(t, application.KeyCredentials, 1)
	assert.Equal(t, thumbprint[:], application.KeyCredentials[0].CustomKeyIdentifier)
}

func TestDecodeCustomKeyIdentifier(t *testing.T) {
	expected := []byte{0x01, 0x02, 0x03}

	decoded, err := decodeCustomKeyIdentifier("010203")
	require.NoError(t, err)
	assert.Equal(t, expected, decoded)

	decoded, err = decodeCustomKeyIdentifier("AQID")
	require.NoError(t, err)
	assert.Equal(t, expected, decoded)

	_, err = decodeCustomKeyIdentifier("not-an-identifier")
	require.Error(t, err)
}

func TestGraphClientRejectsDuplicateDisplayNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(graphApplicationCollection{
			Value: []graphApplication{{ID: "one"}, {ID: "two"}},
		})
	}))
	defer server.Close()

	client := newTestGraphClient(server)
	_, err := client.FindApplication(context.Background(), "duplicate")
	require.ErrorContains(t, err, "matched 2 applications")
}

func TestGraphClientRetriesApplicationPropagation(t *testing.T) {
	oldInterval := retryInterval
	retryInterval = time.Millisecond
	t.Cleanup(func() { retryInterval = oldInterval })

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		response := graphApplicationCollection{}
		if requests > 1 {
			response.Value = []graphApplication{{ID: "object-id"}}
		}
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()

	client := newTestGraphClient(server)
	_, err := client.FindApplication(context.Background(), "app")
	require.NoError(t, err)
	assert.Equal(t, 2, requests)
}

func TestGraphClientReplaceKeyCredentials(t *testing.T) {
	var body graphPatchRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPatch, request.Method)
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newTestGraphClient(server)
	err := client.ReplaceKeyCredentials(context.Background(), "object-id", "cert", []byte("certificate"))
	require.NoError(t, err)
	require.Len(t, body.KeyCredentials, 1)
	assert.Equal(t, "cert", *body.KeyCredentials[0].DisplayName)
	assert.Equal(t, []byte("certificate"), body.KeyCredentials[0].Key)
	assert.Equal(t, keyCredentialType, body.KeyCredentials[0].Type)
	assert.Equal(t, keyCredentialUsage, body.KeyCredentials[0].Usage)
}

func newTestGraphClient(server *httptest.Server) *graphClient {
	return &graphClient{
		credential: fakeCredential{},
		httpClient: server.Client(),
		baseURL:    server.URL,
	}
}
