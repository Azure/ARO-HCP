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

package corecosmosstoragetesting

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

func TestStackDesireStorage(t *testing.T) {
	ctx := t.Context()
	id, err := azcorearm.ParseResourceID("/subscriptions/Sub/resourceGroups/RG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/Cluster/stackDesires/Desire")
	require.NoError(t, err)
	target, err := azcorearm.ParseResourceID("/subscriptions/other/providers/Microsoft.Resources/deploymentStacks/stack")
	require.NoError(t, err)
	desire := &coreapi.StackDesire{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: "sub"},
		Spec:           coreapi.StackDesireSpec{TargetResourceID: target, Action: coreapi.StackDesireActionApply, Authentication: coreapi.StackDesireAuthentication{Type: coreapi.StackDesireAuthenticationTypeFPA}, Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{Template: map[string]any{}}}},
	}
	db, err := NewMockResourcesDBClientWithResources(ctx, []any{desire})
	require.NoError(t, err, "mock initialization must accept StackDesire")
	crud := db.StackDesires("SUB", "rg", "CLUSTER")
	stored, err := crud.Get(ctx, "DESIRE")
	require.NoError(t, err)
	require.Equal(t, "sub", stored.GetPartitionKey(), "partition belongs to the HCP cluster, not the Azure target")
	require.Equal(t, strings.ToLower(id.Parent.String()), strings.ToLower(stored.ParentResourceID.String()))
	require.NotEmpty(t, stored.CosmosETag)
	require.Equal(t, desire.Spec.Stack, stored.Spec.Stack)
	before := stored.DeepCopy()
	stored.Status.ObservedAt = ptr.To(metav1.NewTime(time.Now().UTC().Truncate(time.Second)))
	stored.Status.Stack = &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{CorrelationID: ptr.To("correlation"), ProvisioningState: ptr.To(armdeploymentstacks.DeploymentStackProvisioningStateSucceeded)}}
	updated, err := crud.Replace(ctx, stored, nil)
	require.NoError(t, err)
	require.Equal(t, before.InstanceVersion+1, updated.InstanceVersion)
	require.NotEqual(t, before.CosmosETag, updated.CosmosETag)
	require.Equal(t, "correlation", *updated.Status.Stack.Properties.CorrelationID)
	require.Equal(t, stored.Status.Stack, updated.Status.Stack)
	require.True(t, stored.Status.ObservedAt.Equal(updated.Status.ObservedAt), "observation time must survive storage")
	_, err = crud.Replace(ctx, before, nil)
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "stale writers must not overwrite the outcome")
	updated.Spec.Action = coreapi.StackDesireActionDelete
	updated.Spec.Stack.Properties.ActionOnUnmanage = &armdeploymentstacks.ActionOnUnmanage{Resources: ptr.To(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete)}
	updated.Spec.Stack.Properties.BypassStackOutOfSyncError = ptr.To(true)
	_, err = crud.Replace(ctx, updated, nil)
	require.NoError(t, err, "switching action must preserve target identity")
	iter, err := db.ResourcesGlobalListers().StackDesires().List(ctx, nil)
	require.NoError(t, err)
	count := 0
	for _, item := range iter.Items(ctx) {
		count++
		require.Equal(t, coreapi.StackDesireActionDelete, item.Spec.Action)
		require.Equal(t, updated.Spec.Stack, item.Spec.Stack, "delete policy must survive storage in the SDK stack")
	}
	require.NoError(t, iter.GetError())
	require.Equal(t, 1, count)
	other, err := db.StackDesires("sub", "rg", "other").List(ctx, nil)
	require.NoError(t, err)
	for range other.Items(ctx) {
		t.Fatal("cluster-scoped list returned another cluster's desire")
	}
	require.NoError(t, other.GetError())
	require.NoError(t, crud.Delete(ctx, "desire"))
	_, err = crud.Get(ctx, "desire")
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
	iter, err = db.ResourcesGlobalListers().StackDesires().List(ctx, nil)
	require.NoError(t, err)
	for range iter.Items(ctx) {
		t.Fatal("deleted desires must no longer be managed")
	}
	require.NoError(t, iter.GetError())
}
