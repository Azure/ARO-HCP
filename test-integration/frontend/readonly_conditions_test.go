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

package frontend

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/test-integration/utils/databasemutationhelpers"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

var testConditions = []api.Condition{
	{
		Type:               api.ConditionTypeAvailable,
		Status:             api.ConditionStatusTypeTrue,
		LastTransitionTime: time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
		Reason:             "AsExpected",
		Message:            "All components are running and healthy.",
	},
	{
		Type:               api.ConditionTypeDegraded,
		Status:             api.ConditionStatusTypeFalse,
		LastTransitionTime: time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
		Reason:             "AsExpected",
		Message:            "",
	},
}

func TestReadOnlyConditions(t *testing.T) {
	defer integrationutils.VerifyNoNewGoLeaks(t)
	integrationutils.WithAndWithoutCosmos(t, testReadOnlyConditions)
}

func testReadOnlyConditions(t *testing.T, withMock bool) {
	tests := []struct {
		name string
		fn   func(t *testing.T, ti *integrationutils.IntegrationTestInfo, subscriptionID string)
	}{
		{
			name: "Cluster/conditions-returned-on-GET",
			fn:   testClusterConditionsReturnedOnGET,
		},
		{
			name: "Cluster/conditions-survive-PUT",
			fn:   testClusterConditionsSurvivePUT,
		},
		{
			name: "Cluster/conditions-survive-PATCH",
			fn:   testClusterConditionsSurvivePATCH,
		},
		{
			name: "Cluster/conditions-not-in-older-versions",
			fn:   testClusterConditionsNotInOlderVersions,
		},
		{
			name: "NodePool/conditions-returned-on-GET",
			fn:   testNodePoolConditionsReturnedOnGET,
		},
		{
			name: "NodePool/conditions-survive-PUT",
			fn:   testNodePoolConditionsSurvivePUT,
		},
		{
			name: "ExternalAuth/conditions-returned-on-GET",
			fn:   testExternalAuthConditionsReturnedOnGET,
		},
		{
			name: "ExternalAuth/conditions-survive-PUT",
			fn:   testExternalAuthConditionsSurvivePUT,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			ctx = utils.ContextWithLogger(ctx, integrationutils.DefaultLogger(t))
			logger := utils.LoggerFromContext(ctx)

			testInfo, err := integrationutils.NewIntegrationTestInfoFromEnv(ctx, t, withMock)
			require.NoError(t, err)
			cleanupCtx := context.Background()
			cleanupCtx = utils.ContextWithLogger(cleanupCtx, integrationutils.DefaultLogger(t))
			defer testInfo.Cleanup(cleanupCtx)

			frontendStarted := atomic.Bool{}
			frontendErrCh := make(chan error, 1)
			defer func() {
				if frontendStarted.Load() {
					require.NoError(t, <-frontendErrCh)
				}
			}()
			adminAPIStarted := atomic.Bool{}
			adminAPIErrCh := make(chan error, 1)
			defer func() {
				if adminAPIStarted.Load() {
					require.NoError(t, <-adminAPIErrCh)
				}
			}()
			defer cancel()
			go func() {
				frontendStarted.Store(true)
				frontendErrCh <- testInfo.Frontend.Run(ctx)
			}()
			go func() {
				adminAPIStarted.Store(true)
				adminAPIErrCh <- testInfo.AdminAPI.Run(ctx)
			}()

			err = wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
				for _, url := range []string{testInfo.FrontendURL, testInfo.AdminURL} {
					resp, err := http.Get(url)
					if err != nil {
						return false, nil
					}
					if closeErr := resp.Body.Close(); closeErr != nil {
						logger.Error(closeErr, "failed to close response body")
					}
				}
				return true, nil
			})
			require.NoError(t, err)

			subscriptionID := "6b690bec-0c16-4ecb-8f67-781caf40bba7"
			subscriptionResourceID := api.Must(arm.ToSubscriptionResourceID(subscriptionID))
			subscriptionJSON := []byte(`{
				"resourceId": "/subscriptions/6b690bec-0c16-4ecb-8f67-781caf40bba7",
				"state": "Registered",
				"registrationDate": "2025-12-19T19:53:15+00:00",
				"properties": null
			}`)
			accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2026)
			require.NoError(t, accessor.CreateOrUpdate(ctx, subscriptionResourceID.String(), subscriptionJSON))

			tt.fn(t, testInfo, subscriptionID)
		})
	}
}

