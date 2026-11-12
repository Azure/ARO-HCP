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
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/kubeapplierapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	fleetlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestHostedClusterDesireSourceOrdering(t *testing.T) {
	t.Parallel()
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	crud, err := client.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)

	require.NoError(t, ensureHostedClusterApplyDesire(ctx, crud, identityPublicationDesire("10", `{"clientID":"new"}`)))
	require.NoError(t, ensureHostedClusterApplyDesire(ctx, crud, identityPublicationDesire("9", `{"clientID":"old"}`)))
	got, err := crud.Get(ctx, strings.ToLower(DesireNameHostedCluster))
	require.NoError(t, err)
	assert.JSONEq(t, `{"clientID":"new"}`, string(got.Spec.ServerSideApply.KubeContent.Raw))
	assert.Equal(t, "10", got.Tags[hostedClusterSourceVersionTag])
	got.Status.AppliedKubeGeneration = ptr.To(int64(3))
	_, err = crud.Replace(ctx, got, nil)
	require.NoError(t, err)
	require.NoError(t, ensureHostedClusterApplyDesire(ctx, crud, identityPublicationDesire("11", `{"clientID":"new"}`)))
	got, err = crud.Get(ctx, strings.ToLower(DesireNameHostedCluster))
	require.NoError(t, err)
	assert.Equal(t, "11", got.Tags[hostedClusterSourceVersionTag])
	assert.Equal(t, ptr.To(int64(3)), got.Status.AppliedKubeGeneration)
	require.Error(t, ensureHostedClusterApplyDesire(ctx, crud, identityPublicationDesire("bad", `{}`)))
}
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

func TestOverlayHostedClusterDataPlaneIdentityLeaves(t *testing.T) {
	t.Parallel()
	obj := buildHCUnstructured(t)
	err := kubeapplierhelpers.OverlayHostedClusterDataPlaneClientIDs(&obj,
		kubeapplierhelpers.HostedClusterDataPlaneClientIDs{
			ImageRegistryMSIClientID: "new-img", DiskMSIClientID: "new-disk", FileMSIClientID: "new-file",
		})
	require.NoError(t, err)
	dp, found, err := unstructured.NestedStringMap(obj.Object,
		"spec", "platform", "azure", "azureAuthenticationConfig", "managedIdentities", "dataPlane")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "new-img", dp["imageRegistryMSIClientID"])
	assert.Equal(t, "new-disk", dp["diskMSIClientID"])
	assert.Equal(t, "new-file", dp["fileMSIClientID"])
	location, _, err := unstructured.NestedString(obj.Object, "spec", "platform", "azure", "location")
	require.NoError(t, err)
	assert.Equal(t, "eastus", location)
	controlPlane, found, err := unstructured.NestedFieldNoCopy(obj.Object,
		"spec", "platform", "azure", "azureAuthenticationConfig", "managedIdentities", "controlPlane")
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotNil(t, controlPlane)
}

