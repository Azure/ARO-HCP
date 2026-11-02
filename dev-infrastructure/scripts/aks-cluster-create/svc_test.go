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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpools"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func svcEnv(overrides map[string]string) func(string) string {
	values := map[string]string{
		"CLUSTER_TYPE": "svc", "REGION_AVAILABILITY_ZONE_COUNT": "",
		"LOCATION_AVAILABILITY_ZONES": "1,3,4", "ISTIO_VERSIONS": "asm-1-29",
		"SYSTEM_POOL_ZONES": "", "USER_POOL_ZONES": "", "INFRA_POOL_ZONES": "",
		"SYSTEM_POOL_ZONE_REDUNDANT_MODE": "Auto", "USER_POOL_ZONE_REDUNDANT_MODE": "Auto", "INFRA_POOL_ZONE_REDUNDANT_MODE": "Auto",
		"USER_POOL_COUNT": "5", "USER_POOL_MIN_COUNT": "2",
		"USER_POOL_MAX_SURGE": "25%", "USER_POOL_MAX_UNAVAILABLE": "0",
		"INFRA_POOL_MAX_SURGE": "0", "INFRA_POOL_MAX_UNAVAILABLE": "1",
	}
	for key, value := range overrides {
		values[key] = value
	}
	return envFunc(values)
}

func testSvcOptions(t *testing.T) *validatedOptions {
	t.Helper()
	o, err := newRawOptionsFromEnv(svcEnv(nil)).Validate()
	require.NoError(t, err)
	return o
}

func svcSKUs() []*armcompute.ResourceSKU {
	var skus []*armcompute.ResourceSKU
	for _, name := range []string{"Standard_D4ds_v6", "Standard_E16ds_v6"} {
		skus = append(skus, &armcompute.ResourceSKU{
			Name: ptr.To(name), ResourceType: ptr.To("virtualMachines"),
			Capabilities: []*armcompute.ResourceSKUCapabilities{
				{Name: ptr.To("vCPUs"), Value: ptr.To("4")},
				{Name: ptr.To("MemoryGB"), Value: ptr.To("16")},
				{Name: ptr.To("MaxNetworkInterfaces"), Value: ptr.To("8")},
			},
		})
	}
	return skus
}

