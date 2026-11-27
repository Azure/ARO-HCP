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
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
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
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/junit"
)

// syntheticSubscription is a made-up subscription ID that must never show up in logs or artifacts.
const syntheticSubscription = "0f0e0d0c-0b0a-4909-8807-060504030201"
const syntheticModuleSubscription = "00000000-0000-0000-0000-00000000cafe"

// nestedFailureOperations serves the operations of a deployment that failed the way ARM reports a failure in a
// nested module: the top-level deployment only says DeploymentFailed, the module's deployment operation carries the
// module's own status as a JSON string inside a Conflict error, and the real cause sits on an operation inside the
// module. The shape mirrors a Network Security Perimeter association rejected by the resource provider, but all
// values are synthetic.
func nestedFailureOperations(t *testing.T) (top, module string) {
	t.Helper()
	moduleID := fmt.Sprintf("/subscriptions/%s/resourceGroups/rg/providers/Microsoft.Resources/deployments/module", syntheticModuleSubscription)
	associationID := fmt.Sprintf("/subscriptions/%s/resourceGroups/rg/providers/Microsoft.Network/networkSecurityPerimeters/nsp/resourceAssociations/association", syntheticModuleSubscription)
	failed := to.Ptr("Failed")
	wrapped := mustMarshal(t, map[string]any{"status": "Failed", "error": map[string]any{
		"code":    "ResourceDeploymentFailure",
		"message": "The resource write operation failed to complete successfully, because it reached terminal provisioning state 'Failed'.",
		"details": []any{map[string]any{
			"code":    "DeploymentFailed",
			"target":  moduleID,
			"message": "At least one resource deployment operation failed.",
			"details": []any{map[string]any{
				"code":    "AnmPreconditionFailedException",
				"target":  associationID,
				"message": "synthetic exclusive lock contention",
			}},
		}},
	}})
	top = fakeOperationsResponse(t,
		fakeDeploymentOp("Create", "PT10S", to.Ptr(fmt.Sprintf("/subscriptions/%s/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/vault", syntheticSubscription))),
		armresources.DeploymentOperation{Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperationCreate),
			ProvisioningState:     failed,
			StatusCode:            to.Ptr("Conflict"),
			Timestamp:             to.Ptr(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
			Duration:              to.Ptr("PT1M"),
			TargetResource:        &armresources.TargetResource{ID: to.Ptr(moduleID)},
			StatusMessage: &armresources.StatusMessage{Status: failed, Error: &armresources.ErrorResponse{
				Code:    to.Ptr("Conflict"),
				Message: to.Ptr(wrapped),
			}},
		}},
	)
	module = fakeOperationsResponse(t,
		armresources.DeploymentOperation{Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperationCreate),
			ProvisioningState:     failed,
			StatusCode:            to.Ptr("BadRequest"),
			Timestamp:             to.Ptr(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
			Duration:              to.Ptr("PT1S"),
			TargetResource:        &armresources.TargetResource{ID: to.Ptr(associationID)},
			StatusMessage: &armresources.StatusMessage{Status: failed, Error: &armresources.ErrorResponse{
				Code:    to.Ptr("ResourceDeploymentFailure"),
				Target:  to.Ptr(associationID),
				Message: to.Ptr("The resource write operation failed to complete successfully, because it reached terminal provisioning state 'Failed'."),
				Details: []*armresources.ErrorResponse{{
					Code:    to.Ptr("AnmPreconditionFailedException"),
					Message: to.Ptr(fmt.Sprintf("Error while processing request for association %s: synthetic exclusive lock contention in parent scope %s.", associationID, strings.ToUpper(syntheticSubscription))),
				}},
			}},
		}},
	)
	return top, module
}

func operationsTransport(t *testing.T, responses map[string]func() (int, string)) *fakeTransport {
	t.Helper()
	return &fakeTransport{interceptor: func(request *http.Request) (*http.Response, error) {
		for suffix, respond := range responses {
			if strings.HasSuffix(request.URL.Path, suffix) {
				status, body := respond()
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			}
		}
		return nil, fmt.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
	}}
}

