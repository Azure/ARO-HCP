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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
)

func TestExistingDeploymentDiagnostics(t *testing.T) {
	lookupErr := errors.New("synthetic lookup failure")
	cases := []struct {
		name      string
		body      string
		status    int
		lookupErr error
		timeout   bool
		errorText string
		noDetails bool
	}{
		{name: "failed", body: `{"properties":{"provisioningState":"Failed","error":{"code":"SyntheticFailure","message":"synthetic failure"}}}`, errorText: `pre-existing deployment failed: {"code":"SyntheticFailure","message":"synthetic failure"}`},
		{name: "canceled", body: `{"properties":{"provisioningState":"Canceled"}}`, errorText: "pre-existing deployment: provisioning state is Canceled"},
		{name: "deleted", body: `{"properties":{"provisioningState":"Deleted"}}`, errorText: "pre-existing deployment: provisioning state is Deleted"},
		{name: "deleting", body: `{"properties":{"provisioningState":"Deleting"}}`, errorText: "pre-existing deployment: provisioning state is Deleting"},
		{name: "unspecified", body: `{"properties":{"provisioningState":"NotSpecified"}}`, errorText: "pre-existing deployment: provisioning state is NotSpecified"},
		{name: "missing state", body: `{"properties":{}}`, errorText: "failed to look up pre-existing deployment: found a nil provisioning state"},
		{name: "missing properties", body: `{}`, errorText: "failed to look up pre-existing deployment: found a nil provisioning state"},
		{name: "lookup error", lookupErr: lookupErr, errorText: "failed to look up pre-existing deployment: synthetic lookup failure"},
		{name: "canceled lookup", lookupErr: context.Canceled, errorText: "failed to look up pre-existing deployment: context canceled"},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":{"code":"AuthorizationFailed","message":"synthetic denial"}}`, errorText: "failed to look up pre-existing deployment:"},
		{name: "timeout", body: `{"properties":{"provisioningState":"Running"}}`, timeout: true, errorText: "timed out waiting for pre-existing deployment to finish"},
		{name: "not found", status: http.StatusNotFound, body: `{"error":{"code":"DeploymentNotFound"}}`, noDetails: true},
		{name: "succeeded", body: `{"properties":{"provisioningState":"Succeeded","outputs":{"result":{"type":"string","value":"synthetic output"}}}}`},
	}
	for _, scope := range []string{"ResourceGroup", "Subscription"} {
		for _, test := range cases {
			for _, layer := range []string{"helper", "caller", "concurrent caller"} {
				if test.noDetails && layer != "helper" {
					continue
				}
				t.Run(scope+"/"+test.name+"/"+layer, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(logr.NewContext(t.Context(), logr.Discard()), 5*time.Second)
					defer cancel()
					var deploymentPath string
					var lookups, operations, creates int
					transport := &fakeTransport{interceptor: func(request *http.Request) (*http.Response, error) {
						status, body := http.StatusOK, test.body
						switch {
						case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/operations"):
							operationsPath := deploymentPath + "/operations"
							if scope == "ResourceGroup" {
								operationsPath = strings.Replace(operationsPath, "/providers/Microsoft.Resources", "", 1)
							}
							assert.Equal(t, operationsPath, request.URL.Path)
							assert.NoError(t, request.Context().Err())
							operations++
							body = `{"value":[{"operationId":"synthetic-operation","properties":{"provisioningState":"Failed","provisioningOperation":"Create","timestamp":"2025-01-01T00:00:00Z","duration":"PT1S"}}]}`
						case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/deployments/"):
							deploymentPath = request.URL.Path
							assert.Equal(t, scope == "ResourceGroup", strings.Contains(deploymentPath, "/resourcegroups/"))
							lookups++
							if layer == "concurrent caller" && lookups == 1 {
								status, body = http.StatusNotFound, `{"error":{"code":"DeploymentNotFound"}}`
							} else {
								if test.lookupErr != nil {
									return nil, test.lookupErr
								}
								if test.status != 0 {
									status = test.status
								}
							}
						case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/resourcegroups/rg"):
							body = `{"location":"westus3","tags":{}}`
						case request.Method == http.MethodPut && layer == "concurrent caller" && strings.Contains(request.URL.Path, "/deployments/"):
							creates++
							status, body = http.StatusConflict, `{"error":{"code":"DeploymentActive","message":"synthetic concurrent deployment"}}`
						default:
							return nil, fmt.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
						}
						return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
					}}
					opts := &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}}}
					deployments, err := armresources.NewDeploymentsClient("subscription", &fakeCredential{}, opts)
					require.NoError(t, err)
					operationsClient, err := armresources.NewDeploymentOperationsClient("subscription", &fakeCredential{}, opts)
					require.NoError(t, err)
					getOperations := func(subscription string) (*armresources.DeploymentOperationsClient, error) {
						assert.Equal(t, "subscription", subscription)
						return operationsClient, nil
					}
					timeout := 30
					if test.timeout {
						timeout = 0
					}
					var details DetailsProducer
					var output Output
					prefix := ""
					if layer == "helper" {
						var exists bool
						var armOutput ArmOutput
						exists, armOutput, details, err = pollAndGetOutputFromExistingDeployment(ctx, deployments, getOperations, "subscription", timeout, scope, "rg", "deployment")
						output = armOutput
						assert.Equal(t, test.errorText == "" && !test.noDetails, exists)
					} else {
						directory := t.TempDir()
						require.NoError(t, os.WriteFile(filepath.Join(directory, "params.bicepparam"), []byte("using none"), 0600))
						clientSide, serverSide := net.Pipe()
						server := jsonrpc2.NewConn(jsonrpc2.NewStream(serverSide))
						server.Go(ctx, func(ctx context.Context, reply jsonrpc2.Replier, request jsonrpc2.Request) error {
							assert.Equal(t, "bicep/compileParams", request.Method())
							return reply(ctx, map[string]any{"success": true, "template": `{"resources":[]}`, "parameters": `{"parameters":{}}`}, nil)
						})
						t.Cleanup(func() { _ = clientSide.Close(); _ = server.Close() })
						resourceGroups, clientErr := armresources.NewResourceGroupsClient("subscription", &fakeCredential{}, opts)
						require.NoError(t, clientErr)
						client := &armClient{subscriptionID: "subscription", deploymentClient: deployments, getOperationsClient: getOperations, resourceGroupClient: resourceGroups, bicepClient: bicep.NewLSPClient(ctx, clientSide, false)}
						step := &types.ARMStep{StepMeta: types.StepMeta{Name: "deployment"}, DeploymentLevel: scope, Parameters: "params.bicepparam"}
						output, details, err = runStep(graph.Identifier{}, step, ctx, &executionTargetImpl{resourceGroup: "rg"}, &StepRunOptions{BaseRunOptions: BaseRunOptions{NoPersist: true, DeploymentTimeoutSeconds: timeout}, PipelineDirectory: directory}, NewExecutionState(), func(string, string, *bicep.LSPClient) (*armClient, error) { return client, nil })
						prefix = "failed to run ARM step: failed to poll previously-existing deployment: "
						if layer == "concurrent caller" {
							prefix = "failed to run ARM step: failed to poll concurrent deployment: "
							assert.Equal(t, 1, creates)
						}
					}
					if test.errorText != "" {
						require.Error(t, err)
						assert.Nil(t, output)
						if test.status == http.StatusForbidden {
							assert.Contains(t, err.Error(), prefix+test.errorText)
							var responseErr *azcore.ResponseError
							require.ErrorAs(t, err, &responseErr)
							assert.Equal(t, http.StatusForbidden, responseErr.StatusCode)
						} else {
							assert.EqualError(t, err, prefix+test.errorText)
						}
						if test.lookupErr != nil {
							assert.ErrorIs(t, err, test.lookupErr)
						}
					} else {
						require.NoError(t, err)
						if !test.noDetails {
							assert.Equal(t, ArmOutput{"result": map[string]any{"type": "string", "value": "synthetic output"}}, output)
						}
					}
					if test.noDetails {
						assert.Nil(t, details)
					} else {
						require.NotNil(t, details)
						result, detailsErr := details(ctx)
						require.NoError(t, detailsErr)
						require.NotNil(t, result.ARM)
						assert.Len(t, result.ARM.Operations, 1)
						assert.Equal(t, 1, operations)
					}
				})
			}
		}
	}
}

