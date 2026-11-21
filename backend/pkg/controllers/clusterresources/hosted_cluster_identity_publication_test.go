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

package clusterresources

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	fleetlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func buildHCUnstructured(t *testing.T) unstructured.Unstructured {
	t.Helper()
	obj := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hypershift.openshift.io/v1beta1",
			"kind":       "HostedCluster",
			"metadata":   map[string]interface{}{"name": "test-hc", "namespace": "ns"},
			"spec": map[string]interface{}{
				"platform": map[string]interface{}{
					"azure": map[string]interface{}{
						"location": "eastus", // must be preserved
						"azureAuthenticationConfig": map[string]interface{}{
							"azureAuthenticationConfigType": "ManagedIdentities",
							"managedIdentities": map[string]interface{}{
								"controlPlane": map[string]interface{}{ // must be preserved
									"cloudProvider": map[string]interface{}{"clientID": "cp-client-id"},
								},
								"dataPlane": map[string]interface{}{ // owned leaves
									"imageRegistryMSIClientID": "img-client-id",
									"diskMSIClientID":          "disk-client-id",
									"fileMSIClientID":          "file-client-id",
								},
							},
						},
					},
				},
			},
		},
	}
	return obj
}

func TestIdentityPublication(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	kaClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	clients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	clients.Register(testManagementClusterResourceID, kaClient)
	mcLister := &fleetlistertesting.SliceManagementClusterLister{ManagementClusters: []*fleetapi.ManagementCluster{{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testManagementClusterResourceID}}}}
	c := &clusterResourcesController{kubeApplierDBClients: clients, applyDesireLister: &kubeapplierlistertesting.DBApplyDesireLister{Clients: clients, Lister: mcLister}}
	spc := newSPC(testManagementClusterResourceID)
	cs := buildHCUnstructured(t)
	path := []string{"spec", "platform", "azure", "azureAuthenticationConfig", "managedIdentities", "dataPlane"}
	require.NoError(t, unstructured.SetNestedField(cs.Object, "keep-me", append(path, "futureField")...))
	raw, err := json.Marshal(cs.Object)
	require.NoError(t, err)
	resources := buildClusterResources(map[string]string{"hosted-cluster": string(raw)})
	crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)
	publish := func(source *coreapi.ServiceProviderCluster, image, disk, file string) *kubeapplierapi.ApplyDesire {
		t.Helper()
		require.NoError(t, c.processClusterResources(ctx, testKey(), source, resources, "abc123"))
		stored, err := crud.Get(ctx, DesireNameHostedCluster)
		require.NoError(t, err)
		var obj unstructured.Unstructured
		require.NoError(t, json.Unmarshal(stored.Spec.ServerSideApply.KubeContent.Raw, &obj.Object))
		dp, found, err := unstructured.NestedStringMap(obj.Object, path...)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, map[string]string{"imageRegistryMSIClientID": image, "diskMSIClientID": disk, "fileMSIClientID": file, "futureField": "keep-me"}, dp)
		return stored
	}
	spc.InstanceVersion = 10
	stored := publish(spc, "img-client-id", "disk-client-id", "file-client-id")
	require.Equal(t, "10", stored.Tags[hostedClusterSourceVersionTag])
	hc := &v1beta1.HostedCluster{}
	hc.Spec.Platform.Azure = &v1beta1.AzurePlatformSpec{}
	mi := &v1beta1.AzureResourceManagedIdentities{DataPlane: v1beta1.DataPlaneManagedIdentities{ImageRegistryMSIClientID: "rp-img", DiskMSIClientID: "rp-disk"}}
	hc.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities = mi
	spc.Spec.DesiredHostedCluster = hc
	spc.InstanceVersion++
	partial := spc.DeepCopy()
	publish(partial, "img-client-id", "disk-client-id", "file-client-id")
	spc.InstanceVersion++
	mi.DataPlane.FileMSIClientID = "rp-file"
	// A complete snapshot arriving after a newer fallback must leave the CS IDs in place.
	staleComplete := spc.DeepCopy()
	staleComplete.InstanceVersion = partial.InstanceVersion - 1
	stored = publish(staleComplete, "img-client-id", "disk-client-id", "file-client-id")
	require.Equal(t, "11", stored.Tags[hostedClusterSourceVersionTag])
	stored = publish(spc, "rp-img", "rp-disk", "rp-file")
	require.Equal(t, "12", stored.Tags[hostedClusterSourceVersionTag])
	stored.Status.AppliedKubeGeneration = ptr.To(int64(3))
	_, err = crud.Replace(ctx, stored, nil)
	require.NoError(t, err)
	spc.InstanceVersion++
	stored = publish(spc, "rp-img", "rp-disk", "rp-file")
	require.Equal(t, ptr.To(int64(3)), stored.Status.AppliedKubeGeneration)
	// If the desire cache also lags, its ETag prevents the stale write.
	previouslyPublished := stored.DeepCopy()
	spc.InstanceVersion++
	mi.DataPlane.DiskMSIClientID = "older-disk"
	older := spc.DeepCopy()
	spc.InstanceVersion++
	mi.DataPlane.DiskMSIClientID = "rotated-disk"
	stored = publish(spc, "rp-img", "rotated-disk", "rp-file")
	liveLister := c.applyDesireLister
	c.applyDesireLister = &kubeapplierlistertesting.SliceApplyDesireLister{Desires: []*kubeapplierapi.ApplyDesire{previouslyPublished}}
	err = c.processClusterResources(ctx, testKey(), older, resources, "abc123")
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(err))
	c.applyDesireLister = liveLister
	stale := publish(partial, "rp-img", "rotated-disk", "rp-file")
	require.Equal(t, stored.InstanceVersion, stale.InstanceVersion)
	partial.InstanceVersion = spc.InstanceVersion + 1
	publish(partial, "img-client-id", "disk-client-id", "file-client-id")
}