func operationsGetter(t *testing.T, transport *fakeTransport) OperationsClientGetter {
	t.Helper()
	return func(subscriptionID string) (*armresources.DeploymentOperationsClient, error) {
		return armresources.NewDeploymentOperationsClient(subscriptionID, &fakeCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}}})
	}
}

func TestNestedDeploymentFailureCause(t *testing.T) {
	top, module := nestedFailureOperations(t)
	transport := operationsTransport(t, map[string]func() (int, string){
		"/deployments/deployment/operations": func() (int, string) { return http.StatusOK, top },
		"/deployments/module/operations":     func() (int, string) { return http.StatusOK, module },
	})
	details, err := DetermineOperationsForResourceGroupDeployment(operationsGetter(t, transport), syntheticSubscription, "rg", "deployment")(t.Context())
	require.NoError(t, err)
	require.NotNil(t, details)
	require.Len(t, details.ARM.Operations, 2)

	failed := FailedOperations(details.ARM.Operations)
	require.Len(t, failed, 1, "only the innermost failure is the cause; the module wrapper must not be reported separately")
	assert.Equal(t, []string{"module"}, failed[0].DeploymentPath)
	op := failed[0].Operation
	assert.Equal(t, "Failed", op.ProvisioningState)
	assert.Equal(t, "BadRequest", op.StatusCode)
	assert.Equal(t, "Microsoft.Network/networkSecurityPerimeters/resourceAssociations", op.Resource.ResourceType)
	require.NotNil(t, op.Error)
	assert.Equal(t, []string{"AnmPreconditionFailedException"}, op.Error.innermostCodes())
	assert.Contains(t, op.Error.String(), "synthetic exclusive lock contention")

	// Everything that ends up in published artifacts must be free of subscription IDs.
	serialized, err := yaml.Marshal(details)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(serialized)), syntheticSubscription)
	assert.NotContains(t, strings.ToLower(string(serialized)), syntheticModuleSubscription)
	assert.Contains(t, string(serialized), "/subscriptions/<redacted>/resourceGroups/rg/providers/Microsoft.Network/networkSecurityPerimeters/nsp")
	assert.Contains(t, string(serialized), "AnmPreconditionFailedException")
}

func TestOperationsPartialResults(t *testing.T) {
	top, module := nestedFailureOperations(t)
	withNextLink := func(page string) string {
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(page), &body))
		body["nextLink"] = "https://example.invalid/deployments/module/operations/page2"
		return mustMarshal(t, body)
	}
	for _, testCase := range []struct {
		name   string
		module map[string]func() (int, string)
		// reported are the resources whose failures are expected to be reported, in order.
		reported []string
	}{
		{
			name: "child deployment unreadable",
			module: map[string]func() (int, string){"/deployments/module/operations": func() (int, string) {
				return http.StatusForbidden, fmt.Sprintf(`{"error":{"code":"AuthorizationFailed","message":"synthetic denial for parent scope %s"}}`, syntheticSubscription)
			}},
			// Without the module's operations, the failed module itself is the best available explanation.
			reported: []string{"module"},
		},
		{
			name: "later page of a child deployment unreadable",
			module: map[string]func() (int, string){
				"/deployments/module/operations": func() (int, string) { return http.StatusOK, withNextLink(module) },
				"/deployments/module/operations/page2": func() (int, string) {
					return http.StatusInternalServerError, `{"error":{"code":"InternalServerError"}}`
				},
			},
			// The failure on the first page explains only what was fetched; the module's error may cover the rest.
			reported: []string{"module", "association"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			responses := map[string]func() (int, string){"/deployments/deployment/operations": func() (int, string) { return http.StatusOK, top }}
			maps.Copy(responses, testCase.module)
			details, err := DetermineOperationsForResourceGroupDeployment(operationsGetter(t, operationsTransport(t, responses)), syntheticSubscription, "rg", "deployment")(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to fetch operations for child deployment rg/module")
			assert.NotContains(t, strings.ToLower(err.Error()), syntheticSubscription)
			assert.NotContains(t, strings.ToLower(err.Error()), syntheticModuleSubscription)
			var responseErr *azcore.ResponseError
			require.ErrorAs(t, err, &responseErr)
			require.NotNil(t, details, "operations fetched before the failure must be kept")
			require.Len(t, details.ARM.Operations, 2)
			failed := FailedOperations(details.ARM.Operations)
			var reported []string
			for _, f := range failed {
				reported = append(reported, f.Operation.Resource.Name)
			}
			require.Equal(t, testCase.reported, reported)
			// The module's error wraps its status as a JSON string, which keeps the cause readable in the message.
			assert.Equal(t, []string{"Conflict"}, failed[0].Operation.Error.innermostCodes())
			assert.Contains(t, failed[0].Operation.Error.String(), "AnmPreconditionFailedException")
			assert.Contains(t, failed[0].Operation.Error.String(), "synthetic exclusive lock contention")
			assert.NotContains(t, strings.ToLower(failed[0].Operation.Error.String()), syntheticSubscription)
		})
	}
}

