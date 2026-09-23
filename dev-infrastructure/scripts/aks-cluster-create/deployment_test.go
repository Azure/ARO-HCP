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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	armcontainerservicefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"
	armdeploymentsfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments/fake"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// Decode the wire template, rather than relying on its Go representation.
type deployedTestResource struct {
	Type       string                     `json:"type"`
	Name       string                     `json:"name"`
	DependsOn  []string                   `json:"dependsOn"`
	Properties map[string]json.RawMessage `json:"properties"`
	Tags       map[string]string          `json:"tags"`
}

func deploymentResources(t *testing.T, deployment armdeployments.Deployment) map[string]deployedTestResource {
	t.Helper()
	require.NotNil(t, deployment.Properties)
	require.Equal(t, ptr.To(armdeployments.DeploymentModeIncremental), deployment.Properties.Mode, "unconfigured resources must not be deleted")
	var template struct {
		Resources []deployedTestResource `json:"resources"`
	}
	body, err := json.Marshal(deployment.Properties.Template)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &template))
	resources := make(map[string]deployedTestResource, len(template.Resources))
	for _, resource := range template.Resources {
		require.NotContains(t, resources, resource.Name, "a pool cannot be deployed twice")
		resources[resource.Name] = resource
	}
	return resources
}

func TestBuildDeploymentResourceOwnership(t *testing.T) {
	o := testValidatedOptions()
	bootstrap := testSystemPool()
	bootstrap.EnableSwift = true
	worker := compute.Pool{
		Name: "worker1", Role: compute.PoolRoleWorker,
		Spec:              compute.VMSpec{Size: "Standard_E16ds_v6", SecondaryNICs: 2},
		AvailabilityZones: []string{"2"}, MinCount: 2, MaxCount: 7,
		OSDiskSizeGB: 128, MaxPods: 42, EnableSwift: true,
		Labels: map[string]string{compute.RoleLabel: string(compute.PoolRoleWorker)},
	}
	pools := []compute.Pool{bootstrap, worker}

	created, _, err := o.buildDeployment(nil, pools)
	require.NoError(t, err)
	createResources := deploymentResources(t, created)
	require.Len(t, createResources, 2, "bootstrap is inline, leaving only the worker child")
	cluster := createResources[o.clusterName]
	require.Equal(t, "Microsoft.ContainerService/managedClusters", cluster.Type)
	assert.JSONEq(t, `"1.31.1"`, string(cluster.Properties["kubernetesVersion"]))
	assert.Equal(t, provisioningTagValue, cluster.Tags[provisioningTagKey])
	var inline []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(cluster.Properties["agentPoolProfiles"], &inline))
	require.Len(t, inline, 1)
	assert.JSONEq(t, `"s1abc1234567"`, string(inline[0]["name"]))
	require.NotContains(t, createResources, o.clusterName+"/"+bootstrap.Name)

	live := deployedTestCluster()
	live.Properties.KubernetesVersion = ptr.To("1.35.2")
	live.Properties.AgentPoolProfiles = append(live.Properties.AgentPoolProfiles, &armcontainerservice.ManagedClusterAgentPoolProfile{Name: ptr.To("controller-owned")})
	updated, _, err := o.buildDeployment(&live, pools)
	require.NoError(t, err)
	updateResources := deploymentResources(t, updated)
	require.Len(t, updateResources, 3, "every configured pool is redeployed, but extra pools are not managed")
	assert.NotContains(t, updateResources, o.clusterName+"/controller-owned")
	assert.NotContains(t, updateResources[o.clusterName].Properties, "agentPoolProfiles", "an update must not overwrite live pool membership")
	assert.NotContains(t, updateResources[o.clusterName].Properties, "kubernetesVersion", "reruns must not downgrade a controller-upgraded cluster")

	clusterID := "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ContainerService/managedClusters/cluster1"
	for _, resources := range []map[string]deployedTestResource{createResources, updateResources} {
		for _, resource := range resources {
			if resource.Name == o.clusterName {
				continue
			}
			assert.Equal(t, "Microsoft.ContainerService/managedClusters/agentPools", resource.Type)
			assert.Equal(t, []string{clusterID}, resource.DependsOn, "child pools must wait for the cluster resource")
			assert.NotContains(t, resource.Properties, "orchestratorVersion", "pool reruns must not downgrade Kubernetes")
		}
	}

	// The bootstrap's create-time representation must retain the complete pool
	// contract when the same pool becomes an explicit child on a later run.
	delete(inline[0], "name")
	inlineBody, err := json.Marshal(inline[0])
	require.NoError(t, err)
	childBody, err := json.Marshal(updateResources[o.clusterName+"/"+bootstrap.Name].Properties)
	require.NoError(t, err)
	assert.JSONEq(t, string(inlineBody), string(childBody))
}