// injectClusterConditions sets conditions directly in the database,
// simulating what the backend would do.
func injectClusterConditions(t *testing.T, ctx context.Context, dbClient database.ResourcesDBClient, subscriptionID, resourceGroupName, clusterName string, conditions []api.Condition) {
	t.Helper()
	cluster, err := dbClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, clusterName)
	require.NoError(t, err)
	cluster.ServiceProviderProperties.Conditions = conditions
	_, err = dbClient.HCPClusters(subscriptionID, resourceGroupName).Replace(ctx, cluster, nil)
	require.NoError(t, err)
}

// injectNodePoolConditions sets conditions directly in the database.
func injectNodePoolConditions(t *testing.T, ctx context.Context, dbClient database.ResourcesDBClient, subscriptionID, resourceGroupName, clusterName, nodePoolName string, conditions []api.Condition) {
	t.Helper()
	np, err := dbClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).Get(ctx, nodePoolName)
	require.NoError(t, err)
	np.Properties.Conditions = conditions
	_, err = dbClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).Replace(ctx, np, nil)
	require.NoError(t, err)
}

// injectExternalAuthConditions sets conditions directly in the database.
func injectExternalAuthConditions(t *testing.T, ctx context.Context, dbClient database.ResourcesDBClient, subscriptionID, resourceGroupName, clusterName, authName string, conditions []api.Condition) {
	t.Helper()
	ea, err := dbClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName).Get(ctx, authName)
	require.NoError(t, err)
	ea.Properties.Conditions = conditions
	_, err = dbClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(clusterName).Replace(ctx, ea, nil)
	require.NoError(t, err)
}

// getConditionsFromResponse extracts the conditions array from a GET response map.
func getConditionsFromResponse(t *testing.T, responseMap map[string]any) []any {
	t.Helper()
	props, ok := responseMap["properties"].(map[string]any)
	require.True(t, ok, "response should have properties")
	conditionsRaw, ok := props["conditions"]
	if !ok {
		return nil
	}
	conditions, ok := conditionsRaw.([]any)
	require.True(t, ok, "conditions should be an array")
	return conditions
}

// assertConditionsMatch verifies that the conditions in the response match the expected test conditions.
func assertConditionsMatch(t *testing.T, conditions []any) {
	t.Helper()
	require.Len(t, conditions, len(testConditions), "should have %d conditions", len(testConditions))

	for i, c := range conditions {
		cond, ok := c.(map[string]any)
		require.True(t, ok, "condition[%d] should be a map", i)
		require.Equal(t, string(testConditions[i].Type), cond["type"], "condition[%d].type mismatch", i)
		require.Equal(t, string(testConditions[i].Status), cond["status"], "condition[%d].status mismatch", i)
		require.Equal(t, testConditions[i].Reason, cond["reason"], "condition[%d].reason mismatch", i)
		require.NotEmpty(t, cond["lastTransitionTime"], "condition[%d].lastTransitionTime should be set", i)

		// Empty string message is serialized as nil by PtrOrNil in the API layer,
		// so it may be absent from the JSON response.
		expectedMsg := testConditions[i].Message
		if expectedMsg == "" {
			actualMsg, hasMsg := cond["message"]
			if hasMsg {
				require.Equal(t, "", actualMsg, "condition[%d].message mismatch", i)
			}
		} else {
			require.Equal(t, expectedMsg, cond["message"], "condition[%d].message mismatch", i)
		}
	}
}

func testClusterConditionsReturnedOnGET(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "cond-get"
	resourceID := clusterResourceID(clusterName)

	createClusterAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName)
	injectClusterConditions(t, ctx, testInfo.ResourcesDBClient(), subscriptionID, "resourceGroupName", clusterName, testConditions)

	_, responseMap := getResourceResponse(t, ctx, testInfo, v2026, resourceID)
	conditions := getConditionsFromResponse(t, responseMap)
	assertConditionsMatch(t, conditions)
}

func testClusterConditionsSurvivePUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "cond-put"
	resourceID := clusterResourceID(clusterName)

	createClusterAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName)
	injectClusterConditions(t, ctx, testInfo.ResourcesDBClient(), subscriptionID, "resourceGroupName", clusterName, testConditions)

	// GET via v2026 — response includes conditions
	body, _ := getResourceResponse(t, ctx, testInfo, v2026, resourceID)

	// PUT back the same body (which includes the read-only conditions)
	v2026Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2026)
	require.NoError(t, v2026Accessor.CreateOrUpdate(ctx, resourceID, body))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, clusterName))

	// GET again — conditions should still be present from server side
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2026, resourceID)
	conditions := getConditionsFromResponse(t, afterMap)
	assertConditionsMatch(t, conditions)
}