// TestIncompleteNestedDeploymentKeepsNearestError fetches outer -> inner -> a partially readable page: only the
// inner deployment is incomplete, so its error, not the outer wrapper's, is kept to explain what's missing.
func TestIncompleteNestedDeploymentKeepsNearestError(t *testing.T) {
	failedOp := func(resourceType, name, code string) armresources.DeploymentOperation {
		return armresources.DeploymentOperation{Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: to.Ptr(armresources.ProvisioningOperationCreate),
			ProvisioningState:     to.Ptr("Failed"),
			Timestamp:             to.Ptr(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
			Duration:              to.Ptr("PT1S"),
			TargetResource:        &armresources.TargetResource{ID: to.Ptr(fmt.Sprintf("/subscriptions/%s/resourceGroups/rg/providers/%s/%s", syntheticSubscription, resourceType, name))},
			StatusMessage:         &armresources.StatusMessage{Error: &armresources.ErrorResponse{Code: to.Ptr(code)}},
		}}
	}
	var firstPage map[string]any
	require.NoError(t, json.Unmarshal([]byte(fakeOperationsResponse(t, failedOp("Microsoft.Synthetic/things", "thing", "SyntheticCause"))), &firstPage))
	firstPage["nextLink"] = "https://example.invalid/deployments/inner/operations/page2"
	transport := operationsTransport(t, map[string]func() (int, string){
		"/deployments/deployment/operations": func() (int, string) {
			return http.StatusOK, fakeOperationsResponse(t, failedOp("Microsoft.Resources/deployments", "outer", "OuterWrapper"))
		},
		"/deployments/outer/operations": func() (int, string) {
			return http.StatusOK, fakeOperationsResponse(t, failedOp("Microsoft.Resources/deployments", "inner", "InnerWrapper"))
		},
		"/deployments/inner/operations": func() (int, string) { return http.StatusOK, mustMarshal(t, firstPage) },
		"/deployments/inner/operations/page2": func() (int, string) {
			return http.StatusInternalServerError, `{"error":{"code":"InternalServerError"}}`
		},
	})
	details, err := DetermineOperationsForResourceGroupDeployment(operationsGetter(t, transport), syntheticSubscription, "rg", "deployment")(t.Context())
	require.Error(t, err)
	require.NotNil(t, details)
	var reported []string
	for _, f := range FailedOperations(details.ARM.Operations) {
		reported = append(reported, strings.Join(append(f.DeploymentPath, f.Operation.Resource.Name), "/"))
	}
	assert.Equal(t, []string{"outer/inner", "outer/inner/thing"}, reported)
}

