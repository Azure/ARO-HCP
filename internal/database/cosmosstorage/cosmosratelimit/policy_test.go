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

package cosmosratelimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type testTransport func(*http.Request) (*http.Response, error)

func (t testTransport) Do(req *http.Request) (*http.Response, error) { return t(req) }

func pipeline(bucket *TokenBucket, transport testTransport, retries int32) runtime.Pipeline {
	return runtime.NewPipeline("test", "v0.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
		PerRetryPolicies: []policy.Policy{NewPolicy(bucket)}, Transport: transport,
		Retry:     policy.RetryOptions{MaxRetries: retries, RetryDelay: time.Nanosecond, MaxRetryDelay: time.Nanosecond},
		Telemetry: policy.TelemetryOptions{Disabled: true},
	})
}

func response(req *http.Request, status int, charge string) *http.Response {
	resp := &http.Response{StatusCode: status, Request: req, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}
	resp.Header.Set("x-ms-request-charge", charge)
	return resp
}

func TestPolicyChargesEveryRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, err := NewTokenBucket("test", 1, 1)
		require.NoError(t, err)
		attempts := 0
		start := time.Now()
		p := pipeline(bucket, func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return response(req, http.StatusInternalServerError, "3.5"), nil
			}
			require.Equal(t, 2500*time.Millisecond, time.Since(start), "retry must wait for the first attempt's charge")
			return response(req, http.StatusOK, "0.25"), nil
		}, 1)
		err = bucket.Do(t.Context(), func(ctx context.Context) error {
			req, err := runtime.NewRequest(ctx, http.MethodGet, "https://cosmos.test/dbs/test/colls/Resources/docs/item")
			require.NoError(t, err)
			resp, err := p.Do(req)
			if resp != nil {
				require.NoError(t, resp.Body.Close())
			}
			return err
		})
		require.NoError(t, err)
		require.Equal(t, 2, attempts)
		require.Equal(t, -0.25, bucket.tokens)
	})
}

func TestPolicyResponses(t *testing.T) {
	for _, charge := range []string{"2.5", "0", "", "garbage", "-1", "NaN", "+Inf", "-Inf"} {
		t.Run(charge, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				bucket, err := NewTokenBucket("test", 1, 1)
				require.NoError(t, err)
				p := pipeline(bucket, func(req *http.Request) (*http.Response, error) {
					return response(req, http.StatusForbidden, charge), nil
				}, -1)
				req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://cosmos.test")
				require.NoError(t, err)
				resp, err := p.Do(req)
				require.NoError(t, err)
				require.Equal(t, http.StatusForbidden, resp.StatusCode)
				require.NoError(t, resp.Body.Close())
				expected := 1.0
				if charge == "2.5" {
					expected = -1.5
				}
				require.Equal(t, expected, bucket.tokens)
			})
		})
	}
}

func TestPolicyTransportFailureAndUnlimitedRequests(t *testing.T) {
	bucket, err := NewTokenBucket("test", 1, 1)
	require.NoError(t, err)
	failure := errors.New("transport failed")
	p := pipeline(bucket, func(*http.Request) (*http.Response, error) { return nil, failure }, -1)
	req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://cosmos.test")
	require.NoError(t, err)
	_, err = p.Do(req)
	require.ErrorIs(t, err, failure)
	require.Equal(t, 1.0, bucket.tokens)

	bucket.Consume(100)
	p = pipeline(NewUnlimitedTokenBucket("test"), func(req *http.Request) (*http.Response, error) { return response(req, http.StatusOK, "100"), nil }, -1)
	req, err = runtime.NewRequest(t.Context(), http.MethodGet, "https://cosmos.test")
	require.NoError(t, err)
	resp, err := p.Do(req)
	require.NoError(t, err, "explicitly unlimited clients must not wait")
	require.NoError(t, resp.Body.Close())
}

func TestPolicyCancellationBetweenPages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bucket, err := NewTokenBucket("test", 1, 1)
		require.NoError(t, err)
		calls := 0
		p := pipeline(bucket, func(req *http.Request) (*http.Response, error) {
			calls++
			return response(req, http.StatusOK, "10"), nil
		}, -1)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		for range 2 {
			req, err := runtime.NewRequest(ctx, http.MethodGet, "https://cosmos.test")
			require.NoError(t, err)
			resp, err := p.Do(req)
			if calls == 1 && resp == nil {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
			}
		}
		require.Equal(t, 1, calls)
	})
}
