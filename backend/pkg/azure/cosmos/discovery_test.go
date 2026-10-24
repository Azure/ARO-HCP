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
	"sync"
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
				case strings.Replace(testAccountID, "databaseAccounts/account", "databaseAccounts/AcCoUnT", 1):
					return strings.Replace(testAccountResponse, testAccountID, strings.ToUpper(testAccountID), 1), nil
				case testAccountID + "/sqlDatabases/account":
					return testDatabaseResponse, nil
				case testAccountID + "/sqlDatabases/account/containers/Resources/throughputSettings/default":
					return throughput.body, nil
				default:
					return "", fmt.Errorf("unexpected request path %s", request.URL.Path)
				}
			})
			endpoint, database, err := account.Discover(t.Context())
			require.NoError(t, err)
			require.Equal(t, "account", database)
			require.Equal(t, "https://account.documents.test/", endpoint)
			limit, err := account.ContainerThroughput(t.Context(), "Resources")
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
			endpoint, database, err := account.Discover(t.Context())
			if test.wantError {
				require.Error(t, err)
				_, err = account.ContainerThroughput(t.Context(), "Resources")
				require.ErrorContains(t, err, "must be discovered")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "https://account.documents.test/", endpoint)
			require.Equal(t, "account", database)
		})
	}
}

func discoveredTestAccount(t *testing.T, throughput func(*http.Request) (string, error)) *Account {
	t.Helper()
	account := testAccount(t, func(request *http.Request) (string, error) {
		switch request.URL.Path {
		case testAccountID:
			return testAccountResponse, nil
		case testAccountID + "/sqlDatabases/account":
			return testDatabaseResponse, nil
		default:
			return throughput(request)
		}
	})
	_, _, err := account.Discover(t.Context())
	require.NoError(t, err)
	return account
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
			account := discoveredTestAccount(t, func(request *http.Request) (string, error) {
				require.Equal(t, testAccountID+"/sqlDatabases/account/containers/Resources/throughputSettings/default", request.URL.Path)
				return `{"properties":{"resource":` + test.resource + `}}`, nil
			})
			throughput, err := account.ContainerThroughput(t.Context(), "Resources")
			if test.want == 0 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, throughput)
		})
	}
	account := discoveredTestAccount(t, func(*http.Request) (string, error) { return "", errors.New("forbidden") })
	_, err := account.ContainerThroughput(t.Context(), "Resources")
	require.ErrorContains(t, err, "forbidden")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = account.ContainerThroughput(ctx, "Resources")
	require.Error(t, err)
}

func TestDiscoveryNamesRequireSuccess(t *testing.T) {
	recovered := false
	var paths []string
	account := testAccount(t, func(request *http.Request) (string, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case testAccountID:
			return testAccountResponse, nil
		case testAccountID + "/sqlDatabases/account":
			if !recovered {
				return "", errors.New("database unavailable")
			}
			return testDatabaseResponse, nil
		case testAccountID + "/sqlDatabases/account/containers/Fleet/throughputSettings/default":
			return `{"properties":{"resource":{"throughput":4000}}}`, nil
		default:
			return "", fmt.Errorf("unexpected request %s", request.URL.Path)
		}
	})
	_, err := account.ContainerThroughput(t.Context(), "Fleet")
	require.ErrorContains(t, err, "must be discovered")
	require.Empty(t, paths)
	_, _, err = account.Discover(t.Context())
	require.ErrorContains(t, err, "database unavailable")
	_, err = account.ContainerThroughput(t.Context(), "Fleet")
	require.ErrorContains(t, err, "must be discovered")
	require.Len(t, paths, 2)
	recovered = true
	_, database, err := account.Discover(t.Context())
	require.NoError(t, err)
	require.Equal(t, "account", database)
	limit, err := account.ContainerThroughput(t.Context(), "Fleet")
	require.NoError(t, err)
	require.Equal(t, float64(4000), limit)
	require.Len(t, paths, 5)
}

func TestDiscoveryNamesConcurrentUse(t *testing.T) {
	account := discoveredTestAccount(t, func(request *http.Request) (string, error) {
		if request.URL.Path != testAccountID+"/sqlDatabases/account/containers/Billing/throughputSettings/default" {
			return "", fmt.Errorf("unexpected request %s", request.URL.Path)
		}
		return `{"properties":{"resource":{"throughput":4000}}}`, nil
	})
	var workers sync.WaitGroup
	errors := make(chan error, 20)
	for range 10 {
		workers.Go(func() {
			_, _, err := account.Discover(t.Context())
			errors <- err
		})
		workers.Go(func() {
			_, err := account.ContainerThroughput(t.Context(), "Billing")
			errors <- err
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}