// TestSkippedNestedDeploymentKeepsOuterError cancels the fetch after outer's first nested deployment is fetched: the
// second, whose state is unknown, is skipped, so the failure explained beneath the first must not hide outer's error.
func TestSkippedNestedDeploymentKeepsOuterError(t *testing.T) {
	const otherSubscription = "00000000-0000-0000-0000-00000000beef"
	op := func(subscription, resourceType, name, state, code string) armresources.DeploymentOperation {
		operation := fakeDeploymentOp("Create", "PT1S", to.Ptr(fmt.Sprintf("/subscriptions/%s/resourceGroups/rg/providers/%s/%s", subscription, resourceType, name)))
		if state != "" {
			operation.Properties.ProvisioningState = to.Ptr(state)
		}
		if code != "" {
			operation.Properties.StatusMessage = &armresources.StatusMessage{Error: &armresources.ErrorResponse{Code: to.Ptr(code)}}
		}
		return operation
	}
	transport := operationsTransport(t, map[string]func() (int, string){
		"/deployments/deployment/operations": func() (int, string) {
			return http.StatusOK, fakeOperationsResponse(t, op(syntheticSubscription, "Microsoft.Resources/deployments", "outer", "Failed", "OuterError"))
		},
		"/deployments/outer/operations": func() (int, string) {
			return http.StatusOK, fakeOperationsResponse(t,
				op(syntheticSubscription, "Microsoft.Resources/deployments", "first", "Failed", "FirstWrapper"),
				op(syntheticSubscription, "Microsoft.Resources/deployments", "sparse", "", ""))
		},
		"/deployments/first/operations": func() (int, string) {
			return http.StatusOK, fakeOperationsResponse(t,
				op(syntheticSubscription, "Microsoft.Synthetic/things", "thing", "Failed", "SyntheticCause"),
				op(otherSubscription, "Microsoft.Resources/deployments", "elsewhere", "Succeeded", ""))
		},
	})
	// The SDK fails a request whose context is cancelled while it is in flight, so cancel between requests instead:
	// when the last nested deployment beneath the first one asks for its client.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	getClient := func(subscription string) (*armresources.DeploymentOperationsClient, error) {
		if subscription == otherSubscription {
			cancel()
			return nil, errors.New("synthetic client failure")
		}
		return operationsGetter(t, transport)(subscription)
	}
	details, err := DetermineOperationsForResourceGroupDeployment(getClient, syntheticSubscription, "rg", "deployment")(ctx)
	require.ErrorContains(t, err, "skipped fetching operations for remaining child deployments")
	require.NotNil(t, details)
	var reported []string
	for _, f := range FailedOperations(details.ARM.Operations) {
		reported = append(reported, strings.Join(append(f.DeploymentPath, f.Operation.Resource.Name), "/"))
	}
	assert.Equal(t, []string{"outer", "outer/first/thing"}, reported)
}

// TestFailedDeploymentCauseInStepLog runs a step whose deployment failed with only a generic DeploymentFailed, as
// both a fresh and a pre-existing deployment do, and checks that the cause reaches the logs and the JUnit report.
func TestFailedDeploymentCauseInStepLog(t *testing.T) {
	t.Setenv("COMPRESS_TIMING_METADATA", "false")
	top, module := nestedFailureOperations(t)
	transport := operationsTransport(t, map[string]func() (int, string){
		"/deployments/deployment/operations": func() (int, string) { return http.StatusOK, top },
		"/deployments/module/operations":     func() (int, string) { return http.StatusOK, module },
	})
	var logs lockedBuffer
	logger := testr.NewWithInterface(&bufferT{T: t, out: &logs}, testr.Options{})
	ctx := logr.NewContext(t.Context(), logger)
	pipeline := &types.Pipeline{ServiceGroup: "test", ResourceGroups: []*types.ResourceGroup{{
		ResourceGroupMeta: &types.ResourceGroupMeta{Name: "rg", ResourceGroup: "rg", Subscription: "subscription"},
		Steps:             []types.Step{&types.ARMStep{StepMeta: types.StepMeta{Name: "deployment"}}},
	}}}
	outputDir := t.TempDir()
	timingFile, junitFile := filepath.Join(outputDir, "timing.yaml"), filepath.Join(outputDir, "junit.xml")
	executor := func(graph.Identifier, types.Step, context.Context, ExecutionTarget, *StepRunOptions, *ExecutionState) (Output, DetailsProducer, error) {
		return nil, DetermineOperationsForResourceGroupDeployment(operationsGetter(t, transport), syntheticSubscription, "rg", "deployment"),
			errors.New(`pre-existing deployment failed: {"code":"DeploymentFailed","message":"At least one resource deployment operation failed."}`)
	}
	_, err := RunPipeline(&topology.Service{ServiceGroup: "test"}, pipeline, ctx, &PipelineRunOptions{
		TimingOutputFile:      timingFile,
		JUnitOutputFile:       junitFile,
		Concurrency:           1,
		SubsciptionLookupFunc: func(context.Context, string) (string, error) { return "subscription", nil },
		TopoDirLookupFunc:     func(string) (string, error) { return ".", nil },
	}, executor)
	require.Error(t, err)

	report, err := os.ReadFile(junitFile)
	require.NoError(t, err)
	var suites junit.TestSuites
	require.NoError(t, xml.Unmarshal(report, &suites))
	require.Len(t, suites.Suites, 1)
	require.Len(t, suites.Suites[0].TestCases, 1)
	require.NotNil(t, suites.Suites[0].TestCases[0].FailureOutput)
	timing, err := os.ReadFile(timingFile)
	require.NoError(t, err)
	for name, output := range map[string]string{
		"JUnit":      suites.Suites[0].TestCases[0].FailureOutput.Output,
		"run logger": string(logs.Snapshot()),
	} {
		assert.Contains(t, output, "ARM deployment operation failed.", name)
		assert.Contains(t, output, "AnmPreconditionFailedException", name)
		assert.Contains(t, output, "synthetic exclusive lock contention in parent scope <redacted>", name)
		assert.Contains(t, output, "networkSecurityPerimeters/resourceAssociations", name)
		assert.NotContains(t, strings.ToLower(output), syntheticSubscription, name)
		assert.NotContains(t, strings.ToLower(output), syntheticModuleSubscription, name)
	}
	assert.Contains(t, string(timing), "AnmPreconditionFailedException")
	assert.NotContains(t, strings.ToLower(string(timing)), syntheticSubscription)
	assert.NotContains(t, strings.ToLower(string(timing)), syntheticModuleSubscription)
}

