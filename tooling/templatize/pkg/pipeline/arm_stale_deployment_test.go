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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
)

// TestPlanExistingDeployment covers the decision for every provisioning state ARM reports for a deployment found
// under the name a step is about to deploy to.
func TestPlanExistingDeployment(t *testing.T) {
	for _, testCase := range []struct {
		state         armresources.ProvisioningState
		firstLookup   bool
		allowRedeploy bool
		expected      existingDeploymentAction
	}{
		{state: armresources.ProvisioningStateSucceeded, firstLookup: true, allowRedeploy: true, expected: reuseExisting},
		{state: armresources.ProvisioningStateSucceeded, expected: reuseExisting},
		// Left over from an earlier attempt with identical inputs.
		{state: armresources.ProvisioningStateFailed, firstLookup: true, allowRedeploy: true, expected: redeployOverExisting},
		{state: armresources.ProvisioningStateCanceled, firstLookup: true, allowRedeploy: true, expected: redeployOverExisting},
		// Ended while we waited for it: a concurrent run of these inputs failing now.
		{state: armresources.ProvisioningStateFailed, allowRedeploy: true, expected: failOnExisting},
		{state: armresources.ProvisioningStateCanceled, allowRedeploy: true, expected: failOnExisting},
		// ARM reported the deployment active, so it is a concurrent run even if it has already failed.
		{state: armresources.ProvisioningStateFailed, firstLookup: true, expected: failOnExisting},
		{state: armresources.ProvisioningStateDeleting, firstLookup: true, allowRedeploy: true, expected: failOnExisting},
		{state: armresources.ProvisioningStateDeleted, firstLookup: true, allowRedeploy: true, expected: failOnExisting},
		{state: armresources.ProvisioningStateNotSpecified, firstLookup: true, allowRedeploy: true, expected: failOnExisting},
		{state: armresources.ProvisioningStateRunning, firstLookup: true, allowRedeploy: true, expected: waitForExisting},
		{state: armresources.ProvisioningStateAccepted, expected: waitForExisting},
		{state: "SomethingNew", firstLookup: true, allowRedeploy: true, expected: waitForExisting},
	} {
		t.Run(fmt.Sprintf("%s/firstLookup=%t/allowRedeploy=%t", testCase.state, testCase.firstLookup, testCase.allowRedeploy), func(t *testing.T) {
			assert.Equal(t, testCase.expected, planExistingDeployment(testCase.state, testCase.firstLookup, testCase.allowRedeploy))
		})
	}
}

