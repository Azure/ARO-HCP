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

package frontend

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Azure/ARO-HCP/internal/api"
)

const (
	WildcardDeploymentName    = "{" + PathSegmentDeploymentName + "}"
	WildcardLocation          = "{" + PathSegmentLocation + "}"
	WildcardNodePoolName      = "{" + PathSegmentNodePoolName + "}"
	WildcardOperationID       = "{" + PathSegmentOperationID + "}"
	WildcardResourceGroupName = "{" + PathSegmentResourceGroupName + "}"
	WildcardResourceName      = "{" + PathSegmentResourceName + "}"
	WildcardSubscriptionID    = "{" + PathSegmentSubscriptionID + "}"

	PatternSubscriptions     = "subscriptions/" + WildcardSubscriptionID
	PatternLocations         = "locations/" + WildcardLocation
	PatternProviders         = "providers/" + api.ProviderNamespace
	PatternClusters          = api.ClusterResourceTypeName + "/" + WildcardResourceName
	PatternNodePools         = api.NodePoolResourceTypeName + "/" + WildcardNodePoolName
	PatternDeployments       = "deployments/" + WildcardDeploymentName
	PatternResourceGroups    = "resourcegroups/" + WildcardResourceGroupName
	PatternOperationResults  = api.OperationResultResourceTypeName + "/" + WildcardOperationID
	PatternOperationStatuses = api.OperationStatusResourceTypeName + "/" + WildcardOperationID

	ActionRequestAdminCredential = "requestadmincredential"
	ActionRevokeCredentials      = "revokecredentials"
)

// MuxPattern forms a URL pattern suitable for passing to http.ServeMux.
// Literal path segments must be lowercase because MiddlewareLowercase
// converts the request URL to lowercase before multiplexing.
func MuxPattern(method string, segments ...string) string {
	return fmt.Sprintf("%s /%s", method, strings.ToLower(path.Join(segments...)))
}

func (f *Frontend) routes(r prometheus.Registerer) *MiddlewareMux {
	// Setup metrics middleware
	metricsMiddleware := NewMetricsMiddleware(r, f.collector)

	mux := NewMiddlewareMux(
		MiddlewarePanic,
		MiddlewareReferer,
		metricsMiddleware.Metrics(),
		MiddlewareCorrelationData,
		MiddlewareTracing,
		MiddlewareLogging,
		// NOTE: register panic middlware twice.
		// Making sure we can capture paniced requests in our trace data.
		// But we also can recover if the tracing or logging middleware caused a panic.
		MiddlewarePanic,
		MiddlewareBody,
		MiddlewareLowercase,
		MiddlewareSystemData,
		MiddlewareValidateStatic,
	)

	// Unauthenticated routes
	mux.HandleFunc("/", f.NotFound)
	mux.HandleFunc(MuxPattern(http.MethodGet, "healthz"), f.Healthz)

	// List endpoints
	postMuxMiddleware := NewMiddleware(
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion,
		MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, api.ClusterResourceTypeName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, api.ClusterResourceTypeName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, api.NodePoolResourceTypeName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))

	// Resource ID endpoints
	// Request context holds an azcorearm.ResourceID
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion,
		MiddlewareLockSubscription,
		MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(f.ArmResourceRead))
	mux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(f.ArmResourceDelete))
	mux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, ActionRequestAdminCredential),
		postMuxMiddleware.HandlerFunc(f.ArmResourceActionRequestAdminCredential))
	mux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, ActionRevokeCredentials),
		postMuxMiddleware.HandlerFunc(f.ArmResourceActionRevokeCredentials))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(f.ArmResourceRead))
	mux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(f.CreateOrUpdateNodePool))
	mux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(f.CreateOrUpdateNodePool))
	mux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(f.ArmResourceDelete))

	// Operation endpoints
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion,
		MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, PatternOperationResults),
		postMuxMiddleware.HandlerFunc(f.OperationResult))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, PatternOperationStatuses),
		postMuxMiddleware.HandlerFunc(f.OperationStatus))

	// Exclude ARO-HCP API version validation for the following endpoints defined by ARM.

	// Subscription management endpoints
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		MiddlewareLockSubscription)
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions),
		postMuxMiddleware.HandlerFunc(f.ArmSubscriptionGet))
	mux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions),
		postMuxMiddleware.HandlerFunc(f.ArmSubscriptionPut))

	// Deployment preflight endpoint
	postMuxMiddleware = NewMiddleware(
		MiddlewareLoggingPostMux,
		MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, "providers", api.ProviderNamespace, PatternDeployments, "preflight"),
		postMuxMiddleware.HandlerFunc(f.ArmDeploymentPreflight))

	return mux
}

func (f *Frontend) metricsRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())

	return mux
}
