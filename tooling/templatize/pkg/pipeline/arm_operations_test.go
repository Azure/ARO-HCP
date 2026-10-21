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

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// fakeTransport returns canned HTTP responses keyed by subscription ID extracted from the request URL.
// If interceptor is set, it takes precedence over the static responses map.
type fakeTransport struct {
	responses   map[string]string
	interceptor func(req *http.Request) (*http.Response, error)
}

func (f *fakeTransport) Do(req *http.Request) (*http.Response, error) {
	if f.interceptor != nil {
		return f.interceptor(req)
	}
	for sub, body := range f.responses {
		if strings.Contains(req.URL.Path, "/subscriptions/"+sub+"/") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}
	}
	return nil, fmt.Errorf("unexpected request: %s", req.URL.Path)
}

type fakeCredential struct{}

func (f *fakeCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func fakeOperationsResponse(t *testing.T, ops ...armresources.DeploymentOperation) string {
	t.Helper()
	ptrs := make([]*armresources.DeploymentOperation, len(ops))
	for i := range ops {
		ptrs[i] = &ops[i]
	}
	return mustMarshal(t, armresources.DeploymentOperationsListResult{Value: ptrs})
}

func fakeDeploymentOp(provOp, duration string, resourceID *string) armresources.DeploymentOperation {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	props := armresources.DeploymentOperationProperties{
		ProvisioningOperation: to.Ptr(armresources.ProvisioningOperation(provOp)),
		Timestamp:             to.Ptr(ts),
		Duration:              to.Ptr(duration),
	}
	if resourceID != nil {
		props.TargetResource = &armresources.TargetResource{ID: resourceID}
	}
	return armresources.DeploymentOperation{Properties: &props}
}

func TestOperationFor(t *testing.T) {
	tests := []struct {
		name     string
		item     *armresources.DeploymentOperation
		wantNil  bool
		wantErr  bool
		validate func(t *testing.T, op *Operation)
	}{
		{
			name:    "nil item",
			item:    nil,
			wantNil: true,
		},
		{
			name:    "nil properties",
			item:    &armresources.DeploymentOperation{},
			wantNil: true,
		},
		{
			name: "operation without target resource",
			item: &armresources.DeploymentOperation{
				Properties: &armresources.DeploymentOperationProperties{
					ProvisioningOperation: to.Ptr(armresources.ProvisioningOperation("Create")),
					Timestamp:             to.Ptr(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
					Duration:              to.Ptr("PT1M"),
				},
			},
			validate: func(t *testing.T, op *Operation) {
				assert.Equal(t, "Create", op.OperationType)
				assert.Nil(t, op.Resource)
			},
		},
		{
			name: "operation with target resource parses subscription ID",
			item: &armresources.DeploymentOperation{
				Properties: &armresources.DeploymentOperationProperties{
					ProvisioningOperation: to.Ptr(armresources.ProvisioningOperation("Create")),
					Timestamp:             to.Ptr(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
					Duration:              to.Ptr("PT1M"),
					TargetResource: &armresources.TargetResource{
						ID: to.Ptr("/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.KeyVault/vaults/my-vault"),
					},
				},
			},
			validate: func(t *testing.T, op *Operation) {
				require.NotNil(t, op.Resource)
				assert.Equal(t, "sub-123", op.Resource.SubscriptionID)
				assert.Equal(t, "rg-1", op.Resource.ResourceGroup)
				assert.Equal(t, "my-vault", op.Resource.Name)
				assert.Equal(t, "Microsoft.KeyVault/vaults", op.Resource.ResourceType)
			},
		},
		{
			name: "nested deployment resource parses cross-subscription ID",
			item: &armresources.DeploymentOperation{
				Properties: &armresources.DeploymentOperationProperties{
					ProvisioningOperation: to.Ptr(armresources.ProvisioningOperation("Create")),
					Timestamp:             to.Ptr(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
					Duration:              to.Ptr("PT2M"),
					TargetResource: &armresources.TargetResource{
						ID: to.Ptr("/subscriptions/other-sub-456/resourceGroups/other-rg/providers/Microsoft.Resources/deployments/nested-deploy"),
					},
				},
			},
			validate: func(t *testing.T, op *Operation) {
				require.NotNil(t, op.Resource)
				assert.Equal(t, "other-sub-456", op.Resource.SubscriptionID)
				assert.Equal(t, "other-rg", op.Resource.ResourceGroup)
				assert.Equal(t, "nested-deploy", op.Resource.Name)
				assert.Equal(t, "Microsoft.Resources/deployments", op.Resource.ResourceType)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, err := operationFor(tt.item)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, op)
				return
			}
			require.NotNil(t, op)
			if tt.validate != nil {
				tt.validate(t, op)
			}
		})
	}
}

func TestNewCachedOperationsClientGetter(t *testing.T) {
	t.Run("returns default client for default subscription", func(t *testing.T) {
		defaultClient := &armresources.DeploymentOperationsClient{}
		getter := NewCachedOperationsClientGetter("sub-1", defaultClient, nil, nil)

		client, err := getter("sub-1")
		require.NoError(t, err)
		assert.Same(t, defaultClient, client)
	})

	t.Run("creates and caches client for new subscription", func(t *testing.T) {
		defaultClient := &armresources.DeploymentOperationsClient{}
		getter := NewCachedOperationsClientGetter("sub-1", defaultClient, nil, nil)

		client1, err := getter("sub-2")
		require.NoError(t, err)
		require.NotNil(t, client1)
		assert.NotSame(t, defaultClient, client1)

		client2, err := getter("sub-2")
		require.NoError(t, err)
		assert.Same(t, client1, client2, "should return cached client on second call")
	})
}

func TestFetchOperationsForCrossSubscription(t *testing.T) {
	parentSub := "parent-sub-111"
	childSub := "child-sub-222"

	transport := &fakeTransport{
		responses: map[string]string{
			parentSub: fakeOperationsResponse(t,
				fakeDeploymentOp("Create", "PT1M", to.Ptr(fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/child-rg/providers/Microsoft.Resources/deployments/child-deploy", childSub,
				))),
			),
			childSub: fakeOperationsResponse(t,
				fakeDeploymentOp("Create", "PT30S", to.Ptr(fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/child-rg/providers/Microsoft.KeyVault/vaults/my-vault", childSub,
				))),
			),
		},
	}

	var requestedSubs []string
	getter := func(subID string) (*armresources.DeploymentOperationsClient, error) {
		requestedSubs = append(requestedSubs, subID)
		return armresources.NewDeploymentOperationsClient(subID, &fakeCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}})
	}

	ops, err := fetchOperationsFor(context.Background(), getter, parentSub, "parent-rg", "parent-deploy")
	require.NoError(t, err)

	require.Len(t, ops, 1, "should have one top-level operation")
	require.Len(t, ops[0].Children, 1, "nested deployment should have one child operation")
	assert.Equal(t, "Microsoft.KeyVault/vaults", ops[0].Children[0].Resource.ResourceType)
	assert.Equal(t, childSub, ops[0].Children[0].Resource.SubscriptionID)

	require.Len(t, requestedSubs, 2, "getClient should be called twice (parent + child)")
	assert.Equal(t, parentSub, requestedSubs[0], "first call should be for parent subscription")
	assert.Equal(t, childSub, requestedSubs[1], "second call should be for child subscription")
}