func TestSvcValidationAndPlacement(t *testing.T) {
	for _, test := range []struct {
		name      string
		overrides map[string]string
		want      []poolPlacement
		wantErr   string
	}{
		{name: "regional identifiers and nonzonal overflow", want: []poolPlacement{{name: "w1", zones: []string{"1"}}, {name: "w3", zones: []string{"3"}}, {name: "w4", zones: []string{"4"}}, {name: "wnz1"}, {name: "wnz2"}}},
		{name: "disabled redundancy", overrides: map[string]string{"USER_POOL_ZONE_REDUNDANT_MODE": "Disabled", "USER_POOL_COUNT": "2"}, want: []poolPlacement{{name: "wnz1"}, {name: "wnz2"}}},
		{name: "explicit ordered zones", overrides: map[string]string{"USER_POOL_ZONES": "4,1", "USER_POOL_COUNT": "3", "USER_POOL_ZONE_REDUNDANT_MODE": "Enabled"}, want: []poolPlacement{{name: "w4", zones: []string{"4"}}, {name: "w1", zones: []string{"1"}}, {name: "wnz1"}}},
		{name: "nonzonal region", overrides: map[string]string{"LOCATION_AVAILABILITY_ZONES": "", "USER_POOL_COUNT": "1"}, want: []poolPlacement{{name: "wnz1"}}},
		{name: "Bicep truncation", overrides: map[string]string{"USER_POOL_NAME": "abcdefghijk", "USER_POOL_COUNT": "4"}, want: []poolPlacement{{name: "abcdefghijk1", zones: []string{"1"}}, {name: "abcdefghijk3", zones: []string{"3"}}, {name: "abcdefghijk4", zones: []string{"4"}}, {name: "abcdefghijkn"}}},
		{name: "invalid redundancy", overrides: map[string]string{"USER_POOL_ZONE_REDUNDANT_MODE": "enabled"}, wantErr: "USER_POOL_ZONE_REDUNDANT_MODE"},
		{name: "missing revisions", overrides: map[string]string{"ISTIO_VERSIONS": ""}, wantErr: "ISTIO_VERSIONS"},
		{name: "missing explicit kind", overrides: map[string]string{"CLUSTER_TYPE": ""}, wantErr: "CLUSTER_TYPE"},
		{name: "unknown kind", overrides: map[string]string{"CLUSTER_TYPE": "service"}, wantErr: "CLUSTER_TYPE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			o, err := newRawOptionsFromEnv(svcEnv(test.overrides)).Validate()
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			completed := &completedOptions{validatedOptions: o, skuCache: newRunTestSKUCache(t, svcSKUs())}
			pools, err := completed.desiredPools(t.Context())
			require.NoError(t, err)
			var workers []poolPlacement
			for _, pool := range pools {
				assert.False(t, pool.EnableSwift)
				assert.Zero(t, pool.SecondaryNICs)
				props := agentpoolspec.Build(pool, compute.NetworkConfig{})
				assert.Empty(t, props.Tags)
				switch pool.Role {
				case compute.PoolRoleSystem:
					assert.Equal(t, armcontainerservice.AgentPoolModeSystem, pool.AgentPoolMode)
					assert.Equal(t, []string{compute.TaintCriticalAddonsOnly}, pool.Taints)
					assert.Equal(t, ptr.To("10%"), props.UpgradeSettings.MaxSurge)
					assert.Nil(t, props.UpgradeSettings.MaxUnavailable)
				case compute.PoolRoleWorker:
					workers = append(workers, poolPlacement{name: pool.Name, zones: pool.AvailabilityZones})
					assert.Equal(t, ptr.To("25%"), props.UpgradeSettings.MaxSurge)
					assert.Equal(t, ptr.To("0"), props.UpgradeSettings.MaxUnavailable)
				case compute.PoolRoleInfra:
					assert.Equal(t, []string{compute.TaintInfra}, pool.Taints)
					assert.Equal(t, ptr.To("0"), props.UpgradeSettings.MaxSurge)
					assert.Equal(t, ptr.To("1"), props.UpgradeSettings.MaxUnavailable)
				}
			}
			assert.Equal(t, test.want, workers)
		})
	}
}

func TestSvcSystemPoolOnlyIgnoresDisabledConfiguration(t *testing.T) {
	o, err := newRawOptionsFromEnv(svcEnv(map[string]string{
		"SYSTEM_POOL_ONLY": "true", "USER_POOL_VM_SIZE": "missing", "USER_POOL_MAX_COUNT": "invalid",
		"USER_POOL_ZONE_REDUNDANT_MODE": "invalid", "INFRA_POOL_NAME": "", "INFRA_POOL_MIN_COUNT": "invalid",
	})).Validate()
	require.NoError(t, err)
	completed := &completedOptions{validatedOptions: o, skuCache: newRunTestSKUCache(t, svcSKUs()[:1])}
	pools, err := completed.desiredPools(t.Context())
	require.NoError(t, err)
	require.Len(t, pools, 1)
	assert.Equal(t, armcontainerservice.AgentPoolModeSystem, pools[0].AgentPoolMode)
	assert.Equal(t, map[string]string{compute.RoleLabel: "system"}, pools[0].Labels)
	assert.Empty(t, pools[0].Taints)
	_, err = newRawOptionsFromEnv(envFunc(map[string]string{"SYSTEM_POOL_ONLY": "true"})).Validate()
	require.ErrorContains(t, err, "not supported for mgmt")
}

