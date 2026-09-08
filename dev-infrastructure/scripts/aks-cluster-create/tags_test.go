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
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	armcontainerservicefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8/fake"
)

func TestProvisioningMarkerPresence(t *testing.T) {
	for _, test := range []struct {
		name string
		tags map[string]*string
		want bool
	}{
		{name: "absent"},
		{name: "unrelated", tags: map[string]*string{"other": ptr.To("true")}},
		{name: "true", tags: map[string]*string{provisioningTagKey: ptr.To("true")}, want: true},
		{name: "false", tags: map[string]*string{provisioningTagKey: ptr.To("false")}},
		{name: "empty", tags: map[string]*string{provisioningTagKey: ptr.To("")}},
		{name: "nil", tags: map[string]*string{provisioningTagKey: nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, hasProvisioningTag(test.tags))
		})
	}
}

func TestInitialClusterTags(t *testing.T) {
	o := testValidatedOptions()
	o.clusterTags = map[string]string{
		"owningTeam": "dedicated-team", "custom": "value", "ARO HCP": "ordinary",
	}
	require.Equal(t, map[string]*string{
		"owningTeam": ptr.To("dedicated-team"), "custom": ptr.To("value"),
		"ARO HCP": ptr.To("ordinary"), provisioningTagKey: ptr.To(provisioningTagValue),
	}, initialClusterTags(o.clusterTags))
}

type tagReconcileFixture struct {
	o            *completedOptions
	cluster      *armcontainerservice.ManagedCluster
	writes       int
	written      map[string]*string
	updateStatus int
}

// newTagReconcileFixture builds a fully provisioned cluster that still carries
// the provisioning marker, so reconcileClusterTags finalizes the handover.
func newTagReconcileFixture(t *testing.T) *tagReconcileFixture {
	t.Helper()
	f := &tagReconcileFixture{
		cluster: &armcontainerservice.ManagedCluster{
			ETag: ptr.To("observed-etag"),
			Tags: map[string]*string{provisioningTagKey: ptr.To("true"), "external": ptr.To("keep")},
			Properties: &armcontainerservice.ManagedClusterProperties{
				ProvisioningState: ptr.To("Succeeded"),
				AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
					{Name: ptr.To("system"), ProvisioningState: ptr.To("Succeeded")},
					{Name: ptr.To("worker"), ProvisioningState: ptr.To("Succeeded")},
				},
			},
		},
	}
	clustersClient := newManagedClustersTestClient(t, &armcontainerservicefake.ManagedClustersServer{
		Get: func(context.Context, string, string, *armcontainerservice.ManagedClustersClientGetOptions) (resp azfake.Responder[armcontainerservice.ManagedClustersClientGetResponse], errResp azfake.ErrorResponder) {
			t.Fatal("tag reconciliation must use its supplied cluster snapshot")
			return
		},
		BeginUpdateTags: func(_ context.Context, _, _ string, parameters armcontainerservice.TagsObject, options *armcontainerservice.ManagedClustersClientBeginUpdateTagsOptions) (resp azfake.PollerResponder[armcontainerservice.ManagedClustersClientUpdateTagsResponse], errResp azfake.ErrorResponder) {
			f.writes++
			require.NotNil(t, options)
			require.Equal(t, "observed-etag", ptr.Deref(options.IfMatch, ""))
			f.written = parameters.Tags
			if f.updateStatus != 0 {
				errResp.SetResponseError(f.updateStatus, "ConcurrentUpdate")
				return
			}
			resp.SetTerminalResponse(http.StatusOK, armcontainerservice.ManagedClustersClientUpdateTagsResponse{}, nil)
			return
		},
	})
	f.o = &completedOptions{validatedOptions: testValidatedOptions(), clustersClient: clustersClient}
	f.o.clusterTags = nil
	return f
}