// bufferT captures testr output so the test can inspect what the run logger printed.
type bufferT struct {
	*testing.T
	out *lockedBuffer
}

func (b *bufferT) Log(args ...any) {
	_, _ = fmt.Fprintln(b.out, args...)
	b.T.Log(args...)
}

// TestFreshDeploymentPollFailureKeepsDetails covers the common path: a deployment created by this run fails while
// it is polled. Its operations must still be fetched, or the failure is reported as a bare DeploymentFailed.
func TestFreshDeploymentPollFailureKeepsDetails(t *testing.T) {
	for _, scope := range []string{"ResourceGroup", "Subscription"} {
		t.Run(scope, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(logr.NewContext(t.Context(), logr.Discard()), 10*time.Second)
			defer cancel()
			var mu sync.Mutex
			operations := 0
			transport := &fakeTransport{interceptor: func(request *http.Request) (*http.Response, error) {
				mu.Lock()
				defer mu.Unlock()
				status, body, header := http.StatusOK, "", http.Header{"Content-Type": {"application/json"}}
				switch {
				case strings.HasSuffix(request.URL.Path, "/operationStatuses/synthetic"):
					body = `{"status":"Failed","error":{"code":"DeploymentFailed","message":"At least one resource deployment operation failed."}}`
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/operations"):
					operations++
					body = `{"value":[{"operationId":"synthetic","properties":{"provisioningState":"Failed","provisioningOperation":"Create","timestamp":"2025-01-01T00:00:00Z","duration":"PT1S","statusMessage":{"error":{"code":"SyntheticCause","message":"synthetic cause"}}}}]}`
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/resourcegroups/rg"):
					body = `{"location":"westus3","tags":{}}`
				case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/deployments/"):
					status, body = http.StatusNotFound, `{"error":{"code":"DeploymentNotFound"}}`
				case request.Method == http.MethodPut && strings.Contains(request.URL.Path, "/deployments/"):
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
			directory := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(directory, "params.bicepparam"), []byte("using none"), 0600))
			clientSide, serverSide := net.Pipe()
			server := jsonrpc2.NewConn(jsonrpc2.NewStream(serverSide))
			server.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, request jsonrpc2.Request) error {
				return reply(ctx, map[string]any{"success": true, "template": `{"resources":[]}`, "parameters": `{"parameters":{}}`}, nil)
			})
			t.Cleanup(func() { _ = clientSide.Close(); _ = server.Close() })
			client := &armClient{subscriptionID: "subscription", deploymentClient: deployments, getOperationsClient: operationsGetter(t, transport), resourceGroupClient: resourceGroups, bicepClient: bicep.NewLSPClient(ctx, clientSide, false)}
			step := &types.ARMStep{StepMeta: types.StepMeta{Name: "deployment"}, DeploymentLevel: scope, Parameters: "params.bicepparam"}

			output, details, err := client.runArmStep(ctx, &StepRunOptions{BaseRunOptions: BaseRunOptions{NoPersist: true, DeploymentTimeoutSeconds: 30}, PipelineDirectory: directory}, "rg", graph.Identifier{}, step, NewExecutionState())
			require.Error(t, err)
			assert.Nil(t, output)
			assert.ErrorContains(t, err, "failed to poll deployment: ")
			require.NotNil(t, details, "the operations explain the failure, so they must be fetched on this path, too")
			result, err := details(ctx)
			require.NoError(t, err)
			failed := FailedOperations(result.ARM.Operations)
			require.Len(t, failed, 1)
			assert.Equal(t, []string{"SyntheticCause"}, failed[0].Operation.Error.innermostCodes())
			assert.Equal(t, 1, operations)
		})
	}
}