func TestFetchOperationsForSameSubscription(t *testing.T) {
	sub := "same-sub-333"

	callCount := 0
	transport := &fakeTransport{}
	// Return a nested deployment on the first call, then a leaf resource on the second
	// to avoid infinite recursion while still testing same-subscription routing.
	transport.interceptor = func(req *http.Request) (*http.Response, error) {
		callCount++
		var body string
		if callCount == 1 {
			body = fakeOperationsResponse(t,
				fakeDeploymentOp("Create", "PT1M", to.Ptr(fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/rg/providers/Microsoft.Resources/deployments/child-deploy", sub,
				))),
			)
		} else {
			body = fakeOperationsResponse(t,
				fakeDeploymentOp("Create", "PT30S", to.Ptr(fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/my-vault", sub,
				))),
			)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}

	var requestedSubs []string
	getter := func(subID string) (*armresources.DeploymentOperationsClient, error) {
		requestedSubs = append(requestedSubs, subID)
		return armresources.NewDeploymentOperationsClient(subID, &fakeCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}})
	}

	_, err := fetchOperationsFor(context.Background(), getter, sub, "rg", "parent-deploy")
	require.NoError(t, err)

	for _, s := range requestedSubs {
		assert.Equal(t, sub, s, "all getClient calls should use the same subscription when child matches parent")
	}
}