func TestSvcMeshLifecycleWireOwnership(t *testing.T) {
	for _, test := range []struct {
		name          string
		state         string
		marked        bool
		revisions     []string
		missingState  bool
		wantBootstrap bool
		wantErr       string
	}{
		{name: "established canary", state: "Succeeded", revisions: []string{"asm-1-28", "asm-1-29"}},
		{name: "failed bootstrap already has revisions", state: "Failed", marked: true, revisions: []string{"asm-1-28", "asm-1-29"}},
		{name: "failed legacy with revisions", state: "Failed", revisions: []string{"asm-1-28"}},
		{name: "marked failed without revisions", state: "Failed", marked: true, wantBootstrap: true},
		{name: "marked canceled without revisions", state: "Canceled", marked: true, wantBootstrap: true},
		{name: "legacy failure ambiguous", state: "Failed", wantErr: "ambiguous svc bootstrap recovery"},
		{name: "healthy marker without revisions ambiguous", state: "Succeeded", marked: true, wantErr: "ambiguous svc bootstrap recovery"},
		{name: "missing state", missingState: true, revisions: []string{"asm-1-28"}, wantErr: "provisioning state is missing"},
		{name: "invalid observed revision", state: "Failed", marked: true, revisions: []string{""}, wantErr: "invalid observed Istio revision"},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := testSvcOptions(t)
			pool := testSystemPool()
			live := deployedTestCluster()
			live.Properties.ProvisioningState = ptr.To(test.state)
			if test.missingState {
				live.Properties.ProvisioningState = nil
			}
			if !test.marked {
				delete(live.Tags, agentpools.ProvisioningTagKey)
			}
			if test.revisions != nil {
				live.Properties.ServiceMeshProfile = &armcontainerservice.ServiceMeshProfile{Istio: &armcontainerservice.IstioServiceMesh{}}
				for _, revision := range test.revisions {
					live.Properties.ServiceMeshProfile.Istio.Revisions = append(live.Properties.ServiceMeshProfile.Istio.Revisions, ptr.To(revision))
				}
			}
			before, err := json.Marshal(live)
			require.NoError(t, err)
			deployment, _, err := o.buildDeployment(&live, []compute.Pool{pool})
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			resources := deploymentResources(t, deployment)
			cluster := resources[o.clusterName]
			assert.NotContains(t, cluster.Properties, "kubernetesVersion")
			var mesh struct {
				Istio map[string]json.RawMessage `json:"istio"`
			}
			require.NoError(t, json.Unmarshal(cluster.Properties["serviceMeshProfile"], &mesh))
			if test.wantBootstrap {
				assert.JSONEq(t, `["asm-1-29"]`, string(mesh.Istio["revisions"]))
			} else {
				assert.NotContains(t, mesh.Istio, "revisions", "must omit live merged revisions, not null or []")
			}
			assert.JSONEq(t, `{"gatewayAPI":{"installation":"Standard"}}`, string(cluster.Properties["ingressProfile"]))
			after, err := json.Marshal(live)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "live observation must remain untouched")
			if test.marked {
				assert.Equal(t, agentpools.ProvisioningTagValue, cluster.Tags[agentpools.ProvisioningTagKey])
			}
		})
	}
}

func TestSvcCreationAndObservedPoolCounts(t *testing.T) {
	o := testSvcOptions(t)
	completed := &completedOptions{validatedOptions: o, skuCache: newRunTestSKUCache(t, svcSKUs())}
	pools, err := completed.desiredPools(t.Context())
	require.NoError(t, err)
	deployment, _, err := o.buildDeployment(nil, pools)
	require.NoError(t, err)
	resources := deploymentResources(t, deployment)
	assert.JSONEq(t, `"1.31.1"`, string(resources[o.clusterName].Properties["kubernetesVersion"]))
	var mesh struct {
		Mode  string `json:"mode"`
		Istio struct {
			Revisions  []string `json:"revisions"`
			Components struct {
				IngressGateways []struct {
					Enabled bool   `json:"enabled"`
					Mode    string `json:"mode"`
				} `json:"ingressGateways"`
			} `json:"components"`
		} `json:"istio"`
	}
	require.NoError(t, json.Unmarshal(resources[o.clusterName].Properties["serviceMeshProfile"], &mesh))
	assert.Equal(t, "Istio", mesh.Mode)
	assert.Equal(t, []string{"asm-1-29"}, mesh.Istio.Revisions)
	require.Len(t, mesh.Istio.Components.IngressGateways, 1)
	assert.True(t, mesh.Istio.Components.IngressGateways[0].Enabled)
	assert.Equal(t, "External", mesh.Istio.Components.IngressGateways[0].Mode)
	assert.JSONEq(t, `2`, string(resources[o.clusterName+"/w1"].Properties["count"]))
	live, _, err := o.desiredClusterSpec(nil, &pools[0])
	require.NoError(t, err)
	live.Properties.ProvisioningState = ptr.To("Succeeded")
	live.Properties.AgentPoolProfiles = append(live.Properties.AgentPoolProfiles, &armcontainerservice.ManagedClusterAgentPoolProfile{Name: ptr.To("W1"), Count: ptr.To[int32](4)})
	updated, changed, err := o.buildDeployment(&live, pools)
	require.NoError(t, err)
	assert.Empty(t, changed)
	resources = deploymentResources(t, updated)
	assert.NotContains(t, resources, o.clusterName, "healthy svc reruns must avoid an AKS cluster PUT")
	assert.NotContains(t, resources[o.clusterName+"/w1"].Properties, "count", "existing autoscaled capacity is not owned")
	assert.JSONEq(t, `2`, string(resources[o.clusterName+"/w3"].Properties["count"]))
	assert.NotContains(t, resources[o.clusterName+"/"+pools[0].Name].Properties, "count")
	live.Properties.IngressProfile.GatewayAPI.Installation = ptr.To(armcontainerservice.ManagedGatewayType("Disabled"))
	_, changed, err = o.buildDeployment(&live, pools)
	require.NoError(t, err)
	assert.Contains(t, changed, "properties.ingressProfile.gatewayAPI.installation")
	live.Properties.AgentPoolProfiles = nil
	_, _, err = o.buildDeployment(&live, pools)
	require.ErrorContains(t, err, "agent pool observation is missing")
}