func TestFailedOperationsSelection(t *testing.T) {
	cause := &OperationError{Code: "Wrapper", Details: []OperationError{{Code: "SyntheticCause", Message: "synthetic cause"}}}
	deployment := func(name string) *Resource {
		return &Resource{ResourceType: "Microsoft.Resources/deployments", Name: name}
	}
	leaf := &Resource{ResourceType: "Microsoft.Synthetic/things", Name: "thing"}
	for _, testCase := range []struct {
		name     string
		ops      []Operation
		expected []string // "path:resource:codes"
	}{
		{
			name:     "nested cause wins over wrappers",
			ops:      []Operation{{ProvisioningState: "Failed", Error: &OperationError{Code: "DeploymentFailed"}, Resource: deployment("module"), Children: []Operation{{ProvisioningState: "Failed", Error: cause, Resource: leaf}}}},
			expected: []string{"module:thing:SyntheticCause"},
		},
		{
			name:     "sparse parent does not hide its children",
			ops:      []Operation{{Resource: deployment("module"), Children: []Operation{{ProvisioningState: "Failed", Error: cause, Resource: leaf}}}},
			expected: []string{"module:thing:SyntheticCause"},
		},
		{
			name:     "children without errors do not hide the parent's error",
			ops:      []Operation{{ProvisioningState: "Failed", Error: cause, Resource: deployment("module"), Children: []Operation{{ProvisioningState: "Failed", Resource: leaf}}}},
			expected: []string{":module:SyntheticCause", "module:thing:"},
		},
		{
			name: "one explained child does not hide the parent's error for another",
			ops: []Operation{{ProvisioningState: "Failed", Error: cause, Resource: deployment("module"), Children: []Operation{
				{ProvisioningState: "Failed", Error: &OperationError{Code: "OtherCause"}, Resource: &Resource{Name: "explained"}},
				{ProvisioningState: "Failed", Resource: leaf},
			}}},
			expected: []string{":module:SyntheticCause", "module:explained:OtherCause", "module:thing:"},
		},
		{
			name: "explained children do not hide the parent's error when not all of them were fetched",
			ops: []Operation{{ProvisioningState: "Failed", Error: cause, Resource: deployment("module"), ChildrenIncomplete: true, Children: []Operation{
				{ProvisioningState: "Failed", Error: &OperationError{Code: "OtherCause"}, Resource: leaf},
			}}},
			expected: []string{":module:SyntheticCause", "module:thing:OtherCause"},
		},
		{
			name: "an incompletely fetched bare failure keeps the outer error",
			ops: []Operation{{ProvisioningState: "Failed", Error: cause, Resource: deployment("outer"), Children: []Operation{
				{ProvisioningState: "Failed", Resource: deployment("inner"), ChildrenIncomplete: true, Children: []Operation{
					{ProvisioningState: "Failed", Error: &OperationError{Code: "OtherCause"}, Resource: leaf},
				}},
			}}},
			expected: []string{":outer:SyntheticCause", "outer/inner:thing:OtherCause"},
		},
		{
			name: "an incompletely fetched sparse deployment keeps the outer error",
			ops: []Operation{{ProvisioningState: "Failed", Error: cause, Resource: deployment("outer"), Children: []Operation{
				{Resource: deployment("inner"), ChildrenIncomplete: true, Children: []Operation{
					{ProvisioningState: "Failed", Error: &OperationError{Code: "OtherCause"}, Resource: leaf},
				}},
			}}},
			expected: []string{":outer:SyntheticCause", "outer/inner:thing:OtherCause"},
		},
		{
			name: "an incompletely fetched deployment that succeeded does not keep the outer error",
			ops: []Operation{{ProvisioningState: "Failed", Error: cause, Resource: deployment("outer"), Children: []Operation{
				{ProvisioningState: "Succeeded", Resource: deployment("inner"), ChildrenIncomplete: true},
				{ProvisioningState: "Failed", Error: &OperationError{Code: "OtherCause"}, Resource: leaf},
			}}},
			expected: []string{"outer:thing:OtherCause"},
		},
		{
			name: "an inner error explains bare failures beneath it for all ancestors",
			ops: []Operation{{ProvisioningState: "Failed", Error: &OperationError{Code: "DeploymentFailed"}, Resource: deployment("outer"), Children: []Operation{
				{ProvisioningState: "Failed", Error: cause, Resource: deployment("inner"), Children: []Operation{{ProvisioningState: "Failed", Resource: leaf}}},
			}}},
			expected: []string{"outer:inner:SyntheticCause", "outer/inner:thing:"},
		},
		{
			name: "an unexplained sibling branch keeps the outer error",
			ops: []Operation{{ProvisioningState: "Failed", Error: &OperationError{Code: "DeploymentFailed"}, Resource: deployment("outer"), Children: []Operation{
				{ProvisioningState: "Failed", Error: cause, Resource: deployment("inner"), Children: []Operation{{ProvisioningState: "Failed", Resource: leaf}}},
				{ProvisioningState: "Failed", Resource: deployment("sibling"), Children: []Operation{{ProvisioningState: "Failed", Resource: &Resource{Name: "other"}}}},
			}}},
			expected: []string{":outer:DeploymentFailed", "outer:inner:SyntheticCause", "outer/inner:thing:", "outer/sibling:other:"},
		},
		{
			name: "an explained sibling after an unexplained one does not clear it",
			ops: []Operation{{ProvisioningState: "Failed", Error: &OperationError{Code: "DeploymentFailed"}, Resource: deployment("outer"), Children: []Operation{
				{ProvisioningState: "Failed", Resource: deployment("sibling"), Children: []Operation{{ProvisioningState: "Failed", Resource: &Resource{Name: "other"}}}},
				{ProvisioningState: "Failed", Error: cause, Resource: deployment("inner"), Children: []Operation{{ProvisioningState: "Failed", Resource: leaf}}},
			}}},
			expected: []string{":outer:DeploymentFailed", "outer/sibling:other:", "outer:inner:SyntheticCause", "outer/inner:thing:"},
		},
		{
			name: "several failed top-level operations",
			ops: []Operation{
				{ProvisioningState: "Failed", Error: &OperationError{Code: "FirstCause"}, Resource: &Resource{Name: "first"}},
				{ProvisioningState: "Succeeded", Resource: &Resource{Name: "fine"}},
				{ProvisioningState: "Failed", Error: &OperationError{Code: "SecondCause"}, Resource: &Resource{Name: "second"}},
			},
			expected: []string{":first:FirstCause", ":second:SecondCause"},
		},
		{
			name: "an unexplained failure beneath an unknown state keeps the outer error",
			ops: []Operation{{ProvisioningState: "Failed", Error: &OperationError{Code: "DeploymentFailed"}, Resource: deployment("outer"), Children: []Operation{
				{Resource: deployment("middle"), Children: []Operation{{ProvisioningState: "Failed", Resource: leaf}}},
			}}},
			expected: []string{":outer:DeploymentFailed", "outer/middle:thing:"},
		},
		{
			name: "a recovered deployment does not hide the parent's failure",
			ops: []Operation{{ProvisioningState: "Failed", Error: cause, Resource: deployment("outer"), Children: []Operation{
				{ProvisioningState: "Succeeded", Resource: deployment("middle"), Children: []Operation{
					{ProvisioningState: "Failed", Error: &OperationError{Code: "EarlierFailure"}, Resource: leaf},
				}},
			}}},
			expected: []string{":outer:SyntheticCause"},
		},
		{
			name:     "operations without a resource",
			ops:      []Operation{{ProvisioningState: "Failed", Children: []Operation{{ProvisioningState: "Failed", Error: cause}}}},
			expected: []string{"::SyntheticCause"},
		},
		{
			name:     "failed state without any error is still reported",
			ops:      []Operation{{ProvisioningState: "Failed", Resource: leaf}},
			expected: []string{":thing:"},
		},
		{
			name: "successful operations are not reported",
			ops:  []Operation{{ProvisioningState: "Succeeded", Resource: leaf}, {}},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var got []string
			for _, failed := range FailedOperations(testCase.ops) {
				var codes []string
				if failed.Operation.Error != nil {
					codes = failed.Operation.Error.innermostCodes()
				}
				var name string
				if failed.Operation.Resource != nil {
					name = failed.Operation.Resource.Name
				}
				got = append(got, strings.Join(failed.DeploymentPath, "/")+":"+name+":"+strings.Join(codes, ","))
			}
			assert.Equal(t, testCase.expected, got)
		})
	}
}