func TestBuildDeploymentSkipsConvergedCluster(t *testing.T) {
	for _, test := range []struct {
		name        string
		state       string
		drift       bool
		wantCluster bool
	}{
		{name: "converged", state: provisioningStateSucceeded},
		{name: "configuration changed", state: provisioningStateSucceeded, drift: true, wantCluster: true},
		{name: "failed recovery", state: provisioningStateFailed, wantCluster: true},
		{name: "canceled recovery", state: provisioningStateCanceled, wantCluster: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := testValidatedOptions()
			live := testReconcileCluster(t, o)
			live.Properties.ProvisioningState = ptr.To(test.state)
			if test.drift {
				live.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("old-key")
				live.Properties.AutoScalerProfile.ScanInterval = ptr.To("1m")
			}
			pool := testSystemPool()
			deployment, changed, err := o.buildDeployment(&live, []compute.Pool{pool})
			require.NoError(t, err)
			if test.drift {
				assert.Equal(t, []string{
					"properties.autoScalerProfile.scan-interval",
					"properties.securityProfile.azureKeyVaultKms.keyId",
				}, changed, "diagnostics must be sorted field paths without configuration values")
			} else {
				assert.Empty(t, changed, "unowned settings and provisioning state are not configuration drift")
			}
			resources := deploymentResources(t, deployment)
			_, included := resources[o.clusterName]
			require.Equal(t, test.wantCluster, included, "even a no-op cluster PUT triggers costly AKS reconciliation")
			child, exists := resources[o.clusterName+"/"+pool.Name]
			require.True(t, exists, "skipping the cluster must not skip configured pools")
			if test.wantCluster {
				assert.Equal(t, []string{"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ContainerService/managedClusters/cluster1"}, child.DependsOn)
			} else {
				assert.Empty(t, child.DependsOn, "a dependency cannot reference a resource omitted from the template")
			}
		})
	}
}

func deployedTestCluster() armcontainerservice.ManagedCluster {
	return armcontainerservice.ManagedCluster{
		ETag: ptr.To("fresh-etag"),
		Tags: map[string]*string{
			provisioningTagKey: ptr.To(provisioningTagValue),
			"clusterType":      ptr.To("mgmt"), "persist": ptr.To("true"),
			"external-owner": ptr.To("keep"),
		},
		Properties: &armcontainerservice.ManagedClusterProperties{
			ProvisioningState: ptr.To("Succeeded"),
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{{Name: ptr.To("sys"), ProvisioningState: ptr.To("Succeeded")}},
		},
	}
}

func newDeploymentRunOptions(t *testing.T, clusters *armcontainerservicefake.ManagedClustersServer, deployments *armdeploymentsfake.DeploymentsServer) *completedOptions {
	t.Helper()
	o := testValidatedOptions()
	o.system = poolConfig{name: "sys", vmSize: "sku", osDiskSizeGB: 32, minCount: 1, maxCount: 3, poolCount: 1, zones: []string{"1"}}
	o.user = poolConfig{name: "worker", vmSize: "sku", osDiskSizeGB: 128, minCount: 0, maxCount: 5, poolCount: 1, zones: []string{"1"}}
	o.infra = poolConfig{vmSize: "sku"}
	client, err := armdeployments.NewDeploymentsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: armdeploymentsfake.NewDeploymentsServerTransport(deployments)},
	})
	require.NoError(t, err)
	return &completedOptions{
		validatedOptions:  o,
		clustersClient:    newManagedClustersTestClient(t, clusters),
		deploymentsClient: client,
		deploymentOperationsClient: newDeploymentOperationsTestClient(t, &armdeploymentsfake.DeploymentOperationsServer{
			NewListPager: func(string, string, *armdeployments.DeploymentOperationsClientListOptions) (resp azfake.PagerResponder[armdeployments.DeploymentOperationsClientListResponse]) {
				resp.AddPage(http.StatusOK, armdeployments.DeploymentOperationsClientListResponse{}, nil)
				return
			},
		}),
		skuCache: newRunTestSKUCache(t, []*armcompute.ResourceSKU{{
			Name: ptr.To("sku"), Family: ptr.To("family"), ResourceType: ptr.To("virtualMachines"),
			Capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: ptr.To("vCPUs"), Value: ptr.To("4")},
				{Name: ptr.To("MemoryGB"), Value: ptr.To("16")},
			},
		}}),
	}
}

