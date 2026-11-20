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

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
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

const (
	reuseSubscription = "00000000-0000-0000-0000-00000000cafe"
	succeededBody     = `{"properties":{"provisioningState":"Succeeded","outputs":{"result":{"type":"string","value":"synthetic output"}}}}`
	failedBody        = `{"properties":{"provisioningState":"Failed","error":{"code":"SyntheticFailure","message":"synthetic failure"}}}`
	notFoundBody      = `{"error":{"code":"DeploymentNotFound"}}`
)

var syntheticOutput = ArmOutput{"result": map[string]any{"type": "string", "value": "synthetic output"}}

// deploymentsServer fakes ARM deployments: lookups by name are answered by get, and listing the scope's deployments
// returns the pages in list. It records the requests it sees.
type deploymentsServer struct {
	get  func(name string) (int, string)
	list [][]string // pages of deployment names, all listed as Succeeded

	mu       sync.Mutex
	lookups  []string // deployment names looked up, in order
	listings int
	filters  []string
	creates  int
}

func (s *deploymentsServer) transport() *fakeTransport {
	return &fakeTransport{interceptor: func(request *http.Request) (*http.Response, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		status, body := http.StatusOK, ""
		switch {
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/operations"):
			body = `{"value":[]}`
		case request.Method == http.MethodGet && strings.HasSuffix(strings.TrimSuffix(request.URL.Path, "/"), "/deployments"):
			s.listings++
			s.filters = append(s.filters, request.URL.Query().Get("$filter"))
			if s.list == nil {
				status, body = http.StatusForbidden, `{"error":{"code":"AuthorizationFailed","message":"synthetic denial for /subscriptions/`+reuseSubscription+`"}}`
				break
			}
			page := 0
			if raw := request.URL.Query().Get("page"); raw != "" {
				if _, err := fmt.Sscan(raw, &page); err != nil {
					return nil, err
				}
			}
			var value []map[string]any
			for _, name := range s.list[page] {
				value = append(value, map[string]any{"name": name, "properties": map[string]any{"provisioningState": "Succeeded"}})
			}
			response := map[string]any{"value": value}
			if page+1 < len(s.list) {
				next := *request.URL
				query := next.Query()
				query.Set("page", fmt.Sprint(page+1))
				next.RawQuery = query.Encode()
				response["nextLink"] = next.String()
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				return nil, err
			}
			body = string(encoded)
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/deployments/"):
			name := path.Base(request.URL.Path)
			s.lookups = append(s.lookups, name)
			status, body = s.get(name)
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/resourcegroups/rg"):
			body = `{"location":"westus3","tags":{}}`
		case request.Method == http.MethodPut && strings.Contains(request.URL.Path, "/deployments/"):
			s.creates++
			status, body = http.StatusConflict, `{"error":{"code":"DeploymentActive","message":"synthetic concurrent deployment"}}`
		default:
			return nil, fmt.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}}
}

func (s *deploymentsServer) clients(t *testing.T) (*armresources.DeploymentsClient, OperationsClientGetter, *armresources.ResourceGroupsClient) {
	opts := &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: s.transport(), Retry: policy.RetryOptions{MaxRetries: -1}}}
	deployments, err := armresources.NewDeploymentsClient(reuseSubscription, &fakeCredential{}, opts)
	require.NoError(t, err)
	operations, err := armresources.NewDeploymentOperationsClient(reuseSubscription, &fakeCredential{}, opts)
	require.NoError(t, err)
	resourceGroups, err := armresources.NewResourceGroupsClient(reuseSubscription, &fakeCredential{}, opts)
	require.NoError(t, err)
	return deployments, func(string) (*armresources.DeploymentOperationsClient, error) { return operations, nil }, resourceGroups
}

func TestFindSucceededAttempt(t *testing.T) {
	succeeded := func(string) (int, string) { return http.StatusOK, succeededBody }
	for _, scope := range []string{"ResourceGroup", "Subscription"} {
		for _, testCase := range []struct {
			name string
			list [][]string // nil makes listing fail
			get  func(name string) (int, string)
			// found names the deployment whose outputs are expected; empty means none is found.
			found   string
			lookups []string
		}{
			{
				name:    "the first attempt in attempt order wins, whatever the listing order",
				list:    [][]string{{"unrelated", "attempt-3", "attempt-1"}},
				get:     succeeded,
				found:   "attempt-1",
				lookups: []string{"attempt-1"},
			},
			{
				name:    "found on a later page",
				list:    [][]string{{"unrelated"}, {"attempt-2"}},
				get:     succeeded,
				found:   "attempt-2",
				lookups: []string{"attempt-2"},
			},
			{
				name: "no attempt listed",
				list: [][]string{{"unrelated"}},
				get:  succeeded,
			},
			{
				name: "listing fails",
				get:  succeeded,
			},
			{
				name: "an attempt that is no longer successful is passed over",
				list: [][]string{{"attempt-1", "attempt-2"}},
				get: func(name string) (int, string) {
					if name == "attempt-1" {
						return http.StatusOK, failedBody
					}
					return http.StatusOK, succeededBody
				},
				found:   "attempt-2",
				lookups: []string{"attempt-1", "attempt-2"},
			},
		} {
			t.Run(scope+"/"+testCase.name, func(t *testing.T) {
				var logs strings.Builder
				logger := funcr.New(func(prefix, args string) { logs.WriteString(args + "\n") }, funcr.Options{Verbosity: 10})
				server := &deploymentsServer{get: testCase.get, list: testCase.list}
				deployments, getOperations, _ := server.clients(t)

				name, output, found := findSucceededAttempt(t.Context(), logger, newDeploymentScope(deployments, getOperations, reuseSubscription, scope, "rg"), []string{"attempt-1", "attempt-2", "attempt-3"})

				assert.Equal(t, testCase.found != "", found)
				assert.Equal(t, testCase.found, name)
				if found {
					assert.Equal(t, syntheticOutput, output)
				}
				assert.Equal(t, testCase.lookups, server.lookups)
				assert.Equal(t, []string{"provisioningState eq 'Succeeded'"}, server.filters[:1], "only successful deployments are listed")
				assert.NotContains(t, logs.String(), reuseSubscription, "logs must not name the subscription")
			})
		}
	}
}

func TestReuseSucceededRetryAttemptAfterPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inputs := deploymentInputs{ResourceGroup: "rg", DeploymentLevel: "ResourceGroup"}
		ownName, _, err := stepDigest(inputs)
		require.NoError(t, err)
		retryInputs := inputs
		retryInputs.RetryAttempt = 1
		retryName, _, err := stepDigest(retryInputs)
		require.NoError(t, err)

		ownLookups := 0
		server := &deploymentsServer{list: [][]string{{retryName}}, get: func(name string) (int, string) {
			if name == ownName {
				ownLookups++
				if ownLookups == 1 {
					return http.StatusOK, `{"properties":{"provisioningState":"Running"}}`
				}
				return http.StatusOK, failedBody
			}
			return http.StatusOK, succeededBody
		}}
		deployments, getOperations, _ := server.clients(t)
		ctx := logr.NewContext(t.Context(), logr.Discard())
		exists, output, _, err := pollExistingDeploymentOrOtherAttempt(ctx, deployments, getOperations, reuseSubscription, 30, inputs.DeploymentLevel, inputs.ResourceGroup, ownName, true, inputs)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, syntheticOutput, output)
		assert.Equal(t, []string{ownName, ownName, retryName}, server.lookups)
	})
}

// TestReuseSucceededRetryAttemptStep runs an ARM step whose deployment had already failed while a retry of it
// succeeded, as the upgrade phase of an upgrade test finds a step it doesn't change.
func TestReuseSucceededRetryAttemptStep(t *testing.T) {
	for _, scope := range []string{"ResourceGroup", "Subscription"} {
		t.Run(scope, func(t *testing.T) {
			runStep := func(t *testing.T, retryAttempt int, cacheDir string, server *deploymentsServer) (Output, error) {
				ctx, cancel := context.WithTimeout(logr.NewContext(t.Context(), logr.Discard()), 5*time.Second)
				defer cancel()
				deployments, getOperations, resourceGroups := server.clients(t)
				directory := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(directory, "params.bicepparam"), []byte("using none"), 0600))
				clientSide, serverSide := net.Pipe()
				lsp := jsonrpc2.NewConn(jsonrpc2.NewStream(serverSide))
				lsp.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, request jsonrpc2.Request) error {
					return reply(ctx, map[string]any{"success": true, "template": `{"resources":[]}`, "parameters": `{"parameters":{}}`}, nil)
				})
				t.Cleanup(func() { _ = clientSide.Close(); _ = lsp.Close() })
				client := &armClient{subscriptionID: reuseSubscription, deploymentClient: deployments, getOperationsClient: getOperations, resourceGroupClient: resourceGroups, bicepClient: bicep.NewLSPClient(ctx, clientSide, false)}
				step := &types.ARMStep{StepMeta: types.StepMeta{Name: "deployment"}, DeploymentLevel: scope, Parameters: "params.bicepparam"}
				output, _, err := client.runArmStep(ctx, &StepRunOptions{BaseRunOptions: BaseRunOptions{NoPersist: true, DeploymentTimeoutSeconds: 30, StepCacheDir: cacheDir}, PipelineDirectory: directory, RetryAttempt: retryAttempt}, "rg", graph.Identifier{}, step, NewExecutionState())
				return output, err
			}

			// the deployment attempt 3 of the step makes, found succeeded by an earlier run's retry
			retry := &deploymentsServer{get: func(string) (int, string) { return http.StatusOK, succeededBody }}
			_, err := runStep(t, 3, t.TempDir(), retry)
			require.NoError(t, err)
			require.Len(t, retry.lookups, 1)
			retryName := retry.lookups[0]

			t.Run("already failed", func(t *testing.T) {
				var own string
				server := &deploymentsServer{list: [][]string{{"unrelated", retryName}}, get: func(name string) (int, string) {
					if own == "" || name == own {
						own = name
						return http.StatusOK, failedBody
					}
					return http.StatusOK, succeededBody
				}}
				cacheDir := t.TempDir()
				output, err := runStep(t, 0, cacheDir, server)
				require.NoError(t, err)
				assert.Equal(t, syntheticOutput, output)
				assert.Zero(t, server.creates, "nothing must be deployed")
				assert.Equal(t, []string{own, retryName}, server.lookups)
				cached, err := os.ReadDir(cacheDir)
				require.NoError(t, err)
				require.Len(t, cached, 1, "the reused success must be committed to the step cache")
				assert.Equal(t, own, cached[0].Name(), "under the step's own digest")
			})

			t.Run("failed after DeploymentActive conflict", func(t *testing.T) {
				// The 409 path polls directly and does not search for another attempt.
				lookups := 0
				server := &deploymentsServer{list: [][]string{{retryName}}, get: func(name string) (int, string) {
					lookups++
					if lookups == 1 {
						return http.StatusNotFound, notFoundBody
					}
					return http.StatusOK, failedBody
				}}
				_, err := runStep(t, 0, t.TempDir(), server)
				require.EqualError(t, err, `failed to poll concurrent deployment: pre-existing deployment failed: {"code":"SyntheticFailure","message":"synthetic failure"}`)
				assert.Zero(t, server.listings, "other attempts are not looked for")
			})
		})
	}
}
