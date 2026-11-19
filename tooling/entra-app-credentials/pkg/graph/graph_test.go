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

package graph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	kiotaabstractions "github.com/microsoft/kiota-abstractions-go"
	"github.com/microsoft/kiota-abstractions-go/authentication"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func apiError(statusCode int) error {
	err := kiotaabstractions.NewApiError()
	err.SetStatusCode(statusCode)
	return err
}

func TestIsTransientOnlyCoversPropagationAndNetworkErrors(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusBadRequest,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		assert.False(t, IsTransient(apiError(statusCode)), "status %d", statusCode)
	}

	assert.True(t, IsTransient(apiError(http.StatusNotFound)))
	assert.True(t, IsTransient(&net.DNSError{Err: "temporary failure", IsTemporary: true}))
	assert.True(t, IsTransient(fmt.Errorf("application %q is %w", "app", ErrNotVisible)))
	assert.False(t, IsTransient(errors.New("forbidden")))
}

func TestRetryReturnsOnceTheOperationSucceeds(t *testing.T) {
	attempts := 0
	result, err := Retry(context.Background(), time.Millisecond, func(context.Context) (string, error) {
		attempts++
		if attempts < 3 {
			return "", apiError(http.StatusNotFound)
		}
		return "found", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "found", result)
	assert.Equal(t, 3, attempts)
}

func TestRetryStopsOnNonTransientError(t *testing.T) {
	attempts := 0
	err := RetryDo(context.Background(), time.Millisecond, func(context.Context) error {
		attempts++
		return errors.New("forbidden")
	})

	require.ErrorContains(t, err, "forbidden")
	assert.Equal(t, 1, attempts)
}

func TestRetryReportsLastTransientErrorWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := RetryDo(ctx, time.Millisecond, func(context.Context) error {
		return fmt.Errorf("application %q is %w", "app", ErrNotVisible)
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "application \"app\" is not visible in Microsoft Graph yet")
}

type fakePage struct {
	values []string
	next   *string
}

func (p *fakePage) GetValue() []string        { return p.values }
func (p *fakePage) GetOdataNextLink() *string { return p.next }

func TestCollectPagesFollowsEveryContinuationLink(t *testing.T) {
	link2, link3 := "page2", "page3"
	pages := map[string]*fakePage{
		link2: {values: []string{"c", "d"}, next: &link3},
		link3: {values: []string{"e"}},
	}
	var requested []string

	items, err := CollectPages(context.Background(), &fakePage{values: []string{"a", "b"}, next: &link2},
		func(_ context.Context, link string) (*fakePage, error) {
			requested = append(requested, link)
			return pages[link], nil
		})

	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, items)
	assert.Equal(t, []string{"page2", "page3"}, requested)
}

func TestCollectPagesStopsOnPageError(t *testing.T) {
	link := "page2"

	_, err := CollectPages(context.Background(), &fakePage{values: []string{"a"}, next: &link},
		func(context.Context, string) (*fakePage, error) { return nil, errors.New("boom") })

	require.ErrorContains(t, err, "boom")
}

type graphTransport func(*http.Request) (*http.Response, error)

func (transport graphTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func readOnlyGraphClient(t *testing.T, respond func(*http.Request) (int, string)) *msgraph.GraphServiceClient {
	t.Helper()
	transport := graphTransport(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method, "identity discovery must not mutate Graph")
		status, body := respond(request)
		return &http.Response{
			StatusCode:    status,
			Header:        http.Header{"Content-Type": {"application/json"}},
			ContentLength: int64(len(body)),
			Body:          io.NopCloser(strings.NewReader(body)),
			Request:       request,
		}, nil
	})
	adapter, err := msgraph.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(&authentication.AnonymousAuthenticationProvider{}, nil, nil, &http.Client{Transport: transport})
	require.NoError(t, err)
	return msgraph.NewGraphServiceClient(adapter)
}

func TestLookupApplicationAllowsDistinctUniqueAndDisplayNames(t *testing.T) {
	const applicationID = "11111111-1111-1111-1111-111111111111"
	const clientID = "22222222-2222-2222-2222-222222222222"
	client := readOnlyGraphClient(t, func(request *http.Request) (int, string) {
		assert.Equal(t, "/v1.0/applications(uniqueName='normalized-shared-identity')", request.URL.Path)
		return http.StatusOK, fmt.Sprintf(`{"id":%q,"appId":%q,"displayName":"Shared Identity"}`, applicationID, clientID)
	})
	application, err := LookupApplication(t.Context(), client, "normalized-shared-identity", "Shared Identity")
	require.NoError(t, err)
	assert.Equal(t, applicationID, *application.GetId())
	assert.Equal(t, clientID, *application.GetAppId())
}

func TestIdentityLookupsPreserveNotFound(t *testing.T) {
	client := readOnlyGraphClient(t, func(*http.Request) (int, string) {
		return http.StatusNotFound, `{"error":{"code":"Request_ResourceNotFound","message":"not found"}}`
	})
	_, err := LookupApplication(t.Context(), client, "missing", "Missing")
	assert.True(t, IsNotFound(err), "%v", err)
	_, err = LookupServicePrincipal(t.Context(), client, "22222222-2222-2222-2222-222222222222")
	assert.True(t, IsNotFound(err), "%v", err)
}

func TestAbsentDisplayNameChecksEveryPageAndQuotesFilter(t *testing.T) {
	for _, test := range []struct {
		name, secondPage, wantError string
	}{
		{name: "absent", secondPage: `{"value":[]}`},
		{name: "collision on later page", secondPage: `{"value":[{"id":"11111111-1111-1111-1111-111111111111"}]}`, wantError: "another uniqueName"},
		{name: "missing continuation collection", wantError: "no collection"},
		{name: "missing continuation values", secondPage: `{}`, wantError: "no collection"},
		{name: "null continuation values", secondPage: `{"value":null}`, wantError: "no collection"},
		{name: "malformed collision", secondPage: `{"value":[null]}`, wantError: "another uniqueName"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			client := readOnlyGraphClient(t, func(request *http.Request) (int, string) {
				requests++
				if requests == 1 {
					assert.Equal(t, "displayName eq 'owner''s identity'", request.URL.Query().Get("$filter"))
					return http.StatusOK, `{"value":[],"@odata.nextLink":"https://graph.microsoft.com/v1.0/applications?$skiptoken=next"}`
				}
				assert.Equal(t, "next", request.URL.Query().Get("$skiptoken"))
				if test.secondPage == "" {
					return http.StatusNoContent, ""
				}
				return http.StatusOK, test.secondPage
			})
			err := CheckApplicationDisplayName(t.Context(), client, "owner's identity", "")
			assert.Equal(t, 2, requests)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestDisplayNameMatchRequiresCompleteContinuation(t *testing.T) {
	const applicationID = "11111111-1111-1111-1111-111111111111"
	for _, continuation := range []string{`{}`, `{"value":null}`} {
		t.Run(continuation, func(t *testing.T) {
			requests := 0
			client := readOnlyGraphClient(t, func(request *http.Request) (int, string) {
				requests++
				if requests == 1 {
					return http.StatusOK, fmt.Sprintf(`{"value":[{"id":%q}],"@odata.nextLink":"https://graph.microsoft.com/v1.0/applications?$skiptoken=next"}`, applicationID)
				}
				assert.Equal(t, "next", request.URL.Query().Get("$skiptoken"))
				return http.StatusOK, continuation
			})
			err := CheckApplicationDisplayName(t.Context(), client, "identity", applicationID)
			require.ErrorContains(t, err, "no collection")
			assert.Equal(t, 2, requests)
		})
	}
}
