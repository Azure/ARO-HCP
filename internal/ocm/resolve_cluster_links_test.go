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
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
)

// recordingTransport records the request paths it serves and returns a fixed
// response body. It lets the test assert whether the autoscaler subresource was
// fetched during link resolution.
type recordingTransport struct {
	requestedPaths []string
	body           string
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requestedPaths = append(t.requestedPaths, req.URL.Path)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}

// newTestConnection builds an ocm-sdk-go connection whose transport is fully
// replaced by the provided RoundTripper. A non-expired access token avoids any
// token-refresh round trip.
func newTestConnection(t *testing.T, transport http.RoundTripper) *sdk.Connection {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"typ": "Bearer",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	accessToken, err := token.SignedString([]byte("test-secret"))
	require.NoError(t, err)

	conn, err := sdk.NewConnectionBuilder().
		URL("http://localhost:8000").
		Tokens(accessToken).
		TransportWrapper(func(http.RoundTripper) http.RoundTripper { return transport }).
		Build()
	require.NoError(t, err)

	return conn
}

// fullAutoscaler returns a ClusterAutoscaler builder populated as a full,
// embedded object (i.e. NOT a link) with representative configuration.
func fullAutoscaler() *arohcpv1alpha1.ClusterAutoscalerBuilder {
	return arohcpv1alpha1.NewClusterAutoscaler().
		BalanceSimilarNodeGroups(true).
		MaxPodGracePeriod(600).
		MaxNodeProvisionTime("15m").
		PodPriorityThreshold(-10).
		ResourceLimits(arohcpv1alpha1.NewAutoscalerResourceLimits().MaxNodesTotal(18))
}

// TestResolveClusterLinksEmbeddedAutoscaler is the primary case for the CS
// "embedded autoscaler" change: when the cluster already carries a full
// autoscaler body, resolveClusterLinks must preserve it and must NOT perform a
// second GET against the autoscaler subresource. A nil connection is passed so
// that any attempt to fetch the subresource would panic and fail the test.
func TestResolveClusterLinksEmbeddedAutoscaler(t *testing.T) {
	cluster, err := arohcpv1alpha1.NewCluster().
		Autoscaler(fullAutoscaler()).
		Build()
	require.NoError(t, err)

	resolved, err := resolveClusterLinks(context.Background(), nil, cluster)
	require.NoError(t, err)

	autoscaler, ok := resolved.GetAutoscaler()
	require.True(t, ok, "expected autoscaler to be present on resolved cluster")

	assert.False(t, autoscaler.Link(), "embedded autoscaler must not be a link")
	assert.Equal(t, arohcpv1alpha1.ClusterAutoscalerKind, autoscaler.Kind())
	assert.True(t, autoscaler.BalanceSimilarNodeGroups())
	assert.Equal(t, 600, autoscaler.MaxPodGracePeriod())
	assert.Equal(t, "15m", autoscaler.MaxNodeProvisionTime())
	assert.Equal(t, -10, autoscaler.PodPriorityThreshold())
	assert.Equal(t, 18, autoscaler.ResourceLimits().MaxNodesTotal())
}

// TestResolveClusterLinksAutoscalerLink is the regression case preserving
// compatibility with OLD Clusters Service: when the autoscaler is a link,
// resolveClusterLinks must follow the href and resolve the full body.
func TestResolveClusterLinksAutoscalerLink(t *testing.T) {
	href := "/api/aro_hcp/v1alpha1/clusters/abc/autoscaler"

	cluster, err := arohcpv1alpha1.NewCluster().
		Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().Link(true).HREF(href)).
		Build()
	require.NoError(t, err)

	// The subresource GET returns the full autoscaler body.
	fullBody, err := fullAutoscaler().Build()
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, arohcpv1alpha1.MarshalClusterAutoscaler(fullBody, &buf))

	transport := &recordingTransport{body: buf.String()}
	conn := newTestConnection(t, transport)
	defer conn.Close()

	resolved, err := resolveClusterLinks(context.Background(), conn, cluster)
	require.NoError(t, err)

	// The autoscaler subresource must have been fetched exactly once.
	assert.Contains(t, transport.requestedPaths, href,
		"expected a GET against the autoscaler subresource")

	autoscaler, ok := resolved.GetAutoscaler()
	require.True(t, ok, "expected autoscaler to be present on resolved cluster")
	assert.False(t, autoscaler.Link(), "resolved autoscaler must be a full object")
	assert.Equal(t, 18, autoscaler.ResourceLimits().MaxNodesTotal())
}