func newDeploymentOperationsTestClient(t *testing.T, server *armdeploymentsfake.DeploymentOperationsServer) *armdeployments.DeploymentOperationsClient {
	t.Helper()
	client, err := armdeployments.NewDeploymentOperationsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: armdeploymentsfake.NewDeploymentOperationsServerTransport(server)},
	})
	require.NoError(t, err)
	return client
}

func TestRunDeploymentFinalization(t *testing.T) {
	for _, test := range []struct {
		name               string
		existing           bool
		failure            string
		diagnosticsFailure bool
	}{
		{name: "fresh create"},
		{name: "existing cluster", existing: true},
		{name: "failed deployment", existing: true, failure: "DeploymentFailed"},
		{name: "canceled deployment", existing: true, failure: "DeploymentCanceled"},
		{name: "successful deployment with unavailable diagnostics", diagnosticsFailure: true},
		{name: "failed deployment with unavailable diagnostics", failure: "DeploymentFailed", diagnosticsFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				live := deployedTestCluster()
				var logs bytes.Buffer
				ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
				var operationLists atomic.Int32
				submitted := false
				var observedName, submittedName string
				finalTags := make(chan map[string]*string, 1)
				clusters := &armcontainerservicefake.ManagedClustersServer{
					Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
						if !test.existing && !submitted {
							errResp.SetResponseError(http.StatusNotFound, "ResourceNotFound")
							return
						}
						cluster := live
						if !submitted {
							cluster.ETag = ptr.To("stale-etag")
							cluster.Tags = initialClusterTags(map[string]string{"clusterType": "mgmt", "persist": "true"})
						}
						resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: cluster}, nil)
						return
					},
					BeginUpdateTags: func(_ context.Context, _, _ string, tags armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
						require.Equal(t, ptr.To("fresh-etag"), options.IfMatch, "handover must use the post-deployment observation")
						finalTags <- tags.Tags
						resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{ManagedCluster: live}, nil)
						return
					},
				}
				deployments := &armdeploymentsfake.DeploymentsServer{
					Get: func(_ context.Context, _, name string, _ *armdeployments.DeploymentsClientGetOptions) (resp azfake.Responder[armdeployments.DeploymentsClientGetResponse], errResp azfake.ErrorResponder) {
						observedName = name
						errResp.SetResponseError(http.StatusNotFound, "DeploymentNotFound")
						return
					},
					BeginCreateOrUpdate: func(_ context.Context, _, name string, deployment armdeployments.Deployment, _ *armdeployments.DeploymentsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armdeployments.DeploymentsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
						submitted, submittedName = true, name
						resources := deploymentResources(t, deployment)
						require.Contains(t, resources, "cluster1/worker1")
						if test.existing {
							assert.NotContains(t, resources["cluster1"].Properties, "agentPoolProfiles")
							assert.NotContains(t, resources["cluster1"].Properties, "kubernetesVersion")
							require.Contains(t, resources, "cluster1/sys")
						} else {
							require.Contains(t, resources["cluster1"].Properties, "agentPoolProfiles")
							assert.NotContains(t, resources, "cluster1/sys")
						}
						resp.AddNonTerminalResponse(http.StatusCreated, nil)
						resp.AddNonTerminalResponse(http.StatusCreated, nil)
						if test.failure != "" {
							resp.SetTerminalError(http.StatusBadRequest, test.failure)
						} else {
							resp.SetTerminalResponse(http.StatusOK, armdeployments.DeploymentsClientCreateOrUpdateResponse{DeploymentExtended: armdeployments.DeploymentExtended{
								Properties: &armdeployments.DeploymentPropertiesExtended{ProvisioningState: ptr.To(armdeployments.ProvisioningStateSucceeded)},
							}}, nil)
						}
						return
					},
				}
				o := newDeploymentRunOptions(t, clusters, deployments)
				o.deploymentOperationsClient = newDeploymentOperationsTestClient(t, &armdeploymentsfake.DeploymentOperationsServer{
					NewListPager: func(string, string, *armdeployments.DeploymentOperationsClientListOptions) (resp azfake.PagerResponder[armdeployments.DeploymentOperationsClientListResponse]) {
						operationLists.Add(1)
						resp.AddPage(http.StatusOK, armdeployments.DeploymentOperationsClientListResponse{
							DeploymentOperationsListResult: armdeployments.DeploymentOperationsListResult{
								Value: []*armdeployments.DeploymentOperation{{
									OperationID: ptr.To("cluster-operation"),
									Properties: &armdeployments.DeploymentOperationProperties{
										TargetResource: &armdeployments.TargetResource{ResourceName: ptr.To("cluster1")},
										Duration:       ptr.To("PT30S"),
										Request:        &armdeployments.HTTPMessage{Content: "secret-request-body"},
										Response:       &armdeployments.HTTPMessage{Content: "secret-response-body"},
									},
								}},
							},
						}, nil)
						if test.diagnosticsFailure {
							resp.AddResponseError(http.StatusForbidden, "DiagnosticsForbidden")
							return
						}
						properties := &armdeployments.DeploymentOperationProperties{
							TargetResource:    &armdeployments.TargetResource{ResourceName: ptr.To("cluster1/worker1")},
							ProvisioningState: ptr.To(provisioningStateSucceeded),
						}
						if test.failure != "" {
							properties.ProvisioningState = ptr.To(provisioningStateFailed)
							properties.StatusMessage = &armdeployments.StatusMessage{Error: &armdeployments.ErrorResponse{
								Code:    ptr.To("PoolProvisioningFailed"),
								Details: []*armdeployments.ErrorResponse{{Code: ptr.To("QuotaExceeded"), Message: ptr.To("not enough regional cores")}},
							}}
						}
						resp.AddPage(http.StatusOK, armdeployments.DeploymentOperationsClientListResponse{
							DeploymentOperationsListResult: armdeployments.DeploymentOperationsListResult{
								Value: []*armdeployments.DeploymentOperation{{OperationID: ptr.To("pool-operation"), Properties: properties}},
							},
						}, nil)
						return
					},
				})
				result := make(chan error, 1)
				go func() { result <- o.run(ctx) }()
				synctest.Wait()
				require.True(t, submitted)
				assert.Empty(t, finalTags, "handover must not happen while the deployment is pending")
				assert.Zero(t, operationLists.Load(), "diagnostics must not poll alongside the deployment")
				err := <-result
				assert.Equal(t, int32(1), operationLists.Load(), "collect operations once, on both success and failure")
				assert.Contains(t, logs.String(), "PT30S", "retain operation timings even when a later page fails")
				assert.NotContains(t, logs.String(), "secret-request-body")
				assert.NotContains(t, logs.String(), "secret-response-body")
				if test.diagnosticsFailure {
					assert.Contains(t, logs.String(), "DiagnosticsForbidden")
				} else {
					assert.Contains(t, logs.String(), "cluster1/worker1", "collect resources beyond the first page")
					if test.failure != "" {
						assert.Contains(t, logs.String(), "QuotaExceeded", "include nested provider errors")
					}
				}
				assert.Equal(t, observedName, submittedName, "the observed deployment must be the one this cluster submits")
				if test.failure != "" {
					require.ErrorContains(t, err, test.failure)
					assert.Empty(t, finalTags, "failed or canceled deployments must leave the handover marker intact")
					return
				}
				require.NoError(t, err)
				require.Len(t, finalTags, 1)
				tags := <-finalTags
				assert.NotContains(t, tags, provisioningTagKey)
				assert.Equal(t, ptr.To("keep"), tags["external-owner"], "fresh external tags must survive finalization")
			})
		})
	}
}

