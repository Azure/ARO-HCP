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
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/metadataapi"
)

// TestGetClusterReturnsEmbeddedAutoscaler verifies that GetCluster returns the
// cluster autoscaler configuration directly from the Cluster Service GET cluster
// response, without issuing a second request to resolve an autoscaler "link"
// (the behavior that resolveClusterLinks used to provide). Cluster Service now
// embeds the full autoscaler object in the GET cluster response, so a single
// request must yield a fully-populated, non-link autoscaler.
func TestGetClusterReturnsEmbeddedAutoscaler(t *testing.T) {
	const (
		clusterID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

		maxNodeProvisionTime = "15m"
		maxPodGracePeriod    = 700
		podPriorityThreshold = -11
		maxNodesTotal        = 3
	)

	internalID := metadataapi.Must(metadataapi.NewInternalID(GenerateAROHCPClusterHREF(clusterID)))

	// Build a cluster whose autoscaler is a full, embedded object (not a link).
	cluster, err := arohcpv1alpha1.NewCluster().
		ID(clusterID).
		HREF(internalID.Path()).
		Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().
			MaxNodeProvisionTime(maxNodeProvisionTime).
			MaxPodGracePeriod(maxPodGracePeriod).
			PodPriorityThreshold(podPriorityThreshold).
			ResourceLimits(arohcpv1alpha1.NewAutoscalerResourceLimits().
				MaxNodesTotal(maxNodesTotal))).
		Build()
	require.NoError(t, err, "failed to build embedded-autoscaler cluster fixture")

	// Record every path Cluster Service is asked for so we can prove GetCluster
	// makes exactly one request (the cluster GET) and never resolves a link.
	var (
		mu            sync.Mutex
		requestedURLs []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestedURLs = append(requestedURLs, r.URL.Path)
		mu.Unlock()

		if r.Method == http.MethodGet && r.URL.Path == internalID.Path() {
			w.Header().Set("Content-Type", "application/json")
			if err := arohcpv1alpha1.MarshalCluster(cluster, w); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusNotFound)
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

	autoscaler, ok := got.GetAutoscaler()
	require.True(t, ok, "GetCluster response did not include an autoscaler")
	require.NotNil(t, autoscaler, "autoscaler was nil")

	// The autoscaler must be a full object, not an unresolved link. If it were a
	// link, every field below would be empty and resolveClusterLinks would have
	// been required to populate it.
	assert.False(t, autoscaler.Link(), "autoscaler should be a full embedded object, not a link")

	assert.Equal(t, maxNodeProvisionTime, autoscaler.MaxNodeProvisionTime(), "autoscaler MaxNodeProvisionTime mismatch")
	assert.Equal(t, maxPodGracePeriod, autoscaler.MaxPodGracePeriod(), "autoscaler MaxPodGracePeriod mismatch")
	assert.Equal(t, podPriorityThreshold, autoscaler.PodPriorityThreshold(), "autoscaler PodPriorityThreshold mismatch")

	resourceLimits, ok := autoscaler.GetResourceLimits()
	require.True(t, ok, "autoscaler did not include resource limits")
	assert.Equal(t, maxNodesTotal, resourceLimits.MaxNodesTotal(), "autoscaler ResourceLimits.MaxNodesTotal mismatch")

	// The core assertion: a single cluster GET fully populated the autoscaler,
	// so no autoscaler link-resolution round-trip was needed.
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requestedURLs, 1, "expected exactly one Cluster Service request (the cluster GET), got: %v", requestedURLs)
	assert.Equal(t, internalID.Path(), requestedURLs[0], "the only request should be the cluster GET")
	for _, u := range requestedURLs {
		assert.NotContains(t, strings.TrimPrefix(u, internalID.Path()), "autoscaler", "unexpected autoscaler link-resolution request: %s", u)
	}
}
