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

package cosmosclient

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosratelimit"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

type passthroughPolicy struct{}

func (*passthroughPolicy) Do(req *policy.Request) (*http.Response, error) { return req.Next() }

func TestDatabaseClientRequiresBucket(t *testing.T) {
	client, err := NewCosmosDatabaseClient("https://cosmos.test", "test", Options{}, nil)
	require.ErrorContains(t, err, "requires a token bucket")
	require.Nil(t, client)
}

func TestIndependentClientsRetainBucketsWithSharedOptions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		credential, err := azcosmos.NewKeyCredential("ZmFrZQ==")
		require.NoError(t, err)
		// Spare capacity reproduces append overwriting another client's policy.
		policies := make([]policy.Policy, 1, 4)
		policies[0] = &passthroughPolicy{}
		options := Options{KeyCredential: &credential, ClientOptions: azcore.ClientOptions{
			Retry: policy.RetryOptions{MaxRetries: -1}, PerRetryPolicies: policies,
			Transport: testTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "" || req.URL.Path == "/" {
					return &http.Response{StatusCode: http.StatusOK, Request: req, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"test"}`))}, nil
				}
				return &http.Response{StatusCode: http.StatusNotFound, Request: req,
					Header: http.Header{"X-Ms-Request-Charge": []string{"3"}},
					Body:   io.NopCloser(strings.NewReader(`{"code":"NotFound"}`))}, nil
			}),
		}}
		var containers []*azcosmos.ContainerClient
		for _, name := range []string{"one", "two"} {
			bucket, err := cosmosratelimit.NewTokenBucket(name, 1, 1)
			require.NoError(t, err)
			database, err := NewCosmosDatabaseClient("https://cosmos.test", "test", options, bucket)
			require.NoError(t, err)
			container, err := database.NewContainer("Resources")
			require.NoError(t, err)
			containers = append(containers, container)
		}
		started := time.Now()
		read := func(container *azcosmos.ContainerClient) {
			_, err := container.ReadItem(t.Context(), azcosmos.NewPartitionKeyString("sub"), "item", nil)
			var responseError *azcore.ResponseError
			require.ErrorAs(t, err, &responseError)
			require.Equal(t, http.StatusNotFound, responseError.StatusCode)
		}
		read(containers[0])
		read(containers[1])
		require.Equal(t, time.Duration(0), time.Since(started), "one client's debt must not block another")
		read(containers[0])
		require.Equal(t, 2*time.Second, time.Since(started), "the first client must retain its own debt")
		require.Same(t, policies[0], options.PerRetryPolicies[0])
		require.Nil(t, policies[:cap(policies)][1], "constructors must not mutate the supplied options' backing array")
	})
}