// TestReconcileClusterTagsFinalizesHandover clears the provisioning marker and
// overlays configured tags in one write once the cluster is provisioned, while
// preserving tags this tool does not manage.
func TestReconcileClusterTagsFinalizesHandover(t *testing.T) {
	f := newTagReconcileFixture(t)
	f.o.clusterTags = map[string]string{"owner": "new-team"}
	require.NoError(t, f.o.reconcileClusterTags(context.Background(), f.cluster))
	require.Equal(t, 1, f.writes)
	require.NotContains(t, f.written, provisioningTagKey)
	require.Equal(t, "keep", *f.written["external"])
	require.Equal(t, "new-team", *f.written["owner"])
	require.True(t, hasProvisioningTag(f.cluster.Tags), "pre-update snapshot remains intact")
}

// TestReconcileClusterTagsClearsMarkerWithoutConfigChange writes solely to drop
// the provisioning marker even when no configured tag changed.
func TestReconcileClusterTagsClearsMarkerWithoutConfigChange(t *testing.T) {
	f := newTagReconcileFixture(t)
	require.NoError(t, f.o.reconcileClusterTags(context.Background(), f.cluster))
	require.Equal(t, 1, f.writes)
	require.NotContains(t, f.written, provisioningTagKey)
	require.Equal(t, "keep", *f.written["external"])
}

// TestReconcileClusterTagsSkipsSettledCluster performs no write once the marker
// is gone and the configured tags already match.
func TestReconcileClusterTagsSkipsSettledCluster(t *testing.T) {
	f := newTagReconcileFixture(t)
	delete(f.cluster.Tags, provisioningTagKey)
	f.cluster.Tags["owner"] = ptr.To("team")
	f.o.clusterTags = map[string]string{"owner": "team"}
	require.NoError(t, f.o.reconcileClusterTags(context.Background(), f.cluster))
	require.Zero(t, f.writes)
}

// TestReconcileClusterTagsRejectsUnsafeFinalization refuses to clear the marker
// (or write anything) until the cluster and all pools report success, and never
// mutates the caller's snapshot.
func TestReconcileClusterTagsRejectsUnsafeFinalization(t *testing.T) {
	for _, test := range []struct {
		name    string
		alter   func(*tagReconcileFixture)
		wantErr string
	}{
		{name: "cluster busy", alter: func(f *tagReconcileFixture) { f.cluster.Properties.ProvisioningState = ptr.To("Updating") }, wantErr: "cluster must be successfully provisioned"},
		{name: "missing cluster properties", alter: func(f *tagReconcileFixture) { f.cluster.Properties = nil }, wantErr: "cluster must be successfully provisioned"},
		{name: "later pool busy", alter: func(f *tagReconcileFixture) {
			f.cluster.Properties.AgentPoolProfiles[1].ProvisioningState = ptr.To("Updating")
		}, wantErr: "pool \"worker\" must be successfully provisioned"},
		{name: "pool missing provisioning state", alter: func(f *tagReconcileFixture) { f.cluster.Properties.AgentPoolProfiles[1].ProvisioningState = nil }, wantErr: "must be successfully provisioned"},
		{name: "nil pool", alter: func(f *tagReconcileFixture) { f.cluster.Properties.AgentPoolProfiles[1] = nil }, wantErr: "missing pool observation"},
		{name: "nil ETag", alter: func(f *tagReconcileFixture) { f.cluster.ETag = nil }, wantErr: "no ETag"},
		{name: "empty ETag", alter: func(f *tagReconcileFixture) { f.cluster.ETag = ptr.To("") }, wantErr: "no ETag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTagReconcileFixture(t)
			test.alter(f)
			marked := hasProvisioningTag(f.cluster.Tags)
			require.ErrorContains(t, f.o.reconcileClusterTags(context.Background(), f.cluster), test.wantErr)
			require.Zero(t, f.writes, "unsafe observations must not remove markers or partially update tags")
			require.Equal(t, marked, hasProvisioningTag(f.cluster.Tags))
		})
	}
}

func TestReconcileClusterTagsPropagatesConflicts(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusPreconditionFailed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newTagReconcileFixture(t)
			f.updateStatus = status
			f.o.clusterTags = map[string]string{"owner": "new-team"}
			err := f.o.reconcileClusterTags(context.Background(), f.cluster)
			var responseErr *azcore.ResponseError
			require.ErrorAs(t, err, &responseErr)
			require.Equal(t, status, responseErr.StatusCode)
			require.Equal(t, 1, f.writes, "the outer lifecycle owns retries from a fresh read")
		})
	}
}
