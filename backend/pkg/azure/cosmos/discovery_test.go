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

package cosmos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const testAccountID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/account"

type testCredential struct{}

func (testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type testTransport func(*http.Request) (*http.Response, error)

func (transport testTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func testAccount(t *testing.T, respond func(*http.Request) (string, error)) *Account {
	return testAccountWithID(t, testAccountID, respond)
}

func testAccountWithID(t *testing.T, id string, respond func(*http.Request) (string, error)) *Account {
	t.Helper()
	account, err := NewAccount(id, testCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Retry: policy.RetryOptions{MaxRetries: -1},
		Transport: testTransport(func(request *http.Request) (*http.Response, error) {
			body, err := respond(request)
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: http.StatusOK, Request: request, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}),
	}})
	require.NoError(t, err)
	return account
}

func TestMixedCaseAccountDiscovery(t *testing.T) {
	for _, throughput := range []struct {
		name, body string
		want       float64
	}{
		{"manual", `{"properties":{"resource":{"throughput":400}}}`, 400},
		{"autoscale", `{"properties":{"resource":{"throughput":400,"autoscaleSettings":{"maxThroughput":4000,"targetMaxThroughput":9000}}}}`, 4000},
	} {
		t.Run(throughput.name, func(t *testing.T) {
			id := strings.ReplaceAll(testAccountID, "databaseAccounts/account", "databaseAccounts/AcCoUnT")
			id = strings.ReplaceAll(id, "Microsoft.DocumentDB", "microsoft.documentdb")
			var paths []string
			account := testAccountWithID(t, id, func(request *http.Request) (string, error) {
				paths = append(paths, request.URL.Path)
				switch request.URL.Path {
				case testAccountID:
					return `{"properties":{"documentEndpoint":"https://account.documents.azure.com/"}}`, nil
				case testAccountID + "/sqlDatabases/account":
					return `{"properties":{"resource":{"id":"account"}}}`, nil
				case testAccountID + "/sqlDatabases/account/containers/MixedContainer/throughputSettings/default":
					return throughput.body, nil
				default:
					return "", fmt.Errorf("unexpected request path %s", request.URL.Path)
				}
			})
			endpoint, database, err := account.Discover(t.Context())
			require.NoError(t, err)
			require.Equal(t, "account", database)
			require.Equal(t, "https://account.documents.azure.com/", endpoint)
			limit, err := account.ContainerThroughput(t.Context(), "MixedContainer")
			require.NoError(t, err)
			require.Equal(t, throughput.want, limit)
			require.Len(t, paths, 3)
			require.Equal(t, "AcCoUnT", account.id.Name)
		})
	}
}

func TestParseAccountID(t *testing.T) {
	for _, value := range []string{testAccountID, strings.Replace(testAccountID, "Microsoft.DocumentDB/databaseAccounts", "microsoft.documentdb/databaseaccounts", 1)} {
		id, err := ParseAccountID(value)
		require.NoError(t, err)
		require.Equal(t, "sub", id.SubscriptionID)
		require.Equal(t, "rg", id.ResourceGroupName)
		require.Equal(t, "account", id.Name)
	}
	for _, value := range []string{"", "account", "/subscriptions/sub", testAccountID + "/sqlDatabases/account", testAccountID + "/", testAccountID + "?query", strings.Replace(testAccountID, "Microsoft.DocumentDB", "Microsoft.Storage", 1), "/providers/Microsoft.DocumentDB/databaseAccounts/account"} {
		t.Run(value, func(t *testing.T) { _, err := ParseAccountID(value); require.Error(t, err) })
	}
}

func TestDiscover(t *testing.T) {
	for _, test := range []struct {
		name, accountJSON, databaseJSON, failurePath string
		wantError                                    bool
	}{
		{name: "valid", accountJSON: `{"properties":{"documentEndpoint":"https://account.documents.azure.com/"}}`, databaseJSON: `{"properties":{"resource":{"id":"account"}}}`},
		{name: "missing endpoint", accountJSON: `{}`, wantError: true},
		{name: "empty endpoint", accountJSON: `{"properties":{"documentEndpoint":""}}`, wantError: true},
		{name: "insecure endpoint", accountJSON: `{"properties":{"documentEndpoint":"http://account/"}}`, wantError: true},
		{name: "account failure", failurePath: testAccountID, wantError: true},
		{name: "database failure", accountJSON: `{"properties":{"documentEndpoint":"https://account/"}}`, failurePath: testAccountID + "/sqlDatabases/account", wantError: true},
		{name: "missing database", accountJSON: `{"properties":{"documentEndpoint":"https://account/"}}`, databaseJSON: `{}`, wantError: true},
		{name: "mismatched database", accountJSON: `{"properties":{"documentEndpoint":"https://account/"}}`, databaseJSON: `{"properties":{"resource":{"id":"other"}}}`, wantError: true},
		{name: "database case mismatch", accountJSON: `{"properties":{"documentEndpoint":"https://account/"}}`, databaseJSON: `{"properties":{"resource":{"id":"Account"}}}`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := testAccount(t, func(request *http.Request) (string, error) {
				require.Equal(t, http.MethodGet, request.Method)
				if request.URL.Path == test.failurePath {
					return "", errors.New("ARM unavailable")
				}
				switch request.URL.Path {
				case testAccountID:
					return test.accountJSON, nil
				case testAccountID + "/sqlDatabases/account":
					return test.databaseJSON, nil
				default:
					t.Fatalf("unexpected ARM request %s", request.URL.Path)
					return "", nil
				}
			})
			endpoint, database, err := account.Discover(t.Context())
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "https://account.documents.azure.com/", endpoint)
			require.Equal(t, "account", database)
		})
	}
}

func TestContainerThroughput(t *testing.T) {
	for _, test := range []struct {
		name, resource string
		want           float64
	}{
		{"manual", `{"throughput":4000}`, 4000},
		{"autoscale", `{"throughput":400,"autoscaleSettings":{"maxThroughput":19000,"targetMaxThroughput":50000}}`, 19000},
		{"missing", `{}`, 0},
		{"zero", `{"throughput":0}`, 0},
		{"negative", `{"throughput":-1}`, 0},
		{"pending only", `{"autoscaleSettings":{"targetMaxThroughput":50000}}`, 0},
		{"invalid autoscale with manual", `{"throughput":4000,"autoscaleSettings":{}}`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := testAccount(t, func(request *http.Request) (string, error) {
				require.Equal(t, testAccountID+"/sqlDatabases/account/containers/MC-one/throughputSettings/default", request.URL.Path)
				return `{"properties":{"resource":` + test.resource + `}}`, nil
			})
			throughput, err := account.ContainerThroughput(t.Context(), "MC-one")
			if test.want == 0 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, throughput)
		})
	}
	account := testAccount(t, func(*http.Request) (string, error) { return "", errors.New("forbidden") })
	_, err := account.ContainerThroughput(t.Context(), "Resources")
	require.ErrorContains(t, err, "forbidden")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = account.ContainerThroughput(ctx, "Resources")
	require.Error(t, err)
}
