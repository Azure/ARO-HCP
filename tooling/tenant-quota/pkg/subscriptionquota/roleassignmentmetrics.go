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

package subscriptionquota

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const roleAssignmentMetricsAPIVersion = "2019-08-01-preview"

type roleAssignmentMetrics struct {
	currentCount int64
	limit        int64
}

type roleAssignmentMetricsResponse struct {
	RoleAssignmentsCurrentCount *int64 `json:"roleAssignmentsCurrentCount"`
	RoleAssignmentsLimit        *int64 `json:"roleAssignmentsLimit"`
}

type roleAssignmentMetricsClient struct {
	internal       *azcorearm.Client
	subscriptionID string
}

func newRoleAssignmentMetricsClient(subscriptionID string, credential azcore.TokenCredential,
	options *azcorearm.ClientOptions) (*roleAssignmentMetricsClient, error) {

	client, err := azcorearm.NewClient("tenant-quota", "v1.0.0", credential, options)
	if err != nil {
		return nil, fmt.Errorf("create ARM client: %w", err)
	}

	return &roleAssignmentMetricsClient{
		internal:       client,
		subscriptionID: subscriptionID,
	}, nil
}

func (c *roleAssignmentMetricsClient) Get(ctx context.Context) (roleAssignmentMetrics, error) {
	path := fmt.Sprintf(
		"/subscriptions/%s/providers/Microsoft.Authorization/roleAssignmentsUsageMetrics",
		c.subscriptionID,
	)
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(c.internal.Endpoint(), path))
	if err != nil {
		return roleAssignmentMetrics{}, fmt.Errorf("create role assignment metrics request: %w", err)
	}

	query := req.Raw().URL.Query()
	query.Set("api-version", roleAssignmentMetricsAPIVersion)
	req.Raw().URL.RawQuery = query.Encode()
	req.Raw().Header.Set("Accept", "application/json")

	resp, err := c.internal.Pipeline().Do(req)
	if err != nil {
		return roleAssignmentMetrics{}, fmt.Errorf("get role assignment metrics: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return roleAssignmentMetrics{}, runtime.NewResponseError(resp)
	}

	var result roleAssignmentMetricsResponse
	if err := runtime.UnmarshalAsJSON(resp, &result); err != nil {
		return roleAssignmentMetrics{}, fmt.Errorf("decode role assignment metrics: %w", err)
	}
	if result.RoleAssignmentsCurrentCount == nil {
		return roleAssignmentMetrics{}, fmt.Errorf("role assignment metrics response is missing roleAssignmentsCurrentCount")
	}
	if *result.RoleAssignmentsCurrentCount < 0 {
		return roleAssignmentMetrics{}, fmt.Errorf(
			"role assignment metrics response has negative roleAssignmentsCurrentCount %d",
			*result.RoleAssignmentsCurrentCount,
		)
	}
	if result.RoleAssignmentsLimit == nil {
		return roleAssignmentMetrics{}, fmt.Errorf("role assignment metrics response is missing roleAssignmentsLimit")
	}
	if *result.RoleAssignmentsLimit <= 0 {
		return roleAssignmentMetrics{}, fmt.Errorf(
			"role assignment metrics response has non-positive roleAssignmentsLimit %d",
			*result.RoleAssignmentsLimit,
		)
	}

	return roleAssignmentMetrics{
		currentCount: *result.RoleAssignmentsCurrentCount,
		limit:        *result.RoleAssignmentsLimit,
	}, nil
}
