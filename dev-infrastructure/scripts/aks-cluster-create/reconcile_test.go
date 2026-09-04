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
	"net/http"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	armcontainerservicefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8/fake"
)

// testReconcileCluster adds service defaults and configuration owned elsewhere
// to a fully provisioned cluster. Neither should cause a configuration update.
func testReconcileCluster(t *testing.T, o *validatedOptions) armcontainerservice.ManagedCluster {
	t.Helper()
	bootstrap := testSystemPool()
	cluster, _, err := buildClusterSpec(o, nil, &bootstrap)
	require.NoError(t, err)
	delete(cluster.Tags, provisioningTagKey)
	cluster.ETag = ptr.To("etag-before")
	cluster.Location = ptr.To("observed-region")
	cluster.Properties.DNSPrefix = ptr.To("observed-dns-prefix")
	cluster.Properties.NodeResourceGroup = ptr.To("observed-node-resource-group")
	cluster.Tags["owner"] = ptr.To("another-controller")
	cluster.Tags["arohcp-capacity-system"] = ptr.To(`{"vcpus":12,"memoryGiB":48,"swiftNICs":0}`)
	cluster.Tags["arohcp-capacity-infra"] = ptr.To(`{"vcpus":24,"memoryGiB":96,"swiftNICs":0}`)
	cluster.Tags["arohcp-capacity-worker"] = ptr.To(`{"vcpus":999,"memoryGiB":9999,"swiftNICs":999}`)
	cluster.Properties.ProvisioningState = ptr.To("Succeeded")
	cluster.Properties.KubernetesVersion = ptr.To("1.32.5")
	cluster.Properties.AgentPoolProfiles[0].Count = ptr.To[int32](7)
	cluster.Properties.AgentPoolProfiles[0].MaxCount = ptr.To[int32](11)
	cluster.Properties.APIServerAccessProfile = &armcontainerservice.ManagedClusterAPIServerAccessProfile{
		AuthorizedIPRanges: []*string{ptr.To("192.0.2.0/24")},
	}
	cluster.Properties.AutoScalerProfile.MaxEmptyBulkDelete = ptr.To("10")
	cluster.Properties.NetworkProfile.LoadBalancerProfile.IdleTimeoutInMinutes = ptr.To[int32](30)
	cluster.Properties.AddonProfiles["azureKeyvaultSecretsProvider"].Config["service-default"] = ptr.To("preserve-me")
	cluster.Properties.AddonProfiles["anotherAddon"] = &armcontainerservice.ManagedClusterAddonProfile{
		Enabled: ptr.To(true),
		Config:  map[string]*string{"owner": ptr.To("another-controller")},
	}
	return cluster
}

func TestReconcileClusterConfigurationConverges(t *testing.T) {
	tests := []struct {
		name  string
		drift func(*armcontainerservice.ManagedCluster)
	}{
		{
			name: "KMS key rotation",
			drift: func(cluster *armcontainerservice.ManagedCluster) {
				cluster.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old-version")
			},
		},
		{
			name: "owned static configuration changed and missing",
			drift: func(cluster *armcontainerservice.ManagedCluster) {
				cluster.Properties.DisableLocalAccounts = ptr.To(false)
				cluster.Properties.AutoScalerProfile.ScanInterval = ptr.To("1m")
				cluster.Properties.AddonProfiles["azureKeyvaultSecretsProvider"].Config["rotationPollInterval"] = ptr.To("5m")
				cluster.Properties.StorageProfile.FileCSIDriver = nil
			},
		},
		{
			name: "clearing a configured metrics allowlist",
			drift: func(cluster *armcontainerservice.ManagedCluster) {
				cluster.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics.MetricLabelsAllowlist = ptr.To("pods=[*]")
			},
		},
		{
			name: "retrying a failed update whose desired properties are already visible",
			drift: func(cluster *armcontainerservice.ManagedCluster) {
				cluster.Properties.ProvisioningState = ptr.To(provisioningStateFailed)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validated := testValidatedOptions()
			want := testReconcileCluster(t, validated)
			live := testReconcileCluster(t, validated)
			test.drift(&live)
			writes := 0
			client := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
				Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: live}, nil)
					return
				},
				BeginCreateOrUpdate: func(_ context.Context, resourceGroup, clusterName string, parameters armcontainerservice.ManagedCluster, options *armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
					writes++
					assert.Equal(t, validated.resourceGroup, resourceGroup)
					assert.Equal(t, validated.clusterName, clusterName)
					require.NotNil(t, options)
					require.Equal(t, live.ETag, options.IfMatch, "reject updates based on stale observed configuration")
					require.NotNil(t, parameters.Properties)

					// The fake server sees the SDK-decoded request. Re-encoding it
					// also distinguishes omitted properties from empty arrays.
					body, err := json.Marshal(parameters)
					require.NoError(t, err)
					var payload struct {
						Properties map[string]json.RawMessage `json:"properties"`
					}
					require.NoError(t, json.Unmarshal(body, &payload))
					assert.NotContains(t, payload.Properties, "agentPoolProfiles", "fleet exclusively owns pool updates")
					assert.NotContains(t, payload.Properties, "kubernetesVersion", "configuration reconciliation must not downgrade or upgrade Kubernetes")
					assert.NotContains(t, parameters.Tags, provisioningTagKey, "configuration updates must not restart provisioning")

					// Simulate AKS retaining omitted pool and version properties.
					pools := live.Properties.AgentPoolProfiles
					version := live.Properties.KubernetesVersion
					live = parameters
					live.Properties.AgentPoolProfiles = pools
					live.Properties.KubernetesVersion = version
					live.Properties.ProvisioningState = ptr.To("Succeeded")
					live.ETag = ptr.To("etag-after")
					resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientCreateOrUpdateResponse{ManagedCluster: live}, nil)
					return
				},
			})
			o := &completedOptions{validatedOptions: validated, clustersClient: client}
			existing := live
			before, err := json.Marshal(existing)
			require.NoError(t, err)
			observed, err := o.ensureManagedCluster(context.Background(), &existing, nil, logr.Discard())
			require.NoError(t, err)
			require.Equal(t, live.ETag, observed.ETag, "reuse the completed update's resource version")
			require.Equal(t, 1, writes, "drift must result in a cluster update")
			after, err := json.Marshal(existing)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "building and submitting an update must not mutate the observed snapshot")
			assert.Equal(t, want.Location, live.Location, "preserve creation-only location")
			assert.Equal(t, want.Properties.DNSPrefix, live.Properties.DNSPrefix, "preserve creation-only DNS prefix")
			assert.Equal(t, want.Properties.NodeResourceGroup, live.Properties.NodeResourceGroup, "preserve creation-only node resource group")
			assert.Equal(t, validated.etcdKMSKeyURI, *live.Properties.SecurityProfile.AzureKeyVaultKms.KeyID)
			assert.Equal(t, want.Tags, live.Tags, "configuration updates preserve all live tags")
			assert.Equal(t, want.Properties.DisableLocalAccounts, live.Properties.DisableLocalAccounts)
			assert.Equal(t, want.Properties.AutoScalerProfile.ScanInterval, live.Properties.AutoScalerProfile.ScanInterval)
			assert.Equal(t, want.Properties.StorageProfile.FileCSIDriver, live.Properties.StorageProfile.FileCSIDriver)
			assert.Equal(t, want.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics, live.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics)
			assert.Equal(t, want.Properties.AddonProfiles, live.Properties.AddonProfiles, "merge owned addon settings without dropping unknown settings or addons")
			assert.Equal(t, want.Properties.APIServerAccessProfile, live.Properties.APIServerAccessProfile)
			assert.Equal(t, want.Properties.AutoScalerProfile.MaxEmptyBulkDelete, live.Properties.AutoScalerProfile.MaxEmptyBulkDelete)
			assert.Equal(t, want.Properties.NetworkProfile.LoadBalancerProfile.IdleTimeoutInMinutes, live.Properties.NetworkProfile.LoadBalancerProfile.IdleTimeoutInMinutes)
			assert.Equal(t, want.Properties.KubernetesVersion, live.Properties.KubernetesVersion)
			assert.Equal(t, want.Properties.AgentPoolProfiles, live.Properties.AgentPoolProfiles)

			existing = live
			_, err = o.ensureManagedCluster(context.Background(), &existing, nil, logr.Discard())
			require.NoError(t, err)
			assert.Equal(t, 1, writes, "a second reconciliation of the converged resource must not PUT")
		})
	}
}