func TestSvcUsableFailedGETSubmitsRecoveryAndFinalizes(t *testing.T) {
	for _, test := range []struct {
		name           string
		failDeployment bool
		canary         bool
	}{
		{name: "successful recovery"},
		{name: "rejected recovery retains marker", failDeployment: true},
		{name: "failed canary preserves revisions", canary: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			live := deployedTestCluster()
			live.Properties.ProvisioningState = ptr.To("Failed")
			if test.canary {
				live.Properties.ServiceMeshProfile = &armcontainerservice.ServiceMeshProfile{Istio: &armcontainerservice.IstioServiceMesh{Revisions: []*string{ptr.To("asm-1-28"), ptr.To("asm-1-29")}}}
			}
			submitted, finalized := false, false
			o := newDeploymentRunOptions(t, &armcontainerservicefake.ManagedClustersServer{
				Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					observation := live
					if submitted {
						properties := *live.Properties
						observation.Properties = &properties
						properties.ProvisioningState = ptr.To("Succeeded")
					}
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: observation}, nil)
					return
				},
				BeginUpdateTags: func(_ context.Context, _, _ string, tags armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
					finalized = true
					assert.True(t, submitted)
					assert.Equal(t, ptr.To("fresh-etag"), options.IfMatch)
					assert.NotContains(t, tags.Tags, agentpools.ProvisioningTagKey)
					assert.Equal(t, ptr.To("keep"), tags.Tags["external-owner"])
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{ManagedCluster: live}, nil)
					return
				},
			}, &armdeploymentsfake.DeploymentsServer{
				Get: func(context.Context, string, string, *armdeployments.DeploymentsClientGetOptions) (resp azfake.Responder[armdeployments.DeploymentsClientGetResponse], errResp azfake.ErrorResponder) {
					errResp.SetResponseError(http.StatusNotFound, "DeploymentNotFound")
					return
				},
				BeginCreateOrUpdate: func(_ context.Context, _, _ string, deployment armdeployments.Deployment, _ *armdeployments.DeploymentsClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armdeployments.DeploymentsClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
					submitted = true
					resources := deploymentResources(t, deployment)
					cluster := resources["cluster1"]
					assert.Equal(t, agentpools.ProvisioningTagValue, cluster.Tags[agentpools.ProvisioningTagKey])
					assert.NotContains(t, cluster.Properties, "kubernetesVersion")
					assert.NotContains(t, cluster.Properties, "agentPoolProfiles")
					if test.canary {
						var mesh struct {
							Istio map[string]json.RawMessage `json:"istio"`
						}
						require.NoError(t, json.Unmarshal(cluster.Properties["serviceMeshProfile"], &mesh))
						assert.NotContains(t, mesh.Istio, "revisions", "actual submitted wire must omit live canary revisions")
					} else {
						assert.Contains(t, string(cluster.Properties["serviceMeshProfile"]), `"revisions":["asm-1-29"]`)
					}
					if test.failDeployment {
						resp.SetTerminalError(http.StatusBadRequest, "KMSRejected")
					} else {
						resp.SetTerminalResponse(http.StatusOK, armdeployments.DeploymentsClientCreateOrUpdateResponse{DeploymentExtended: armdeployments.DeploymentExtended{Properties: &armdeployments.DeploymentPropertiesExtended{ProvisioningState: ptr.To(armdeployments.ProvisioningStateSucceeded)}}}, nil)
					}
					return
				},
			})
			o.clusterType, o.istioVersions, o.systemPoolOnly = clusterTypeSvc, []string{"asm-1-29"}, true
			err := o.run(t.Context())
			assert.True(t, submitted, "a usable Failed GET must reach actual generated deployment submission")
			if test.failDeployment {
				require.ErrorContains(t, err, "KMSRejected")
				assert.False(t, finalized)
			} else {
				require.NoError(t, err)
				assert.True(t, finalized)
			}
			assert.Equal(t, ptr.To(agentpools.ProvisioningTagValue), live.Tags[agentpools.ProvisioningTagKey])
		})
	}
}