// TestStaleFailedDeployment covers a deployment name poisoned by an earlier attempt: deployment names are a digest
// of the step inputs and the step cache only records successes, so when the first attempt of an earlier run failed
// and a retry (with a different digest) succeeded, a later run's first attempt finds the failed deployment again.
func TestStaleFailedDeployment(t *testing.T) {
	const running = `{"properties":{"provisioningState":"Running"}}`
	const failed = `{"properties":{"provisioningState":"Failed","error":{"code":"DeploymentFailed","message":"synthetic earlier failure"}}}`
	for _, scope := range []string{"ResourceGroup", "Subscription"} {
		for _, testCase := range []struct {
			name string
			// existing is the state of the deployment when it is first looked up.
			existing string
			// later, if set, is the state of the deployment on subsequent lookups.
			later string
			// conflict makes the PUT report that an identical deployment is already running.
			conflict  bool
			redeploys bool
			// redeployFails makes the redeployment itself fail.
			redeployFails bool
			errorText     string
		}{
			{name: "failed earlier", existing: failed, redeploys: true},
			// The redeployment's operations must stay available for diagnostics when it fails too.
			{name: "failed earlier, redeployment fails", existing: failed, redeployFails: true},
			// A deployment that fails while we wait for it is a concurrent run of these inputs failing now.
			{name: "failed while waiting", existing: running, later: failed, errorText: `failed to poll previously-existing deployment: pre-existing deployment failed: {"code":"DeploymentFailed","message":"synthetic earlier failure"}`},
			// The concurrent deployment we were told about failed: that is this run failing, not a leftover.
			{name: "concurrent failure", conflict: true, existing: failed, errorText: `failed to poll concurrent deployment: pre-existing deployment failed: {"code":"DeploymentFailed","message":"synthetic earlier failure"}`},
		} {
			t.Run(scope+"/"+testCase.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(logr.NewContext(t.Context(), logr.Discard()), 10*time.Second)
				defer cancel()
				var mu sync.Mutex
				var puts, lookups int
				transport := &fakeTransport{interceptor: func(request *http.Request) (*http.Response, error) {
					mu.Lock()
					defer mu.Unlock()
					status, body, header := http.StatusOK, "", http.Header{"Content-Type": {"application/json"}}
					switch {
					case strings.HasSuffix(request.URL.Path, "/operationStatuses/synthetic"):
						body = `{"status":"Succeeded"}`
						if testCase.redeployFails {
							body = `{"status":"Failed","error":{"code":"SyntheticRedeployFailure","message":"synthetic redeployment failure"}}`
						}
					case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/operations"):
						// the second operation is sparse, as failed operations may be
						body = `{"value":[{"operationId":"synthetic-operation","properties":{"provisioningState":"Failed","provisioningOperation":"Create","timestamp":"2025-01-01T00:00:00Z","duration":"PT1S"}},{"operationId":"sparse-operation","properties":{"provisioningState":"Failed"}}]}`
					case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/resourcegroups/rg"):
						body = `{"location":"westus3","tags":{}}`
					case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/deployments/"):
						lookups++
						switch {
						case puts > 0 && !testCase.conflict:
							// the final GET of the long-running operation, after our redeployment succeeded
							body = `{"properties":{"provisioningState":"Succeeded","outputs":{"result":{"type":"string","value":"synthetic output"}}}}`
						case testCase.conflict && lookups == 1:
							status, body = http.StatusNotFound, `{"error":{"code":"DeploymentNotFound"}}`
						case testCase.later != "" && lookups > 1:
							body = testCase.later
						default:
							body = testCase.existing
						}
					case request.Method == http.MethodPut && strings.Contains(request.URL.Path, "/deployments/"):
						puts++
						if testCase.conflict {
							status, body = http.StatusConflict, `{"error":{"code":"DeploymentActive","message":"synthetic concurrent deployment"}}`
							break
						}
						status, body = http.StatusCreated, `{"properties":{"provisioningState":"Accepted"}}`
						header.Set("Azure-AsyncOperation", (&url.URL{Scheme: request.URL.Scheme, Host: request.URL.Host, Path: request.URL.Path + "/operationStatuses/synthetic", RawQuery: "api-version=2021-04-01"}).String())
					default:
						return nil, fmt.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
					}
					return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				}}
				opts := &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}}}
				deployments, err := armresources.NewDeploymentsClient("subscription", &fakeCredential{}, opts)
				require.NoError(t, err)
				resourceGroups, err := armresources.NewResourceGroupsClient("subscription", &fakeCredential{}, opts)
				require.NoError(t, err)
				operations, err := armresources.NewDeploymentOperationsClient("subscription", &fakeCredential{}, opts)
				require.NoError(t, err)
				directory := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(directory, "params.bicepparam"), []byte("using none"), 0600))
				clientSide, serverSide := net.Pipe()
				server := jsonrpc2.NewConn(jsonrpc2.NewStream(serverSide))
				server.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, request jsonrpc2.Request) error {
					return reply(ctx, map[string]any{"success": true, "template": `{"resources":[]}`, "parameters": `{"parameters":{}}`}, nil)
				})
				t.Cleanup(func() { _ = clientSide.Close(); _ = server.Close() })
				client := &armClient{
					subscriptionID:      "subscription",
					deploymentClient:    deployments,
					getOperationsClient: func(string) (*armresources.DeploymentOperationsClient, error) { return operations, nil },
					resourceGroupClient: resourceGroups,
					bicepClient:         bicep.NewLSPClient(ctx, clientSide, false),

					existingDeploymentPollInterval: time.Millisecond,
				}
				step := &types.ARMStep{StepMeta: types.StepMeta{Name: "deployment"}, DeploymentLevel: scope, Parameters: "params.bicepparam"}
				cacheDir := t.TempDir()

				output, details, err := client.runArmStep(ctx, &StepRunOptions{BaseRunOptions: BaseRunOptions{NoPersist: true, DeploymentTimeoutSeconds: 30, StepCacheDir: cacheDir}, PipelineDirectory: directory}, "rg", graph.Identifier{}, step, NewExecutionState())

				mu.Lock()
				defer mu.Unlock()
				cached, readErr := os.ReadDir(cacheDir)
				require.NoError(t, readErr)
				if testCase.redeploys {
					require.NoError(t, err)
					assert.Equal(t, ArmOutput{"result": map[string]any{"type": "string", "value": "synthetic output"}}, output)
					assert.Equal(t, 1, puts, "the stale deployment must be deployed again under the same name")
					assert.Len(t, cached, 1, "the successful redeployment must be committed to the step cache")
					return
				}
				if testCase.redeployFails {
					require.ErrorContains(t, err, "failed to poll deployment:")
					assert.ErrorContains(t, err, "SyntheticRedeployFailure")
					assert.Nil(t, output)
					assert.Equal(t, 1, puts)
					assert.Empty(t, cached)
					require.NotNil(t, details, "the failed redeployment's operations must be available for diagnostics")
					mu.Unlock()
					result, detailsErr := details(ctx)
					mu.Lock()
					require.NoError(t, detailsErr)
					require.NotNil(t, result.ARM)
					assert.Len(t, result.ARM.Operations, 2)
					return
				}
				require.EqualError(t, err, testCase.errorText)
				assert.Nil(t, output)
				assert.Empty(t, cached)
				if testCase.conflict {
					assert.Equal(t, 1, puts)
				} else {
					assert.Zero(t, puts)
				}
			})
		}
	}
}