func TestClusterResourcesPublishesOnlyOwnedIdentityLeaves(t *testing.T) {
	t.Parallel()
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	spc := newSPC(testManagementClusterResourceID)
	spc.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP = false
	spc.Spec.DesiredHostedCluster = &v1beta1.HostedCluster{}
	require.NoError(t, kubeapplierhelpers.SetDesiredHostedClusterDataPlaneClientIDs(
		spc.Spec.DesiredHostedCluster, kubeapplierhelpers.HostedClusterDataPlaneClientIDs{
			ImageRegistryMSIClientID: "new-img", DiskMSIClientID: "new-disk", FileMSIClientID: "new-file",
		}))
	spc.Spec.DesiredHostedCluster.Annotations = map[string]string{"other-controller": "not-an-overlay"}
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{spc})
	require.NoError(t, err)
	kaClient := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	kaClients := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients()
	kaClients.Register(testManagementClusterResourceID, kaClient)
	mcLister := &fleetlistertesting.SliceManagementClusterLister{
		ManagementClusters: []*fleetapi.ManagementCluster{{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: testManagementClusterResourceID}}},
	}
	controller := &clusterResourcesController{
		resourcesDBClient:    db,
		kubeApplierDBClients: kaClients,
		applyDesireLister:    &kubeapplierlistertesting.DBApplyDesireLister{Clients: kaClients, Lister: mcLister},
	}
	csHC := buildHCUnstructured(t)
	raw, err := json.Marshal(csHC.Object)
	require.NoError(t, err)
	resources := buildClusterResources(map[string]string{"hosted-cluster": string(raw)})

	// Partial identity intent without the durable marker must not override CS.
	require.NoError(t, controller.processClusterResources(ctx, testKey(), testManagementClusterResourceID, resources, "abc123"))
	beforeCRUD, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)
	before, err := beforeCRUD.Get(ctx, DesireNameHostedCluster)
	require.NoError(t, err)
	var original v1beta1.HostedCluster
	require.NoError(t, json.Unmarshal(before.Spec.ServerSideApply.KubeContent.Raw, &original))
	assert.Equal(t, "img-client-id", kubeapplierhelpers.DataPlaneClientIDsFromHostedCluster(&original).ImageRegistryMSIClientID)
	spcCRUD := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
	owned, err := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	owned.Spec.HostedClusterDataPlaneIdentitiesOwnedByRP = true
	_, err = spcCRUD.Replace(ctx, owned, nil)
	require.NoError(t, err)
	require.NoError(t, controller.processClusterResources(ctx, testKey(), testManagementClusterResourceID, resources, "abc123"))
	crud, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)
	desire, err := crud.Get(ctx, strings.ToLower(DesireNameHostedCluster))
	require.NoError(t, err)
	var published unstructured.Unstructured
	require.NoError(t, json.Unmarshal(desire.Spec.ServerSideApply.KubeContent.Raw, &published.Object))
	ids, _, err := unstructured.NestedStringMap(published.Object,
		"spec", "platform", "azure", "azureAuthenticationConfig", "managedIdentities", "dataPlane")
	require.NoError(t, err)
	assert.Equal(t, "new-img", ids["imageRegistryMSIClientID"])
	assert.Equal(t, "new-disk", ids["diskMSIClientID"])
	assert.Equal(t, "new-file", ids["fileMSIClientID"])
	assert.Empty(t, published.GetAnnotations()["other-controller"], "other DesiredHostedCluster leaves must not be overlaid")
	location, _, err := unstructured.NestedString(published.Object, "spec", "platform", "azure", "location")
	require.NoError(t, err)
	assert.Equal(t, "eastus", location)

	// Losing one owned leaf must retain the last complete manifest; it must
	// never revive the stale Cluster Service IDs or delete the desire.
	currentSPC, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).
		Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.NoError(t, err)
	broken := currentSPC.DeepCopy()
	broken.Spec.DesiredHostedCluster.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.DataPlane.FileMSIClientID = ""
	_, err = db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Replace(ctx, broken, nil)
	require.NoError(t, err)
	require.ErrorContains(t, controller.processClusterResources(ctx, testKey(), testManagementClusterResourceID, resources, "abc123"), "incomplete")
	retained, err := crud.Get(ctx, strings.ToLower(DesireNameHostedCluster))
	require.NoError(t, err)
	assert.Equal(t, desire.InstanceVersion, retained.InstanceVersion)
}

func identityPublicationDesire(version, content string) *kubeapplierapi.ApplyDesire {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(kubeapplierapihelpers.ToClusterScopedApplyDesireResourceIDString(
		testSubscriptionID, testResourceGroupName, testClusterName, DesireNameHostedCluster)))
	return &kubeapplierapi.ApplyDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(testManagementClusterResourceID.String())},
		Spec: kubeapplierapi.ApplyDesireSpec{
			ManagementCluster: testManagementClusterResourceID,
			Type:              kubeapplierapi.ApplyDesireTypeServerSideApply,
			TargetItem:        kubeapplierapi.ResourceReference{Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters", Name: "hc", Namespace: "ns"},
			ServerSideApply:   &kubeapplierapi.ServerSideApplyConfig{KubeContent: &runtime.RawExtension{Raw: []byte(content)}},
		},
		Tags: map[string]string{kubeapplierapi.TagControllerName: ClusterResourcesControllerName, hostedClusterSourceVersionTag: version},
	}
}

// Inject a real competing write between GET and Replace. The mock enforces
// ETags, so this verifies the retry reads and respects the winner's version.
type identityPublicationConflictCRUD struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]
	beforeReplace func(context.Context) error
}

