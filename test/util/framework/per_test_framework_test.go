// Copyright 2025 Microsoft Corporation
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
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	graphutil "github.com/Azure/ARO-HCP/internal/graph/util"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
)

func TestIsResourceGroupNotFoundError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "non-ResponseError",
			err:  fmt.Errorf("something went wrong"),
			want: false,
		},
		{
			name: "HTTP 404 status",
			err:  &azcore.ResponseError{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "ResourceGroupNotFound error code",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceGroupNotFound"},
			want: true,
		},
		{
			name: "ResourceNotFound error code",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceNotFound"},
			want: true,
		},
		{
			name: "HTTP 409 Conflict without matching error code",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ConflictError"},
			want: false,
		},
		{
			name: "HTTP 403 Forbidden",
			err:  &azcore.ResponseError{StatusCode: http.StatusForbidden},
			want: false,
		},
		{
			name: "wrapped ResponseError with 404",
			err:  fmt.Errorf("outer: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound}),
			want: true,
		},
		{
			name: "wrapped ResponseError with ResourceGroupNotFound",
			err:  fmt.Errorf("outer: %w", &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceGroupNotFound"}),
			want: true,
		},
		{
			name: "wrapped non-ResponseError",
			err:  fmt.Errorf("outer: %w", fmt.Errorf("inner")),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isResourceGroupNotFoundError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsKeyVaultNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "non-ResponseError",
			err:  fmt.Errorf("something went wrong"),
			want: false,
		},
		{
			name: "HTTP 404 status",
			err:  &azcore.ResponseError{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "HTTP 409 Conflict",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "VaultAlreadyExists"},
			want: false,
		},
		{
			name: "HTTP 403 Forbidden",
			err:  &azcore.ResponseError{StatusCode: http.StatusForbidden},
			want: false,
		},
		{
			name: "wrapped ResponseError with 404",
			err:  fmt.Errorf("outer: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound}),
			want: true,
		},
		{
			name: "wrapped non-ResponseError",
			err:  fmt.Errorf("outer: %w", fmt.Errorf("inner")),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isKeyVaultNotFound(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsIgnorableResourceGroupCleanupError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error returns false",
			err:  nil,
			want: false,
		},
		{
			name: "non-ResponseError returns false",
			err:  fmt.Errorf("some error"),
			want: false,
		},
		{
			name: "404 ResponseError returns true",
			err:  &azcore.ResponseError{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "ResourceGroupNotFound returns true",
			err:  &azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceGroupNotFound"},
			want: true,
		},
		{
			name: "joined error with all not-found errors",
			err: errors.Join(
				&azcore.ResponseError{StatusCode: http.StatusNotFound},
				&azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "ResourceGroupNotFound"},
			),
			want: true,
		},
		{
			name: "joined error with one real failure and one not-found",
			err: errors.Join(
				&azcore.ResponseError{StatusCode: http.StatusNotFound},
				&azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"},
			),
			want: false,
		},
		{
			name: "joined error with all real failures",
			err: errors.Join(
				&azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed"},
				&azcore.ResponseError{StatusCode: http.StatusConflict, ErrorCode: "SomeOtherError"},
			),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isIgnorableResourceGroupCleanupError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetARMSubscriptionsClientFactoryReturnsCachedFactory(t *testing.T) {
	t.Parallel()

	sentinel := &armsubscriptions.ClientFactory{}
	tc := &perItOrDescribeTestContext{
		armSubscriptionsClientFactory: sentinel,
	}

	got, err := tc.GetARMSubscriptionsClientFactory()
	assert.NoError(t, err)
	assert.Same(t, sentinel, got)
}

func TestGetARMSubscriptionsClientFactoryUnlockedReturnsCachedFactory(t *testing.T) {
	t.Parallel()

	sentinel := &armsubscriptions.ClientFactory{}
	tc := &perItOrDescribeTestContext{
		armSubscriptionsClientFactory: sentinel,
	}

	got, err := tc.getARMSubscriptionsClientFactoryUnlocked()
	assert.NoError(t, err)
	assert.Same(t, sentinel, got)
}

func TestGetARMSubscriptionsClientFactoryIgnoresSiblingFactories(t *testing.T) {
	t.Parallel()

	tc := newTestContextWithStubCredential()
	tc.clientFactory20240610 = &hcpsdk20240610preview.ClientFactory{}
	tc.armResourcesClientFactory = &armresources.ClientFactory{}

	got, err := tc.GetARMSubscriptionsClientFactory()
	assert.NoError(t, err, "sibling factories must not short-circuit subscriptions factory construction")
	assert.NotNil(t, got, "must not return (nil, nil) when armSubscriptionsClientFactory is unset")
	assert.Same(t, got, tc.armSubscriptionsClientFactory, "constructed factory must be cached")

	cached, err := tc.GetARMSubscriptionsClientFactory()
	assert.NoError(t, err)
	assert.Same(t, got, cached)
}

func TestGetARMSubscriptionsClientFactoryUnlockedIgnoresSiblingFactories(t *testing.T) {
	t.Parallel()

	tc := newTestContextWithStubCredential()
	tc.clientFactory20240610 = &hcpsdk20240610preview.ClientFactory{}
	tc.armResourcesClientFactory = &armresources.ClientFactory{}

	got, err := tc.getARMSubscriptionsClientFactoryUnlocked()
	assert.NoError(t, err, "sibling factories must not short-circuit unlocked subscriptions factory construction")
	assert.NotNil(t, got, "must not return (nil, nil) when armSubscriptionsClientFactory is unset")
	assert.Same(t, got, tc.armSubscriptionsClientFactory)

	cached, err := tc.getARMSubscriptionsClientFactoryUnlocked()
	assert.NoError(t, err)
	assert.Same(t, got, cached)
}

func TestGetARMSubscriptionsClientFactoryCacheHitConcurrent(t *testing.T) {
	t.Parallel()

	tc := newTestContextWithStubCredential()
	sentinel, err := tc.GetARMSubscriptionsClientFactory()
	assert.NoError(t, err)
	assert.NotNil(t, sentinel)

	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprintf("reader-%d", i), func(t *testing.T) {
			t.Parallel()
			got, err := tc.GetARMSubscriptionsClientFactory()
			assert.NoError(t, err)
			assert.Same(t, sentinel, got)
		})
	}
}

// Per-test subscriptionID is never assigned in production (the live cache is
// perBinaryInvocationTestContext.subscriptionID). These tests seed the unused
// field on purpose: they catch the Lock()/RUnlock() mismatch from PR #6312,
// which is a runtime fatal if that field is ever written. Persisting a
// per-test copy in getSubscriptionIDUnlocked is a separate follow-up.
func TestSubscriptionIDCacheHit(t *testing.T) {
	t.Parallel()

	tc := &perItOrDescribeTestContext{
		subscriptionID: "sub-123",
	}

	got, err := tc.SubscriptionID(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "sub-123", got)
}

func TestSubscriptionIDCacheHitConcurrent(t *testing.T) {
	t.Parallel()

	tc := &perItOrDescribeTestContext{
		subscriptionID: "sub-123",
	}

	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprintf("reader-%d", i), func(t *testing.T) {
			t.Parallel()
			got, err := tc.SubscriptionID(context.Background())
			assert.NoError(t, err)
			assert.Equal(t, "sub-123", got)
		})
	}
}

func TestGetGraphClientReturnsCachedClient(t *testing.T) {
	t.Parallel()

	sentinel := &graphutil.Client{}
	tc := &perItOrDescribeTestContext{
		graphClient: sentinel,
	}

	got, err := tc.GetGraphClient(context.Background())
	assert.NoError(t, err)
	assert.Same(t, sentinel, got)
}

func TestGetGraphClientUnlockedReturnsCachedClient(t *testing.T) {
	t.Parallel()

	sentinel := &graphutil.Client{}
	tc := &perItOrDescribeTestContext{
		graphClient: sentinel,
	}

	got, err := tc.getGraphClientUnlocked(context.Background())
	assert.NoError(t, err)
	assert.Same(t, sentinel, got)
}

func TestGetGraphClientPersistsOnCreate(t *testing.T) {
	t.Parallel()

	tc := newTestContextWithStubCredential()
	tc.perBinaryInvocationTestContext.azureCredentials = stubTokenCredential{
		token: unsignedJWT(`{"oid":"00000000-0000-0000-0000-000000000001","idtyp":"app"}`),
	}

	got, err := tc.GetGraphClient(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Same(t, got, tc.graphClient, "successful NewClient must be stored on the test context")

	cached, err := tc.GetGraphClient(context.Background())
	assert.NoError(t, err)
	assert.Same(t, got, cached)
}

func TestGetGraphClientUnlockedPersistsOnCreate(t *testing.T) {
	t.Parallel()

	tc := newTestContextWithStubCredential()
	tc.perBinaryInvocationTestContext.azureCredentials = stubTokenCredential{
		token: unsignedJWT(`{"oid":"00000000-0000-0000-0000-000000000001","idtyp":"app"}`),
	}

	got, err := tc.getGraphClientUnlocked(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Same(t, got, tc.graphClient)

	cached, err := tc.getGraphClientUnlocked(context.Background())
	assert.NoError(t, err)
	assert.Same(t, got, cached)
}

func TestGetGraphClientDoesNotCacheFailedCreate(t *testing.T) {
	t.Parallel()

	tc := newTestContextWithStubCredential()
	tc.perBinaryInvocationTestContext.azureCredentials = stubTokenCredential{
		err: errors.New("no token"),
	}

	got, err := tc.GetGraphClient(context.Background())
	assert.Error(t, err)
	assert.Nil(t, got)
	assert.Nil(t, tc.graphClient, "failed NewClient must not cache a client")
}

func TestRecordTestStep(t *testing.T) {
	t.Parallel()

	tc := &perItOrDescribeTestContext{}
	start := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	finish := start.Add(time.Second)

	tc.RecordTestStep("Clean up resource group rg (no RP)", start, finish)
	tc.RecordTestStep("second step", start.Add(2*time.Second), finish.Add(2*time.Second))

	assert.Len(t, tc.timingMetadata.Steps, 2)
	assert.Equal(t, "Clean up resource group rg (no RP)", tc.timingMetadata.Steps[0].Name)
	assert.Equal(t, start.Format(time.RFC3339), tc.timingMetadata.Steps[0].StartedAt)
	assert.Equal(t, finish.Format(time.RFC3339), tc.timingMetadata.Steps[0].FinishedAt)
	assert.Equal(t, "second step", tc.timingMetadata.Steps[1].Name)
}

func TestRecordTestStepConcurrent(t *testing.T) {
	t.Parallel()

	tc := &perItOrDescribeTestContext{}
	start := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	finish := start.Add(time.Second)

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			tc.RecordTestStep(fmt.Sprintf("step-%d", i), start, finish)
		}(i)
	}
	wg.Wait()

	assert.Len(t, tc.timingMetadata.Steps, goroutines)
	names := make(map[string]struct{}, goroutines)
	for _, step := range tc.timingMetadata.Steps {
		names[step.Name] = struct{}{}
	}
	assert.Len(t, names, goroutines)
}

type stubTokenCredential struct {
	token string
	err   error
}

func (s stubTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if s.err != nil {
		return azcore.AccessToken{}, s.err
	}
	return azcore.AccessToken{
		Token:     s.token,
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

func unsignedJWT(claimsJSON string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claimsJSON))
	return header + "." + payload + "."
}

func newTestContextWithStubCredential() *perItOrDescribeTestContext {
	return &perItOrDescribeTestContext{
		perBinaryInvocationTestContext: &perBinaryInvocationTestContext{
			azureCredentials: stubTokenCredential{token: "unused"},
		},
	}
}
