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

package ocm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
)

const testAutoscalerClusterID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// getClusterViaFakeClusterService stands up an httptest server that emulates
// Cluster Service, serves the given cluster on its GET endpoint, and calls
// GetCluster against it. It returns the cluster GetCluster produced along with
// the list of paths the server was asked for, so callers can assert how many
// (and which) Cluster Service requests GetCluster issued.
//
// Any GET other than the cluster itself is answered with a valid (empty)
// ClusterAutoscaler. This means that if GetCluster were to resolve an autoscaler
// link (the behavior resolveClusterLinks used to provide), that follow-up
// request would succeed rather than error — so it surfaces cleanly as an extra
// recorded request instead of a confusing GetCluster failure.
func getClusterViaFakeClusterService(t *testing.T, internalID metadataapi.InternalID, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, []string) {
	t.Helper()

	// An empty-but-valid autoscaler served as the target of any link resolution.
	linkTargetAutoscaler, err := arohcpv1alpha1.NewClusterAutoscaler().Build()
	require.NoError(t, err, "failed to build link-target autoscaler")

	var (
		mu            sync.Mutex
		requestedURLs []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestedURLs = append(requestedURLs, r.URL.Path)
		mu.Unlock()

		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method: "+r.Method, http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == internalID.Path() {
			if err := arohcpv1alpha1.MarshalCluster(cluster, w); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		// Any other GET is treated as an autoscaler-link resolution target.
		if err := arohcpv1alpha1.MarshalClusterAutoscaler(linkTargetAutoscaler, w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	conn, err := sdk.NewUnauthenticatedConnectionBuilder().
		URL(server.URL).
		Build()
	require.NoError(t, err, "failed to build Cluster Service connection")
	defer conn.Close()

	csc := &clusterServiceClient{conn: conn}

	got, err := csc.GetCluster(context.Background(), internalID)
	require.NoError(t, err, "GetCluster returned an error")
	require.NotNil(t, got, "GetCluster returned a nil cluster")

	mu.Lock()
	defer mu.Unlock()
	// Return a copy so callers can read it without holding the lock.
	urls := append([]string(nil), requestedURLs...)
	return got, urls
}

// TestGetClusterReturnsEmbeddedAutoscaler verifies that GetCluster returns the
// cluster autoscaler configuration directly from the Cluster Service GET cluster
// response. Cluster Service now embeds the full autoscaler object in the GET
// cluster response, so a single request must yield a fully-populated, non-link
// autoscaler.
func TestGetClusterReturnsEmbeddedAutoscaler(t *testing.T) {
	const (
		maxNodeProvisionTime = "15m"
		maxPodGracePeriod    = 700
		podPriorityThreshold = -11
		maxNodesTotal        = 3
	)

	internalID := metadataapi.Must(metadataapi.NewInternalID(GenerateAROHCPClusterHREF(testAutoscalerClusterID)))

	// Build a cluster whose autoscaler is a full, embedded object (not a link).
	cluster, err := arohcpv1alpha1.NewCluster().
		ID(testAutoscalerClusterID).
		HREF(internalID.Path()).
		Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
			MaxNodeProvisionTime(maxNodeProvisionTime).
			MaxPodGracePeriod(maxPodGracePeriod).
			PodPriorityThreshold(podPriorityThreshold).
			ResourceLimits(arohcpv1alpha1.NewAutoscalerResourceLimits().
				MaxNodesTotal(maxNodesTotal))).
		Build()
	require.NoError(t, err, "failed to build embedded-autoscaler cluster fixture")

	got, requestedURLs := getClusterViaFakeClusterService(t, internalID, cluster)

	autoscaler, ok := got.GetAutoscaler()
	require.True(t, ok, "GetCluster response did not include an autoscaler")
	require.NotNil(t, autoscaler, "autoscaler was nil")

	// The autoscaler must be a full object, not an unresolved link.
	assert.False(t, autoscaler.Link(), "autoscaler should be a full embedded object, not a link")

	assert.Equal(t, maxNodeProvisionTime, autoscaler.MaxNodeProvisionTime(), "autoscaler MaxNodeProvisionTime mismatch")
	assert.Equal(t, maxPodGracePeriod, autoscaler.MaxPodGracePeriod(), "autoscaler MaxPodGracePeriod mismatch")
	assert.Equal(t, podPriorityThreshold, autoscaler.PodPriorityThreshold(), "autoscaler PodPriorityThreshold mismatch")

	resourceLimits, ok := autoscaler.GetResourceLimits()
	require.True(t, ok, "autoscaler did not include resource limits")
	assert.Equal(t, maxNodesTotal, resourceLimits.MaxNodesTotal(), "autoscaler ResourceLimits.MaxNodesTotal mismatch")

	// A single cluster GET fully populated the autoscaler.
	require.Len(t, requestedURLs, 1, "expected exactly one Cluster Service request (the cluster GET), got: %v", requestedURLs)
	assert.Equal(t, internalID.Path(), requestedURLs[0], "the only request should be the cluster GET")
}

// TestGetClusterDoesNotResolveAutoscalerLink is the regression guard for the
// removal of resolveClusterLinks from GetCluster. It feeds GetCluster a cluster
// whose autoscaler is a *link* (href only, no embedded fields) and asserts that
// GetCluster returns it as-is without issuing a second request to resolve the
// link. If resolveClusterLinks were reintroduced, it would detect the link and
// fetch the HREF, producing an extra recorded request and failing this test.
func TestGetClusterDoesNotResolveAutoscalerLink(t *testing.T) {
	internalID := metadataapi.Must(metadataapi.NewInternalID(GenerateAROHCPClusterHREF(testAutoscalerClusterID)))
	autoscalerHREF := internalID.Path() + "/autoscaler"

	// Build a cluster whose autoscaler is a link rather than an embedded object.
	cluster, err := arohcpv1alpha1.NewCluster().
		ID(testAutoscalerClusterID).
		HREF(internalID.Path()).
		Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
			Link(true).
			HREF(autoscalerHREF)).
		Build()
	require.NoError(t, err, "failed to build linked-autoscaler cluster fixture")

	// Sanity-check the fixture itself: if it is not a link, resolveClusterLinks
	// would never have resolved it and this test would guard nothing.
	fixtureAutoscaler, ok := cluster.GetAutoscaler()
	require.True(t, ok && fixtureAutoscaler.Link(), "test fixture autoscaler must be a link")

	got, requestedURLs := getClusterViaFakeClusterService(t, internalID, cluster)

	// The core regression assertion: GetCluster must not have followed the
	// autoscaler link. Reintroducing resolveClusterLinks would add a second
	// request here.
	require.Len(t, requestedURLs, 1, "GetCluster must not resolve the autoscaler link; expected only the cluster GET, got: %v", requestedURLs)
	assert.Equal(t, internalID.Path(), requestedURLs[0], "the only request should be the cluster GET")
	assert.NotContains(t, requestedURLs, autoscalerHREF, "GetCluster unexpectedly resolved the autoscaler link")

	// The link is passed through to the caller unresolved.
	autoscaler, ok := got.GetAutoscaler()
	require.True(t, ok, "GetCluster response did not include an autoscaler")
	assert.True(t, autoscaler.Link(), "GetCluster should pass the autoscaler link through unresolved")
}