func TestReconcileClusterConfigurationIgnoresUnownedConfiguration(t *testing.T) {
	validated := testValidatedOptions()
	live := testReconcileCluster(t, validated)
	live.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics = nil
	live.Tags["CLUSTERTYPE"] = live.Tags["clusterType"]
	delete(live.Tags, "clusterType")
	writes := 0
	client := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
		Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
			resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: live}, nil)
			return
		},
		BeginCreateOrUpdate: func(context.Context, string, string, armcontainerservice.ManagedCluster, *armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
			writes++
			resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientCreateOrUpdateResponse{ManagedCluster: live}, nil)
			return
		},
	})
	_, err := (&completedOptions{validatedOptions: validated, clustersClient: client}).ensureManagedCluster(context.Background(), &live, nil, logr.Discard())
	require.NoError(t, err)
	assert.Zero(t, writes, "server defaults, unrelated configuration, and newer fleet-owned pools/version must not cause PUT")
}

func TestReconcileClusterConfigurationUpdateErrors(t *testing.T) {
	tests := []struct {
		name       string
		poll       bool
		statusCode int
		errorCode  string
	}{
		{name: "concurrent update", statusCode: http.StatusPreconditionFailed, errorCode: "PreconditionFailed"},
		{name: "accepted update fails during polling", poll: true, statusCode: http.StatusBadRequest, errorCode: "ConfigurationUpdateFailed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validated := testValidatedOptions()
			live := testReconcileCluster(t, validated)
			live.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old-version")
			writes := 0
			client := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
				Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
					resp.SetResponse(http.StatusOK, armcontainerservice.ManagedClustersClientGetResponse{ManagedCluster: live}, nil)
					return
				},
				BeginCreateOrUpdate: func(_ context.Context, _, _ string, _ armcontainerservice.ManagedCluster, options *armcontainerservice.ManagedClustersClientBeginCreateOrUpdateOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientCreateOrUpdateResponse], errResp azfake.ErrorResponder) {
					writes++
					require.NotNil(t, options)
					require.Equal(t, ptr.To("etag-before"), options.IfMatch)
					if test.poll {
						resp.AddNonTerminalResponse(http.StatusCreated, nil)
						resp.SetTerminalError(test.statusCode, test.errorCode)
					} else {
						errResp.SetResponseError(test.statusCode, test.errorCode)
					}
					return
				},
			})
			_, err := (&completedOptions{validatedOptions: validated, clustersClient: client}).ensureManagedCluster(context.Background(), &live, nil, logr.Discard())
			var responseErr *azcore.ResponseError
			require.ErrorAs(t, err, &responseErr, "preserve the Azure error for callers")
			assert.Equal(t, test.statusCode, responseErr.StatusCode)
			assert.Equal(t, test.errorCode, responseErr.ErrorCode)
			assert.Equal(t, 1, writes, "do not retry a stale conditional update or a terminal failure")
		})
	}
}
