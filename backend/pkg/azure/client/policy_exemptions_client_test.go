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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
)

type policyExemptionsTestCredential func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)

func (credential policyExemptionsTestCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return credential(ctx, options)
}

func policyExemptionsCredential(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type policyExemptionsTestPolicy struct{}

func (policyExemptionsTestPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set("X-Test-Policy", "applied")
	return req.Next()
}

func policyExemptionsTestOptions(server *httptest.Server) *azcorearm.ClientOptions {
	return &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: server.URL, Audience: "https://policy-exemptions.example"},
		}},
		Transport: server.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}
}

func TestPolicyExemptionsClientPaging(t *testing.T) {
	const subscriptionID = "subscription/with?reserved"
	const resourceGroup = "group /?#%"
	const exemptionID = "/subscriptions/sub/resourceGroups/group/providers/Microsoft.Authorization/policyExemptions/exemption"
	requests := 0
	var endpoint string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		assert.Equal(t, http.MethodGet, req.Method)
		assert.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
		assert.Equal(t, "applied", req.Header.Get("X-Test-Policy"))
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			assert.Equal(t, "/subscriptions/"+url.PathEscape(subscriptionID)+"/resourceGroups/"+url.PathEscape(resourceGroup)+"/providers/Microsoft.Authorization/policyExemptions", req.URL.EscapedPath())
			assert.Equal(t, url.Values{"api-version": {"2022-07-01-preview"}, "$filter": {"atScope()"}}, req.URL.Query())
			assert.Equal(t, "application/json", req.Header.Get("Accept"))
			_, _ = fmt.Fprintf(w, `{"value":[{"id":%q,"properties":{"policyAssignmentId":"assignment-id","expiresOn":"2027-01-02T03:04:05Z","policyDefinitionReferenceIds":["reference-id"],"resourceSelectors":[{"name":"selector","selectors":[{"kind":"resourceLocation","in":["eastus"]}]}],"assignmentScopeValidation":"DoNotValidate"}}],"nextLink":%q}`, exemptionID, endpoint+"/next?$skiptoken=opaque%2Btoken&api-version=2022-07-01-preview")
		case 2:
			assert.Equal(t, "/next", req.URL.Path)
			assert.Equal(t, "opaque+token", req.URL.Query().Get("$skiptoken"))
			assert.Empty(t, req.URL.Query().Get("$filter"), "nextLink must be followed without rebuilding its query")
			_, _ = io.WriteString(w, `{"value":[],"nextLink":""}`)
		default:
			t.Error("unexpected extra page request")
		}
	}))
	t.Cleanup(server.Close)
	endpoint = server.URL
	credentialCalls := 0
	credential := policyExemptionsTestCredential(func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
		credentialCalls++
		assert.Equal(t, []string{"https://policy-exemptions.example/.default"}, options.Scopes)
		return policyExemptionsCredential(ctx, options)
	})
	options := policyExemptionsTestOptions(server)
	options.PerCallPolicies = []policy.Policy{policyExemptionsTestPolicy{}}
	client, err := NewPolicyExemptionsClient(subscriptionID, credential, options)
	require.NoError(t, err)
	pager := client.NewListForResourceGroupPager(resourceGroup)
	assert.Zero(t, requests, "pager creation must not send requests")
	require.True(t, pager.More())
	page, err := pager.NextPage(context.Background())
	require.NoError(t, err)
	require.Len(t, page.Value, 1)
	exemption := page.Value[0]
	assert.Equal(t, exemptionID, *exemption.ID)
	assert.Equal(t, "/subscriptions/sub/resourceGroups/group", exemption.Scope())
	require.NotNil(t, exemption.Properties)
	assert.Equal(t, "assignment-id", *exemption.Properties.PolicyAssignmentID)
	require.NotNil(t, exemption.Properties.ExpiresOn)
	assert.True(t, exemption.Properties.ExpiresOn.Equal(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)))
	assert.Equal(t, []*string{to.Ptr("reference-id")}, exemption.Properties.PolicyDefinitionReferenceIDs)
	assert.JSONEq(t, `[{"name":"selector","selectors":[{"kind":"resourceLocation","in":["eastus"]}]}]`, string(exemption.Properties.ResourceSelectors))
	assert.Equal(t, "DoNotValidate", *exemption.Properties.AssignmentScopeValidation)
	require.True(t, pager.More())
	page, err = pager.NextPage(context.Background())
	require.NoError(t, err)
	assert.Empty(t, page.Value)
	assert.False(t, pager.More())
	assert.Equal(t, 2, requests)
	assert.Positive(t, credentialCalls)
}