func (c *identityPublicationConflictCRUD) Replace(ctx context.Context, d *kubeapplierapi.ApplyDesire, o *azcosmos.ItemOptions) (*kubeapplierapi.ApplyDesire, error) {
	if c.beforeReplace != nil {
		f := c.beforeReplace
		c.beforeReplace = nil
		if err := f(ctx); err != nil {
			return nil, err
		}
	}
	return c.ResourceCRUD.Replace(ctx, d, o)
}

// Simulate a Cosmos create conflict after PrepareForCreate mutates the candidate.
// The next read still reports not found, so publication must retry with version 0.
type identityPublicationCreateConflictCRUD struct {
	cosmosstorageutils.ResourceCRUD[kubeapplierapi.ApplyDesire, *kubeapplierapi.ApplyDesire]
	createCalls int
}

func (c *identityPublicationCreateConflictCRUD) Create(ctx context.Context, candidate *kubeapplierapi.ApplyDesire, options *azcosmos.ItemOptions) (*kubeapplierapi.ApplyDesire, error) {
	c.createCalls++
	if c.createCalls == 1 {
		if err := cosmosstorageutils.PrepareForCreate(candidate); err != nil {
			return nil, err
		}
		return nil, &azcore.ResponseError{StatusCode: http.StatusConflict}
	}
	return c.ResourceCRUD.Create(ctx, candidate, options)
}

func TestHostedClusterPublisherRetriesCreateConflict(t *testing.T) {
	t.Parallel()
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	crud, err := client.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)

	conflicting := &identityPublicationCreateConflictCRUD{ResourceCRUD: crud}
	candidate := identityPublicationDesire("10", `{"clientID":"new"}`)
	require.NoError(t, ensureHostedClusterApplyDesire(ctx, conflicting, candidate))
	assert.Equal(t, 2, conflicting.createCalls)
	assert.Zero(t, candidate.InstanceVersion, "the source candidate must remain reusable")

	got, err := crud.Get(ctx, DesireNameHostedCluster)
	require.NoError(t, err)
	assert.Equal(t, "10", got.Tags[hostedClusterSourceVersionTag])
	assert.JSONEq(t, `{"clientID":"new"}`, string(got.Spec.ServerSideApply.KubeContent.Raw))
}

func TestHostedClusterPublisherRejectsStaleConcurrentWrite(t *testing.T) {
	t.Parallel()
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
	crud, err := client.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
	require.NoError(t, err)
	require.NoError(t, ensureHostedClusterApplyDesire(ctx, crud, identityPublicationDesire("8", `{"id":"initial"}`)))
	conflicting := &identityPublicationConflictCRUD{ResourceCRUD: crud, beforeReplace: func(ctx context.Context) error {
		return ensureHostedClusterApplyDesire(ctx, crud, identityPublicationDesire("10", `{"id":"winner"}`))
	}}
	require.NoError(t, ensureHostedClusterApplyDesire(ctx, conflicting, identityPublicationDesire("9", `{"id":"stale"}`)))
	got, err := crud.Get(ctx, DesireNameHostedCluster)
	require.NoError(t, err)
	assert.Equal(t, "10", got.Tags[hostedClusterSourceVersionTag])
	assert.JSONEq(t, `{"id":"winner"}`, string(got.Spec.ServerSideApply.KubeContent.Raw))
}
func TestHostedClusterPublisherFailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*kubeapplierapi.ApplyDesire)
	}{
		{"different owner", func(d *kubeapplierapi.ApplyDesire) { d.Tags[kubeapplierapi.TagControllerName] = "another-controller" }},
		{"invalid stored version", func(d *kubeapplierapi.ApplyDesire) { d.Tags[hostedClusterSourceVersionTag] = "corrupt" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			client := kubeappliercosmosstoragetesting.NewMockKubeApplierDBClient()
			crud, err := client.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
			require.NoError(t, err)
			initial := identityPublicationDesire("10", `{"id":"keep"}`)
			tc.mutate(initial)
			created, err := crud.Create(ctx, initial, nil)
			require.NoError(t, err)
			require.Error(t, ensureHostedClusterApplyDesire(ctx, crud, identityPublicationDesire("11", `{"id":"bad"}`)))
			got, err := crud.Get(ctx, DesireNameHostedCluster)
			require.NoError(t, err)
			assert.Equal(t, created.InstanceVersion, got.InstanceVersion)
		})
	}
}