func TestOperationErrorRedaction(t *testing.T) {
	other := "11111111-2222-4333-8444-555555555555"
	for _, testCase := range []struct {
		name, in, out string
	}{
		{name: "resource id", in: "/subscriptions/" + other + "/resourceGroups/rg", out: "/subscriptions/<redacted>/resourceGroups/rg"},
		{name: "upper-case resource id", in: "/SUBSCRIPTIONS/" + strings.ToUpper(other) + "/rg", out: "/SUBSCRIPTIONS/<redacted>/rg"},
		{name: "URL-encoded resource id", in: "%2Fsubscriptions%2F" + other + "%2Frg", out: "%2Fsubscriptions%2F<redacted>%2Frg"},
		{name: "JSON-escaped resource id", in: `\/subscriptions\/` + other + `\/rg`, out: `\/subscriptions\/<redacted>\/rg`},
		{name: "other subscription in prose", in: "access denied to subscription '" + other + "'", out: "access denied to subscription '<redacted>'"},
		{name: "other subscription in JSON", in: `"subscriptionId":"` + other + `"`, out: `"subscriptionId":"<redacted>"`},
		{name: "known id in prose", in: "quota exceeded in " + syntheticSubscription, out: "quota exceeded in <redacted>"},
		{name: "known id upper-case", in: "in " + strings.ToUpper(syntheticSubscription), out: "in <redacted>"},
		{name: "known id mixed-case", in: "in 0F0e0D0c-0B0a-4909-8807-060504030201", out: "in <redacted>"},
		{name: "unrelated ids kept", in: "Correlation ID: " + other, out: "Correlation ID: " + other},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.out, redactSubscriptionIDs(testCase.in, []string{syntheticSubscription}))
		})
	}

	// Deeply nested (or self-similar) error details are cut off rather than recursed into without bound.
	var deep *armresources.ErrorResponse
	for i := 0; i < 3*maxOperationErrorDepth; i++ {
		deep = &armresources.ErrorResponse{Code: to.Ptr(fmt.Sprintf("Level%d", i)), Details: []*armresources.ErrorResponse{deep}}
	}
	rendered, err := json.Marshal(operationErrorFor(deep, nil))
	require.NoError(t, err)
	assert.Equal(t, maxOperationErrorDepth+1, strings.Count(string(rendered), `"code"`))
}