func TestRunWaitsForActiveDeployment(t *testing.T) {
	for _, terminal := range []armdeployments.ProvisioningState{
		armdeployments.ProvisioningStateSucceeded,
		armdeployments.ProvisioningStateFailed,
		armdeployments.ProvisioningStateCanceled,
	} {
		t.Run(string(terminal), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				observations := 0
				var logs bytes.Buffer
				ctx := logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil)))
				previousFinished := false
				var observedName string
				submissions := 0
				live := deployedTestCluster()
				delete(live.Tags, provisioningTagKey)
				o := newDeploymentRunOptions(t, &armcontainerservicefake.ManagedClustersServer{
					Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
						require.True(t, previousFinished, "cluster reads must follow the active deployment, not provide stale input")
						if terminal == armdeployments.ProvisioningStateFailed {
							assert.Contains(t, logs.String(), "PreviousPoolFailure", "report the prior error before recovery observes resources")
							assert.Contains(t, logs.String(), "previous pool exhausted quota")
						}
						resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: live}, nil)
						return
					},
				}, &armdeploymentsfake.DeploymentsServer{
					Get: func(_ context.Context, _, name string, _ *armdeployments.DeploymentsClientGetOptions) (resp azfake.Responder[armdeployments.DeploymentsClientGetResponse], errResp azfake.ErrorResponder) {
						observedName = name
						observations++
						state := armdeployments.ProvisioningStateRunning
						if observations >= 3 {
							state, previousFinished = terminal, true
						}
						properties := &armdeployments.DeploymentPropertiesExtended{ProvisioningState: ptr.To(state)}
						if state == armdeployments.ProvisioningStateFailed {
							properties.Error = &armdeployments.ErrorResponse{
								Code:    ptr.To("DeploymentFailed"),
								Details: []*armdeployments.ErrorResponse{{Code: ptr.To("PreviousPoolFailure"), Message: ptr.To("previous pool exhausted quota")}},
							}
						}
						resp.SetResponse(http.StatusOK, armdeployments.DeploymentsClientGetResponse{DeploymentExtended: armdeployments.DeploymentExtended{
							Properties: properties,
						}}, nil)
						return
					},
					BeginCreateOrUpdate: func(_ context.Context, _, name string, _ armdeployments.Deployment, _ *armdeployments.DeploymentsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armdeployments.DeploymentsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
						require.True(t, previousFinished, "never overwrite an active deployment")
						assert.Equal(t, observedName, name)
						submissions++
						resp.SetTerminalResponse(http.StatusOK, armdeployments.DeploymentsClientCreateOrUpdateResponse{DeploymentExtended: armdeployments.DeploymentExtended{
							Properties: &armdeployments.DeploymentPropertiesExtended{ProvisioningState: ptr.To(armdeployments.ProvisioningStateSucceeded)},
						}}, nil)
						return
					},
				})

				require.NoError(t, o.run(ctx))
				require.Equal(t, 1, submissions, "configured resources must be redeployed even after a previous success or failure")
			})
		})
	}
}

