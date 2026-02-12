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

package app

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	ocmsdk "github.com/openshift-online/ocm-sdk-go"

	backendtracing "github.com/Azure/ARO-HCP/backend/pkg/tracing"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func NewClustersServiceClient(ctx context.Context, clustersServiceURL string, clustersServiceTLSInsecure bool) (ocm.ClusterServiceClientSpec, error) {
	// Create OCM connection
	ocmConnection, err := ocmsdk.NewUnauthenticatedConnectionBuilder().
		TransportWrapper(func(r http.RoundTripper) http.RoundTripper {
			return otelhttp.NewTransport(http.DefaultTransport)
		}).
		URL(clustersServiceURL).
		Insecure(clustersServiceTLSInsecure).
		Build()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create OCM connection: %w", err))
	}

	// Create Cluster Service Client using the OCM connection
	clusterServiceClient := ocm.NewClusterServiceClientWithTracing(
		ocm.NewClusterServiceClient(
			ocmConnection,
			"",
			false,
			false,
		),
		backendtracing.BackendTracerName,
	)

	return clusterServiceClient, nil
}
