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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onsi/gomega/format"
	"github.com/stretchr/testify/require"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		Host:    "https://admin:unused@cluster.example.test/proxy?token=unused#unused",
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
		Host: "https://cluster.example.test/proxy", APIPath: admin.APIPath, BearerToken: "external",
		TLSClientConfig: rest.TLSClientConfig{
			ServerName: admin.ServerName, CAData: admin.CAData, CAFile: admin.CAFile,
			NextProtos: admin.NextProtos,
		},
	}, cfg)
}

func TestEntraTokenFailureOmitsCredentials(t *testing.T) {
	const marker = "synthetic-sensitive-detail"
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New(marker))
	auth := EntraExternalAuthContext{
		Credential:   stubTokenCredential{err: errors.New(marker)},
		ClientSecret: marker,
	}
	config, err := auth.GetRESTConfig(ctx, &rest.Config{Host: "https://cluster.example.test"})
	require.Nil(t, config)
	require.Error(t, err)
	require.NotContains(t, format.Object(err, 1), marker)
	require.NotContains(t, format.Object(auth, 1), marker)
	require.NotContains(t, fmt.Sprintf("%+v %#v", auth, auth), marker)
}

func TestExternalAuthRoleBindings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		role   string
		create func(context.Context, *rest.Config) error
	}{
		{"entra-admins", "cluster-admin", func(ctx context.Context, config *rest.Config) error {
			return CreateClusterRoleBinding(ctx, "test-user", config)
		}},
		{"custom-binding", "view", func(ctx context.Context, config *rest.Config) error {
			auth := EntraExternalAuthContext{Subject: "test-user"}
			return auth.GrantClusterRole(ctx, "custom-binding", "view", config)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const marker = "synthetic-response-detail"
			var binding rbacv1.ClusterRoleBinding
			config := &rest.Config{
				Host: "https://cluster.example.test",
				Transport: manifestRoundTripper(func(req *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodPost, req.Method)
					require.Equal(t, "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings", req.URL.Path)
					require.NoError(t, json.NewDecoder(req.Body).Decode(&binding))
					return &http.Response{
						StatusCode: http.StatusConflict,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"kind":"Status","apiVersion":"v1","status":"Failure","code":409,"reason":"AlreadyExists","message":"synthetic-response-detail"}`)),
					}, nil
				}),
			}
			config.ContentType = "application/json"
			err := tc.create(t.Context(), config)
			require.True(t, apierrors.IsAlreadyExists(err))
			require.NotContains(t, format.Object(err, 1), marker)
			require.Equal(t, tc.name, binding.Name)
			require.Equal(t, rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: tc.role}, binding.RoleRef)
			require.Equal(t, []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: "test-user"}}, binding.Subjects)
		})
	}
}