func TestPolicyExemptionsClientResponses(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body        string
		errorCode   string
		invalidJSON bool
	}{
		{name: "empty list", status: http.StatusOK, body: `{"value":[]}`},
		{name: "missing optional fields", status: http.StatusOK, body: `{"value":[{"id":"id"}]}`},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":{"code":"AuthorizationFailed","message":"access denied"}}`, errorCode: "AuthorizationFailed"},
		{name: "not found", status: http.StatusNotFound, body: `{"error":{"code":"ResourceGroupNotFound","message":"not found"}}`, errorCode: "ResourceGroupNotFound"},
		{name: "unexpected success status", status: http.StatusCreated, body: `{}`, errorCode: "Created"},
		{name: "invalid JSON", status: http.StatusOK, body: `{`, invalidJSON: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)
			client, err := NewPolicyExemptionsClient("subscription", policyExemptionsTestCredential(policyExemptionsCredential), policyExemptionsTestOptions(server))
			require.NoError(t, err)
			pager := client.NewListForResourceGroupPager("group")
			page, err := pager.NextPage(context.Background())
			switch {
			case test.errorCode != "":
				var responseError *azcore.ResponseError
				require.ErrorAs(t, err, &responseError)
				assert.Equal(t, test.status, responseError.StatusCode)
				if test.status != http.StatusCreated {
					assert.Equal(t, test.errorCode, responseError.ErrorCode)
				}
			case test.invalidJSON:
				require.Error(t, err)
			default:
				require.NoError(t, err)
				assert.Nil(t, page.NextLink)
			}
			assert.False(t, pager.More())
		})
	}
}

type policyExemptionsTestTransport func(*http.Request) (*http.Response, error)

func (transport policyExemptionsTestTransport) Do(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestPolicyExemptionsClientValidationAndDefaults(t *testing.T) {
	credential := policyExemptionsTestCredential(policyExemptionsCredential)
	_, err := NewPolicyExemptionsClient("", credential, nil)
	require.ErrorContains(t, err, "subscriptionID cannot be empty")
	_, err = NewPolicyExemptionsClient("subscription", nil, nil)
	require.ErrorContains(t, err, "credential cannot be nil")
	client, err := NewPolicyExemptionsClient("subscription", credential, nil)
	require.NoError(t, err, "nil options must be supported")
	_, err = client.NewListForResourceGroupPager("").NextPage(context.Background())
	require.ErrorContains(t, err, "resourceGroupName cannot be empty")

	requests := 0
	options := &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Transport: policyExemptionsTestTransport(func(req *http.Request) (*http.Response, error) {
			requests++
			assert.Equal(t, "https", req.URL.Scheme)
			assert.Equal(t, "management.azure.com", req.URL.Host)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"value":[]}`)), Request: req}, nil
		}),
	}}
	client, err = NewPolicyExemptionsClient("subscription", credential, options)
	require.NoError(t, err)
	_, err = client.NewListForResourceGroupPager("group").NextPage(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, requests, "custom SDK transport must be used")
}

func TestPolicyExemptionsClientPipelineErrors(t *testing.T) {
	wantErr := errors.New("test credential failure")
	credential := policyExemptionsTestCredential(func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
		return azcore.AccessToken{}, wantErr
	})
	client, err := NewPolicyExemptionsClient("subscription", credential, nil)
	require.NoError(t, err)
	_, err = client.NewListForResourceGroupPager("group").NextPage(context.Background())
	require.ErrorIs(t, err, wantErr)

	t.Run("insecure nextLink uses SDK protections", func(t *testing.T) {
		requests := 0
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"value":[],"nextLink":"http://insecure.example/next"}`)
		}))
		t.Cleanup(server.Close)
		client, err := NewPolicyExemptionsClient("subscription", policyExemptionsTestCredential(policyExemptionsCredential), policyExemptionsTestOptions(server))
		require.NoError(t, err)
		pager := client.NewListForResourceGroupPager("group")
		_, err = pager.NextPage(context.Background())
		require.NoError(t, err)
		_, err = pager.NextPage(context.Background())
		require.ErrorContains(t, err, "https")
		assert.Equal(t, 1, requests, "insecure nextLink must not be sent")
	})
}

func TestPolicyExemptionScope(t *testing.T) {
	for _, test := range []struct {
		name string
		id   *string
		want string
	}{
		{name: "missing ID"},
		{name: "empty ID", id: to.Ptr("")},
		{name: "invalid type", id: to.Ptr("/subscriptions/sub/providers/Microsoft.Authorization/policyAssignments/name")},
		{name: "missing name", id: to.Ptr("/subscriptions/sub/providers/Microsoft.Authorization/policyExemptions/")},
		{name: "child resource", id: to.Ptr("/subscriptions/sub/providers/Microsoft.Authorization/policyExemptions/name/child")},
		{name: "subscription", id: to.Ptr("/subscriptions/sub/providers/Microsoft.Authorization/policyExemptions/name"), want: "/subscriptions/sub"},
		{name: "case insensitive", id: to.Ptr("/subscriptions/Sub/providers/MICROSOFT.AUTHORIZATION/POLICYEXEMPTIONS/name"), want: "/subscriptions/Sub"},
		{name: "management group", id: to.Ptr("/providers/Microsoft.Management/managementGroups/group/providers/Microsoft.Authorization/policyExemptions/name"), want: "/providers/Microsoft.Management/managementGroups/group"},
		{name: "tenant", id: to.Ptr("/providers/Microsoft.Authorization/policyExemptions/name"), want: "/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			exemption := &PolicyExemption{ID: test.id}
			assert.Equal(t, test.want, exemption.Scope())
		})
	}
	var exemption *PolicyExemption
	assert.Empty(t, exemption.Scope())
	var properties PolicyExemptionProperties
	require.NoError(t, json.Unmarshal([]byte(`{}`), &properties))
	assert.Nil(t, properties.ExpiresOn)
	assert.Nil(t, properties.ResourceSelectors)
}
