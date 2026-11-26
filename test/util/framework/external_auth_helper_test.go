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

package framework

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestBuildEntraExternalAuthDefaults(t *testing.T) {
	params := NewDefaultExternalAuthParams()
	require.Equal(t, ExternalAuthParams{
		PrefixPolicy:   hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix,
		SubjectPrefix:  "prefix-",
		UsernameClaim:  "sub",
		GroupsClaim:    "groups",
		IncludeClients: true,
	}, params)

	props := buildEntraExternalAuth("tenant", "app", params).Properties
	require.Equal(t, &hcpsdk20240610preview.TokenIssuerProfile{
		URL:       to.Ptr("https://login.microsoftonline.com/tenant/v2.0"),
		Audiences: []*string{to.Ptr("app")},
	}, props.Issuer)
	require.Equal(t, &hcpsdk20240610preview.UsernameClaimProfile{
		Claim:        to.Ptr("sub"),
		PrefixPolicy: to.Ptr(hcpsdk20240610preview.UsernameClaimPrefixPolicyPrefix),
		Prefix:       to.Ptr("prefix-"),
	}, props.Claim.Mappings.Username)
	require.Equal(t, to.Ptr("groups"), props.Claim.Mappings.Groups.Claim)
	require.Len(t, props.Clients, 2)
	for i, want := range []struct {
		name       string
		clientType hcpsdk20240610preview.ExternalAuthClientType
	}{
		{"console", hcpsdk20240610preview.ExternalAuthClientTypeConfidential},
		{"cli", hcpsdk20240610preview.ExternalAuthClientTypePublic},
	} {
		client := props.Clients[i]
		require.Equal(t, to.Ptr("app"), client.ClientID)
		require.Equal(t, to.Ptr(want.name), client.Component.Name)
		require.Equal(t, to.Ptr("openshift-console"), client.Component.AuthClientNamespace)
		require.Equal(t, to.Ptr(want.clientType), client.Type)
	}
}

func TestBuildEntraExternalAuthNoPrefix(t *testing.T) {
	params := NewDefaultExternalAuthParams()
	params.PrefixPolicy = hcpsdk20240610preview.UsernameClaimPrefixPolicyNoPrefix
	username := buildEntraExternalAuth("tenant", "app", params).Properties.Claim.Mappings.Username
	require.Equal(t, to.Ptr(hcpsdk20240610preview.UsernameClaimPrefixPolicyNoPrefix), username.PrefixPolicy)
	require.Nil(t, username.Prefix)
}

func TestBuildEntraExternalAuthCustomClaimsWithoutClients(t *testing.T) {
	params := NewDefaultExternalAuthParams()
	params.UsernameClaim = "oid"
	params.SubjectPrefix = "entra:"
	params.GroupsClaim = "roles"
	params.IncludeClients = false
	props := buildEntraExternalAuth("tenant", "app", params).Properties
	require.Equal(t, to.Ptr("oid"), props.Claim.Mappings.Username.Claim)
	require.Equal(t, to.Ptr("entra:"), props.Claim.Mappings.Username.Prefix)
	require.Equal(t, to.Ptr("roles"), props.Claim.Mappings.Groups.Claim)
	require.Nil(t, props.Clients)
}

func TestEntraRESTConfigUsesOnlyExternalCredentials(t *testing.T) {
	admin := &rest.Config{
		Host:    "https://cluster.example.test",
		APIPath: "/api",
		TLSClientConfig: rest.TLSClientConfig{
			ServerName: "cluster.example.test",
			CAData:     []byte("test-ca"), CAFile: "ca-file",
			NextProtos: []string{"h2"},
			KeyData:    []byte("unused"), CertData: []byte("unused"),
			KeyFile: "key-file", CertFile: "cert-file",
		},
		Username: "admin", Password: "unused",
		BearerToken: "unused", BearerTokenFile: "token-file",
		AuthProvider: &clientcmdapi.AuthProviderConfig{Name: "unused"},
		ExecProvider: &clientcmdapi.ExecConfig{Command: "unused"},
		Impersonate:  rest.ImpersonationConfig{UserName: "admin"},
	}
	auth := EntraExternalAuthContext{Scope: "app/.default", Credential: stubTokenCredential{token: "external"}}
	cfg, err := auth.GetRESTConfig(t.Context(), admin)
	require.NoError(t, err)
	require.Equal(t, &rest.Config{
		Host: admin.Host, APIPath: admin.APIPath, BearerToken: "external",
		TLSClientConfig: rest.TLSClientConfig{
			ServerName: admin.ServerName, CAData: admin.CAData, CAFile: admin.CAFile,
			NextProtos: admin.NextProtos,
		},
	}, cfg)
}
