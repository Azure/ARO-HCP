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
const testAccountResponse = `{"id":"` + testAccountID + `","name":"account","properties":{"documentEndpoint":"https://account.documents.test/"}}`
const testDatabaseResponse = `{"properties":{"resource":{"id":"account"}}}`

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
		{name: "valid", accountJSON: testAccountResponse, databaseJSON: testDatabaseResponse},
		{name: "missing account properties", accountJSON: `{"id":"` + testAccountID + `","name":"account"}`, wantError: true},
		{name: "missing endpoint", accountJSON: strings.Replace(testAccountResponse, `"documentEndpoint":"https://account.documents.test/"`, ``, 1), wantError: true},
		{name: "empty endpoint", accountJSON: strings.Replace(testAccountResponse, "https://account.documents.test/", "", 1), wantError: true},
		{name: "insecure endpoint", accountJSON: strings.Replace(testAccountResponse, "https://", "http://", 1), wantError: true},
		{name: "missing account name", accountJSON: strings.Replace(testAccountResponse, `"name":"account",`, ``, 1), wantError: true},
		{name: "empty account name", accountJSON: strings.Replace(testAccountResponse, `"name":"account"`, `"name":""`, 1), wantError: true},
		{name: "inconsistent account name", accountJSON: strings.Replace(testAccountResponse, `"name":"account"`, `"name":"other"`, 1), wantError: true},
		{name: "missing account ID", accountJSON: strings.Replace(testAccountResponse, `"id":"`+testAccountID+`",`, ``, 1), wantError: true},
		{name: "empty account ID", accountJSON: strings.Replace(testAccountResponse, testAccountID, "", 1), wantError: true},
		{name: "inconsistent account ID", accountJSON: strings.Replace(testAccountResponse, "/resourceGroups/rg/", "/resourceGroups/other/", 1), wantError: true},
		{name: "account failure", failurePath: testAccountID, wantError: true},
		{name: "database failure", accountJSON: testAccountResponse, failurePath: testAccountID + "/sqlDatabases/account", wantError: true},
		{name: "missing database", accountJSON: testAccountResponse, databaseJSON: `{}`, wantError: true},
		{name: "missing database resource", accountJSON: testAccountResponse, databaseJSON: `{"properties":{}}`, wantError: true},
		{name: "missing database ID", accountJSON: testAccountResponse, databaseJSON: `{"properties":{"resource":{}}}`, wantError: true},
		{name: "empty database ID", accountJSON: testAccountResponse, databaseJSON: `{"properties":{"resource":{"id":""}}}`, wantError: true},
		{name: "mismatched database", accountJSON: testAccountResponse, databaseJSON: `{"properties":{"resource":{"id":"other"}}}`, wantError: true},
		{name: "database case mismatch", accountJSON: testAccountResponse, databaseJSON: `{"properties":{"resource":{"id":"Account"}}}`, wantError: true},
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
			discovered, err := account.Discover(t.Context())
			if test.wantError {
				require.Error(t, err)
				require.Nil(t, discovered)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "https://account.documents.test/", discovered.Endpoint())
			require.Equal(t, "account", discovered.DatabaseName())
		})
	}
}