func TestSvcDiscoveryFailuresNeverBecomeAbsence(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":{"code":"Forbidden","message":"not allowed"}}`},
		{name: "throttled", status: http.StatusTooManyRequests, body: `{"error":{"code":"TooManyRequests"}}`},
		{name: "invalid JSON", status: http.StatusOK, body: `{"properties":`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := armcontainerservice.NewManagedClustersClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: deploymentDiagnosticsTransport(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": []string{"application/json"}}, Request: req, Body: io.NopCloser(strings.NewReader(test.body))}, nil
				}),
			}})
			require.NoError(t, err)
			got, err := getCluster(t.Context(), client, "rg1", "cluster1")
			require.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

func TestSvcGatewayAPIObservationAndDrift(t *testing.T) {
	for _, installation := range []string{"Standard", "Disabled", ""} {
		t.Run("installation="+installation, func(t *testing.T) {
			o := testSvcOptions(t)
			pool := testSystemPool()
			live, _, err := o.desiredClusterSpec(nil, &pool)
			require.NoError(t, err)
			live.Properties.ProvisioningState = ptr.To("Succeeded")
			if installation == "" {
				live.Properties.IngressProfile = nil
			} else {
				live.Properties.IngressProfile.GatewayAPI.Installation = ptr.To(armcontainerservice.ManagedGatewayType(installation))
			}
			body, err := json.Marshal(live)
			require.NoError(t, err)
			client, err := armcontainerservice.NewManagedClustersClient("sub1", &azfake.TokenCredential{}, &azcorearm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: deploymentDiagnosticsTransport(func(req *http.Request) (*http.Response, error) {
					assert.Equal(t, http.MethodGet, req.Method)
					assert.Equal(t, "2025-10-02-preview", req.URL.Query().Get("api-version"))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Request: req, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				}),
			}})
			require.NoError(t, err)
			observed, err := getCluster(t.Context(), client, "rg1", "cluster1")
			require.NoError(t, err)
			deployment, changed, err := o.buildDeployment(observed, []compute.Pool{pool})
			require.NoError(t, err)
			resources := deploymentResources(t, deployment)
			if installation == "Standard" {
				assert.Empty(t, changed)
				assert.NotContains(t, resources, o.clusterName)
				return
			}
			assert.NotEmpty(t, changed)
			assert.Contains(t, resources, o.clusterName)
			assert.JSONEq(t, `{"gatewayAPI":{"installation":"Standard"}}`, string(resources[o.clusterName].Properties["ingressProfile"]))
		})
	}
}

func TestSvcFinalizationRetainsMarkerWithoutPoolObservation(t *testing.T) {
	options := testSvcOptions(t)
	live, _, err := options.desiredClusterSpec(nil, ptr.To(testSystemPool()))
	require.NoError(t, err)
	live.Properties.ProvisioningState = ptr.To(provisioningStateSucceeded)
	live.ETag = ptr.To("observed-etag")
	live.Properties.AgentPoolProfiles = nil
	completed := &completedOptions{validatedOptions: options}
	err = completed.reconcileClusterTags(context.Background(), &live)
	require.ErrorContains(t, err, "missing agent pool observation")
	assert.Equal(t, ptr.To(agentpools.ProvisioningTagValue), live.Tags[agentpools.ProvisioningTagKey])
}