func TestOperationsDiagnosticsCancellation(t *testing.T) {
	for _, scope := range []string{"ResourceGroup", "Subscription"} {
		t.Run(scope, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			requested := false
			transport := &fakeTransport{interceptor: func(request *http.Request) (*http.Response, error) {
				requested = true
				assert.NoError(t, request.Context().Err())
				cancel()
				<-request.Context().Done()
				return nil, request.Context().Err()
			}}
			client, err := armresources.NewDeploymentOperationsClient("subscription", &fakeCredential{}, &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}}})
			require.NoError(t, err)
			getOperations := func(string) (*armresources.DeploymentOperationsClient, error) { return client, nil }
			details := DetermineOperationsForResourceGroupDeployment(getOperations, "subscription", "rg", "deployment")
			if scope == "Subscription" {
				details = DetermineOperationsForSubscriptionDeployment(getOperations, "subscription", "deployment")
			}
			result, err := details(ctx)
			assert.True(t, requested)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, context.Canceled)
		})
	}
}

func TestIsDeploymentActiveError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "unrelated error",
			err:      errors.New("something else"),
			expected: false,
		},
		{
			name: "409 DeploymentActive",
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "DeploymentActive",
			},
			expected: true,
		},
		{
			name: "409 different error code",
			err: &azcore.ResponseError{
				StatusCode: http.StatusConflict,
				ErrorCode:  "SomethingElse",
			},
			expected: false,
		},
		{
			name: "404 DeploymentNotFound",
			err: &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
				ErrorCode:  "DeploymentNotFound",
			},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isDeploymentActiveError(tt.err))
		})
	}
}