func TestRunCancelWhileWaitingForActiveDeployment(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := newDeploymentRunOptions(t, &armcontainerservicefake.ManagedClustersServer{}, &armdeploymentsfake.DeploymentsServer{
			Get: func(context.Context, string, string, *armdeployments.DeploymentsClientGetOptions) (resp azfake.Responder[armdeployments.DeploymentsClientGetResponse], errResp azfake.ErrorResponder) {
				resp.SetResponse(http.StatusOK, armdeployments.DeploymentsClientGetResponse{DeploymentExtended: armdeployments.DeploymentExtended{
					Properties: &armdeployments.DeploymentPropertiesExtended{ProvisioningState: ptr.To(armdeployments.ProvisioningStateRunning)},
				}}, nil)
				return
			},
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- o.run(ctx) }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-result, context.Canceled)
		// No cluster or deployment write endpoint is supplied: cancellation must
		// leave the active operation and its handover marker untouched.
	})
}

type deploymentDiagnosticsTransport func(*http.Request) (*http.Response, error)

func (f deploymentDiagnosticsTransport) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDeploymentDiagnosticsAfterCancellation(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "available"
		if blocked {
			name = "bounded when unavailable"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var logs bytes.Buffer
				ctx, cancel := context.WithCancel(logr.NewContext(t.Context(), logr.FromSlogHandler(slog.NewJSONHandler(&logs, nil))))
				cancel()
				client, err := armdeployments.NewDeploymentOperationsClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{
					ClientOptions: policy.ClientOptions{Transport: deploymentDiagnosticsTransport(func(req *http.Request) (*http.Response, error) {
						if blocked {
							<-req.Context().Done()
							return nil, req.Context().Err()
						}
						return &http.Response{
							StatusCode: http.StatusOK, Header: make(http.Header), Request: req,
							Body: io.NopCloser(strings.NewReader(`{"value":[{"operationId":"pool","properties":{"statusMessage":{"error":{"code":"PoolTimedOut"}}}}]}`)),
						}, nil
					})},
				})
				require.NoError(t, err)
				o := &completedOptions{validatedOptions: testValidatedOptions(), deploymentOperationsClient: client}
				started := time.Now()
				o.logDeploymentOperations(ctx, deploymentName)
				if blocked {
					assert.Contains(t, logs.String(), context.DeadlineExceeded.Error())
					assert.LessOrEqual(t, time.Since(started), time.Minute, "unavailable diagnostics must not hang recovery")
				} else {
					assert.Contains(t, logs.String(), "PoolTimedOut", "a canceled deployment context must still allow diagnostics")
				}
			})
		})
	}
}
