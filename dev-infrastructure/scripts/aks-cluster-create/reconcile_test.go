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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
)

// testReconcileCluster adds service defaults and configuration owned elsewhere
// to a fully provisioned cluster. Configuration updates must preserve both.
func testReconcileCluster(t *testing.T, o *validatedOptions) armcontainerservice.ManagedCluster {
	t.Helper()
	bootstrap := testSystemPool()
	cluster, _, err := o.desiredClusterSpec(nil, &bootstrap)
	require.NoError(t, err)
	cluster.Location = ptr.To("observed-region")
	cluster.Properties.DNSPrefix = ptr.To("observed-dns-prefix")
	cluster.Properties.NodeResourceGroup = ptr.To("observed-node-resource-group")
	cluster.Tags["owner"] = ptr.To("another-controller")
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

func TestDesiredClusterSpecMergesOwnedConfiguration(t *testing.T) {
	o := testValidatedOptions()
	live := testReconcileCluster(t, o)
	live.Properties.SecurityProfile.AzureKeyVaultKms.KeyID = ptr.To("https://kv1.vault.azure.net/keys/aks-etcd-encryption/old-version")
	live.Properties.DisableLocalAccounts = ptr.To(false)
	live.Properties.AutoScalerProfile.ScanInterval = ptr.To("1m")
	live.Properties.AddonProfiles["azureKeyvaultSecretsProvider"].Config["rotationPollInterval"] = ptr.To("5m")
	live.Properties.StorageProfile.FileCSIDriver = nil
	live.Properties.NetworkProfile.LoadBalancerProfile.OutboundIPs.PublicIPs = []*armcontainerservice.ResourceReference{
		{ID: ptr.To("old-outbound-ip")},
		{ID: ptr.To("extra-outbound-ip")},
	}
	live.Tags["CLUSTERTYPE"] = live.Tags["clusterType"]
	delete(live.Tags, "clusterType")
	before, err := json.Marshal(live)
	require.NoError(t, err)

	got, _, err := o.desiredClusterSpec(&live, nil)
	require.NoError(t, err)

	assert.Equal(t, ptr.To(o.etcdKMSKeyURI), got.Properties.SecurityProfile.AzureKeyVaultKms.KeyID)
	assert.Equal(t, ptr.To(true), got.Properties.DisableLocalAccounts)
	assert.Equal(t, ptr.To("10s"), got.Properties.AutoScalerProfile.ScanInterval)
	assert.Equal(t, ptr.To("1h"), got.Properties.AddonProfiles["azureKeyvaultSecretsProvider"].Config["rotationPollInterval"])
	require.NotNil(t, got.Properties.StorageProfile.FileCSIDriver)
	assert.Equal(t, ptr.To(true), got.Properties.StorageProfile.FileCSIDriver.Enabled)
	assert.Equal(t, []*armcontainerservice.ResourceReference{{ID: ptr.To(o.outboundIPResourceID)}}, got.Properties.NetworkProfile.LoadBalancerProfile.OutboundIPs.PublicIPs, "configured lists replace live lists")

	assert.Equal(t, live.Properties.AddonProfiles["anotherAddon"], got.Properties.AddonProfiles["anotherAddon"])
	assert.Equal(t, ptr.To("preserve-me"), got.Properties.AddonProfiles["azureKeyvaultSecretsProvider"].Config["service-default"])
	assert.Equal(t, live.Properties.AutoScalerProfile.MaxEmptyBulkDelete, got.Properties.AutoScalerProfile.MaxEmptyBulkDelete)
	assert.Equal(t, live.Properties.NetworkProfile.LoadBalancerProfile.IdleTimeoutInMinutes, got.Properties.NetworkProfile.LoadBalancerProfile.IdleTimeoutInMinutes)
	assert.Equal(t, live.Properties.APIServerAccessProfile, got.Properties.APIServerAccessProfile)
	assert.Equal(t, live.Location, got.Location, "location is creation-only")
	assert.Equal(t, live.Properties.DNSPrefix, got.Properties.DNSPrefix, "DNS prefix is creation-only")
	assert.Equal(t, live.Properties.NodeResourceGroup, got.Properties.NodeResourceGroup, "node resource group is creation-only")
	assert.Equal(t, live.Tags, got.Tags, "preserve live tag values and key casing")

	after, err := json.Marshal(live)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "building the deployment must not mutate its observation")
}

func TestDesiredClusterSpecOmitsPoolsAndVersionOnUpdate(t *testing.T) {
	o := testValidatedOptions()
	live := testReconcileCluster(t, o)
	bootstrap := testSystemPool()

	got, _, err := o.desiredClusterSpec(&live, &bootstrap)
	require.NoError(t, err)
	body, err := json.Marshal(got)
	require.NoError(t, err)
	var payload struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(body, &payload))
	assert.NotContains(t, payload.Properties, "agentPoolProfiles", "existing pools are managed only by child resources")
	assert.NotContains(t, payload.Properties, "kubernetesVersion", "configuration updates must not trigger upgrades or downgrades")
	assert.Equal(t, ptr.To("1.32.5"), live.Properties.KubernetesVersion)
	require.Len(t, live.Properties.AgentPoolProfiles, 1)
	assert.Equal(t, ptr.To[int32](7), live.Properties.AgentPoolProfiles[0].Count, "omitting pools must not mutate the observation")
}

func TestDesiredClusterSpecClearsMetricAllowlists(t *testing.T) {
	o := testValidatedOptions()
	live := testReconcileCluster(t, o)
	liveMetrics := live.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics
	liveMetrics.MetricLabelsAllowlist = ptr.To("pods=[*]")
	liveMetrics.MetricAnnotationsAllowList = ptr.To("namespaces=[*]")
	o.metricLabelsAllowlist = ""
	o.metricAnnotationsAllowlist = ""

	got, _, err := o.desiredClusterSpec(&live, nil)
	require.NoError(t, err)
	metrics := got.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics
	require.NotNil(t, metrics)
	body, err := json.Marshal(metrics)
	require.NoError(t, err)
	assert.JSONEq(t, `{"metricLabelsAllowlist":"","metricAnnotationsAllowList":""}`, string(body), "empty configuration must explicitly clear live values")
	assert.Equal(t, ptr.To("pods=[*]"), liveMetrics.MetricLabelsAllowlist)
	assert.Equal(t, ptr.To("namespaces=[*]"), liveMetrics.MetricAnnotationsAllowList)
}

func TestDesiredClusterSpecPreservesOmittedMetricAllowlists(t *testing.T) {
	o := testValidatedOptions()
	live := testReconcileCluster(t, o)
	live.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics = nil

	got, _, err := o.desiredClusterSpec(&live, nil)
	require.NoError(t, err)
	assert.Nil(t, got.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics, "an absent allowlist object already represents empty configuration")

	live.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics = &armcontainerservice.ManagedClusterAzureMonitorProfileKubeStateMetrics{
		MetricAnnotationsAllowList: ptr.To("namespaces=[*]"),
	}
	got, _, err = o.desiredClusterSpec(&live, nil)
	require.NoError(t, err)
	metrics := got.Properties.AzureMonitorProfile.Metrics.KubeStateMetrics
	require.NotNil(t, metrics)
	assert.Nil(t, metrics.MetricLabelsAllowlist, "an absent allowlist remains omitted when its sibling must be cleared")
	assert.Equal(t, ptr.To(""), metrics.MetricAnnotationsAllowList)
}