func TestComputeResourceGroupTags(t *testing.T) {
	tests := []struct {
		name         string
		existingTags map[string]*string
		extraTags    map[string]string
		persist      bool
		expectedTags map[string]*string
		description  string
	}{
		// Empty existing tags
		{
			name:         "empty_tags_persist_true",
			existingTags: map[string]*string{},
			persist:      true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
			},
			description: "Empty tags with persist=true should add persist tag",
		},
		{
			name:         "empty_tags_persist_false",
			existingTags: map[string]*string{},
			persist:      false,
			expectedTags: map[string]*string{},
			description:  "Empty tags with persist=false should remain empty",
		},

		// Nil existing tags (edge case)
		{
			name:         "nil_tags_persist_true",
			existingTags: nil,
			persist:      true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
			},
			description: "Nil tags with persist=true should add persist tag",
		},
		{
			name:         "nil_tags_persist_false",
			existingTags: nil,
			persist:      false,
			expectedTags: map[string]*string{},
			description:  "Nil tags with persist=false should result in empty map",
		},

		// Existing tags with persist="true"
		{
			name: "existing_persist_true_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=true with persist=true should preserve persist tag",
		},
		{
			name: "existing_persist_true_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"), // Should be preserved (safety rule)
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=true with persist=false should preserve persist tag (safety rule)",
		},

		// Existing tags with persist="false"
		{
			name: "existing_persist_false_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("false"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=false with persist=true should set persist to true",
		},
		{
			name: "existing_persist_false_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("false"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env": to.Ptr("dev"),
			},
			description: "Existing persist=false with persist=false should not set persist tag",
		},

		// Existing tags with persist="something_else"
		{
			name: "existing_persist_invalid_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("maybe"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=invalid with persist=true should set persist to true",
		},
		{
			name: "existing_persist_invalid_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("maybe"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env": to.Ptr("dev"),
			},
			description: "Existing persist=invalid with persist=false should not set persist tag",
		},

		// Existing tags without persist tag
		{
			name: "no_persist_tag_persist_true",
			existingTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
				"persist": to.Ptr("true"),
			},
			description: "No persist tag with persist=true should add persist tag",
		},
		{
			name: "no_persist_tag_persist_false",
			existingTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			description: "No persist tag with persist=false should not add persist tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeResourceGroupTags(tt.existingTags, tt.extraTags, tt.persist)
			assert.Equal(t, tt.expectedTags, result, tt.description)
		})
	}
}

func TestComputeResourceGroupTagsExtras(t *testing.T) {
	const jobID = "jobID.aro-hcp-ci.redhat.com"
	// Extra tags are the desired values: they apply in full on the create path
	// (nil existing tags) and to pre-existing groups alike.
	tests := []struct {
		name         string
		existingTags map[string]*string
		extraTags    map[string]string
		persist      bool
		expectedTags map[string]*string
	}{
		{
			name:         "create applies extras",
			existingTags: nil,
			extraTags:    map[string]string{jobID: "2097011220782518272"},
			expectedTags: map[string]*string{jobID: to.Ptr("2097011220782518272")},
		},
		{
			name:         "create applies extras with persist",
			existingTags: nil,
			extraTags:    map[string]string{jobID: "2097011220782518272"},
			persist:      true,
			expectedTags: map[string]*string{
				jobID:     to.Ptr("2097011220782518272"),
				"persist": to.Ptr("true"),
			},
		},
		{
			name:         "existing overwrites carried key",
			existingTags: map[string]*string{"owner": to.Ptr("shared"), jobID: to.Ptr("previous")},
			extraTags:    map[string]string{jobID: "2097011220782518272"},
			expectedTags: map[string]*string{"owner": to.Ptr("shared"), jobID: to.Ptr("2097011220782518272")},
		},
		{
			name:         "existing overwrites carried key in any casing",
			existingTags: map[string]*string{"JOBID.ARO-HCP-CI.REDHAT.COM": to.Ptr("previous")},
			extraTags:    map[string]string{jobID: "2097011220782518272"},
			expectedTags: map[string]*string{"JOBID.ARO-HCP-CI.REDHAT.COM": to.Ptr("2097011220782518272")},
		},
		{
			name:         "existing gains a key it did not carry",
			existingTags: map[string]*string{"owner": to.Ptr("shared")},
			extraTags:    map[string]string{jobID: "2097011220782518272"},
			expectedTags: map[string]*string{"owner": to.Ptr("shared"), jobID: to.Ptr("2097011220782518272")},
		},
		{
			name:         "existing without tags gains extras",
			existingTags: map[string]*string{},
			extraTags:    map[string]string{jobID: "2097011220782518272"},
			expectedTags: map[string]*string{jobID: to.Ptr("2097011220782518272")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedTags, computeResourceGroupTags(tt.existingTags, tt.extraTags, tt.persist))
		})
	}
}
