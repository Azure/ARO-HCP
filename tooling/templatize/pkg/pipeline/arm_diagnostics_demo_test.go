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
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/junit"
)

func TestARMDiagnosticsDemoLive(t *testing.T) {
	rg := os.Getenv("ARM_DEMO_RESOURCE_GROUP")
	if rg == "" {
		t.Skip("requires an isolated, pre-created CI resource group")
	}
	subscription, region := os.Getenv("INFRA_SUBSCRIPTION_ID"), os.Getenv("LOCATION")
	require.NotEmpty(t, subscription)
	require.NotEmpty(t, region)

	dir := t.TempDir()
	for name, contents := range map[string]string{
		"outer.bicep": `param missingName string
module inner 'inner.bicep' = {
  name: 'arm-diagnostics-inner'
  params: { missingName: missingName }
}
output result string = inner.outputs.result`,
		"inner.bicep": `param missingName string
module missing 'missing.bicep' = {
  name: 'arm-diagnostics-missing'
  params: { missingName: missingName }
}
output result string = missing.outputs.result`,
		// A runtime reference rather than a Bicep validation error; the fresh group has no storage account.
		"missing.bicep": `param missingName string
resource missing 'Microsoft.Storage/storageAccounts@2023-05-01' existing = { name: missingName }
output result string = missing.properties.primaryEndpoints.blob`,
		"demo.bicepparam": `using './outer.bicep'
param missingName = 'arodiagmissingaccount'`,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	ctx = logr.NewContext(ctx, testr.New(t))
	client, err := bicep.StartJSONRPCServer(ctx, logr.FromContextOrDiscard(ctx), false)
	require.NoError(t, err)

	output := runARMDiagnosticsDemo(t, ctx, dir, subscription, region, rg, client, RunStep)
	require.Contains(t, output, "DeploymentFailed", "the ARM deployment must fail, not the setup")
}

func TestARMDiagnosticsDemoForbidden(t *testing.T) {
	const subscription = "11111111-2222-4333-8444-555555555555"
	ctx := logr.NewContext(t.Context(), testr.New(t))
	client, err := armresources.NewDeploymentsClient(subscription, &fakeCredential{}, &azcorearm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}, Transport: &fakeTransport{interceptor: func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"AuthorizationFailed","message":"synthetic ARM denial"}}`)), Request: request}, nil
		}}},
	})
	require.NoError(t, err)

	output := runARMDiagnosticsDemo(t, ctx, t.TempDir(), subscription, "westus3", "synthetic-group", nil,
		func(_ graph.Identifier, _ types.Step, ctx context.Context, _ ExecutionTarget, _ *StepRunOptions, _ *ExecutionState) (Output, DetailsProducer, error) {
			_, err := client.Get(ctx, "synthetic-group", "synthetic-deployment", nil)
			return nil, nil, err
		})
	require.Contains(t, output, "AuthorizationFailed")
}

func runARMDiagnosticsDemo(t *testing.T, ctx context.Context, dir, subscription, region, rg string, client *bicep.LSPClient, execute Executor) string {
	t.Helper()
	report := filepath.Join(dir, "junit.xml")
	definition := &types.Pipeline{ServiceGroup: "demo", ResourceGroups: []*types.ResourceGroup{{
		ResourceGroupMeta: &types.ResourceGroupMeta{Name: "demo", ResourceGroup: rg, Subscription: "demo"},
		Steps: []types.Step{&types.ARMStep{
			StepMeta: types.StepMeta{Name: "deployment"}, DeploymentLevel: "ResourceGroup", DeploymentMode: "Incremental", Parameters: "demo.bicepparam",
		}},
	}}}
	_, err := RunPipeline(&topology.Service{ServiceGroup: "demo"}, definition, ctx, &PipelineRunOptions{
		BaseRunOptions:        BaseRunOptions{NoPersist: true, BicepClient: client, DeploymentTimeoutSeconds: 180, SkipBicepparamValidation: true},
		Region:                region,
		Concurrency:           1,
		JUnitOutputFile:       report,
		SubsciptionLookupFunc: func(context.Context, string) (string, error) { return subscription, nil },
		TopoDirLookupFunc:     func(string) (string, error) { return dir, nil },
	}, execute)
	require.Error(t, err, "the demo must fail in templatize")
	data, err := os.ReadFile(report)
	require.NoError(t, err)
	var result junit.TestSuites
	require.NoError(t, xml.Unmarshal(data, &result))
	require.Len(t, result.Suites, 1)
	require.Len(t, result.Suites[0].TestCases, 1)
	failure := result.Suites[0].TestCases[0].FailureOutput
	require.NotNil(t, failure)
	t.Logf("DEMO JUNIT OUTPUT:\n%s", failure.Output)
	return failure.Output
}