func testClusterConditionsSurvivePATCH(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "cond-patch"
	resourceID := clusterResourceID(clusterName)

	createClusterAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName)
	injectClusterConditions(t, ctx, testInfo.ResourcesDBClient(), subscriptionID, "resourceGroupName", clusterName, testConditions)

	// PATCH an unrelated field
	patchBody := []byte(`{"tags": {"patched": "true"}}`)
	v2026Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2026)
	require.NoError(t, v2026Accessor.Patch(ctx, resourceID, patchBody))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, clusterName))

	// GET — conditions should still be present
	_, afterMap := getResourceResponse(t, ctx, testInfo, v2026, resourceID)
	conditions := getConditionsFromResponse(t, afterMap)
	assertConditionsMatch(t, conditions)
}

func testClusterConditionsNotInOlderVersions(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "cond-older"
	resourceID := clusterResourceID(clusterName)

	createClusterAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName)
	injectClusterConditions(t, ctx, testInfo.ResourcesDBClient(), subscriptionID, "resourceGroupName", clusterName, testConditions)

	for _, version := range []string{v2024, v2025} {
		t.Run(version, func(t *testing.T) {
			_, responseMap := getResourceResponse(t, ctx, testInfo, version, resourceID)
			props, ok := responseMap["properties"].(map[string]any)
			require.True(t, ok, "response should have properties")
			_, hasConditions := props["conditions"]
			require.False(t, hasConditions, "GET via %s should not include conditions", version)
		})
	}
}

func testNodePoolConditionsReturnedOnGET(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "cond-np-get"
	nodePoolName := "np01"
	resourceID := nodePoolResourceID(clusterName, nodePoolName)

	createClusterAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName)
	createNodePoolAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName, nodePoolName)
	injectNodePoolConditions(t, ctx, testInfo.ResourcesDBClient(), subscriptionID, "resourceGroupName", clusterName, nodePoolName, testConditions)

	_, responseMap := getResourceResponse(t, ctx, testInfo, v2026, resourceID)
	conditions := getConditionsFromResponse(t, responseMap)
	assertConditionsMatch(t, conditions)
}

func testNodePoolConditionsSurvivePUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "cond-np-put"
	nodePoolName := "np01"
	resourceID := nodePoolResourceID(clusterName, nodePoolName)

	createClusterAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName)
	createNodePoolAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName, nodePoolName)
	injectNodePoolConditions(t, ctx, testInfo.ResourcesDBClient(), subscriptionID, "resourceGroupName", clusterName, nodePoolName, testConditions)

	body, _ := getResourceResponse(t, ctx, testInfo, v2026, resourceID)

	v2026Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2026)
	require.NoError(t, v2026Accessor.CreateOrUpdate(ctx, resourceID, body))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, nodePoolName))

	_, afterMap := getResourceResponse(t, ctx, testInfo, v2026, resourceID)
	conditions := getConditionsFromResponse(t, afterMap)
	assertConditionsMatch(t, conditions)
}

func testExternalAuthConditionsReturnedOnGET(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "cond-ea-get"
	authName := "default"
	resourceID := externalAuthResourceID(clusterName, authName)

	createClusterAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName)
	createExternalAuthAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName, authName)
	injectExternalAuthConditions(t, ctx, testInfo.ResourcesDBClient(), subscriptionID, "resourceGroupName", clusterName, authName, testConditions)

	_, responseMap := getResourceResponse(t, ctx, testInfo, v2026, resourceID)
	conditions := getConditionsFromResponse(t, responseMap)
	assertConditionsMatch(t, conditions)
}

func testExternalAuthConditionsSurvivePUT(t *testing.T, testInfo *integrationutils.IntegrationTestInfo, subscriptionID string) {
	ctx := utils.ContextWithLogger(t.Context(), integrationutils.DefaultLogger(t))
	clusterName := "cond-ea-put"
	authName := "default"
	resourceID := externalAuthResourceID(clusterName, authName)

	createClusterAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName)
	createExternalAuthAndComplete(t, ctx, testInfo, v2026, subscriptionID, clusterName, authName)
	injectExternalAuthConditions(t, ctx, testInfo.ResourcesDBClient(), subscriptionID, "resourceGroupName", clusterName, authName, testConditions)

	body, _ := getResourceResponse(t, ctx, testInfo, v2026, resourceID)

	v2026Accessor := databasemutationhelpers.NewVersionedHTTPTestAccessor(testInfo.FrontendURL, v2026)
	require.NoError(t, v2026Accessor.CreateOrUpdate(ctx, resourceID, body))
	require.NoError(t, integrationutils.MarkOperationsCompleteForName(ctx, testInfo.ResourcesDBClient(), subscriptionID, authName))

	_, afterMap := getResourceResponse(t, ctx, testInfo, v2026, resourceID)
	conditions := getConditionsFromResponse(t, afterMap)
	assertConditionsMatch(t, conditions)
}
