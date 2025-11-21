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
	WildcardExternalAuthName  = "{" + PathSegmentExternalAuthName + "}"
	WildcardOperationID       = "{" + PathSegmentOperationID + "}"
	WildcardResourceGroupName = "{" + PathSegmentResourceGroupName + "}"
	WildcardResourceName      = "{" + PathSegmentResourceName + "}"
	WildcardSubscriptionID    = "{" + PathSegmentSubscriptionID + "}"

	PatternSubscriptions     = "subscriptions/" + WildcardSubscriptionID
	PatternLocations         = "locations/" + WildcardLocation
	PatternProviders         = "providers/" + api.ProviderNamespace
	PatternClusters          = api.ClusterResourceTypeName + "/" + WildcardResourceName
	PatternNodePools         = api.NodePoolResourceTypeName + "/" + WildcardNodePoolName
	PatternVersions          = api.VersionResourceTypeName + "/" + WildcardResourceName
	PatternExternalAuth      = api.ExternalAuthResourceTypeName + "/" + WildcardExternalAuthName
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

func (f *Frontend) routes(r prometheus.Registerer) http.Handler {
	// Setup metrics middleware
	metricsMiddleware := NewMetricsMiddleware(r, f.collector)

	middlewareMux := NewMiddlewareMux(
		MiddlewarePanic,
		MiddlewareReferer,
		metricsMiddleware.Metrics(),
		MiddlewareCorrelationData,
		newMiddlewareAudit(f.auditClient).handleRequest,
		MiddlewareTracing,
		MiddlewareLowercase,
		MiddlewareLogging,
		// NOTE: register panic middleware twice.
		// Making sure we can capture panicked requests in our trace data.
		// But we also can recover if the tracing or logging middleware caused a panic.
		MiddlewarePanic,
		MiddlewareBody,
		MiddlewareSystemData,
		MiddlewareValidateStatic,
	)

	middlewareMux.HandleFunc("/", f.NotFound)

	// Resource list endpoints
	postMuxMiddleware := NewMiddleware(
		MiddlewareLoggingPostMux,
		newMiddlewareValidatedAPIVersion(f.apiRegistry).handleRequest,
		newMiddlewareValidateSubscriptionState(f.dbClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, api.ClusterResourceTypeName),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceListClusters)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, api.ClusterResourceTypeName),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceListClusters)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, api.NodePoolResourceTypeName),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceListNodePools)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, api.ExternalAuthResourceTypeName),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceListExternalAuths)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, api.VersionResourceTypeName),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceListVersion)))

	// Resource read endpoints
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		newMiddlewareValidatedAPIVersion(f.apiRegistry).handleRequest,
		newMiddlewareValidateSubscriptionState(f.dbClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(reportError(f.GetHCPCluster)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(reportError(f.GetNodePool)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, PatternVersions),
		postMuxMiddleware.HandlerFunc(reportError(f.GetOpenshiftVersions)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternExternalAuth),
		postMuxMiddleware.HandlerFunc(reportError(f.GetExternalAuth)))

	// Resource create/update/delete endpoints
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		newMiddlewareValidatedAPIVersion(f.apiRegistry).handleRequest,
		newMiddlewareLockSubscription(f.dbClient).handleRequest,
		newMiddlewareValidateSubscriptionState(f.dbClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(reportError(f.CreateOrUpdateHCPCluster)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(reportError(f.CreateOrUpdateHCPCluster)))
	middlewareMux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceDelete)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, ActionRequestAdminCredential),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceActionRequestAdminCredential)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, ActionRevokeCredentials),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceActionRevokeCredentials)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(reportError(f.CreateOrUpdateNodePool)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(reportError(f.CreateOrUpdateNodePool)))
	middlewareMux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternNodePools),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceDelete)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternExternalAuth),
		postMuxMiddleware.HandlerFunc(reportError(f.CreateOrUpdateExternalAuth)))
	middlewareMux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternExternalAuth),
		postMuxMiddleware.HandlerFunc(reportError(f.CreateOrUpdateExternalAuth)))
	middlewareMux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters, PatternExternalAuth),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmResourceDelete)))

	// Operation endpoints
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		newMiddlewareValidatedAPIVersion(f.apiRegistry).handleRequest,
		newMiddlewareValidateSubscriptionState(f.dbClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, PatternOperationResults),
		postMuxMiddleware.HandlerFunc(reportError(f.OperationResult)))
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders, PatternLocations, PatternOperationStatuses),
		postMuxMiddleware.HandlerFunc(reportError(f.OperationStatus)))
	// Exclude ARO-HCP API version validation for the following endpoints defined by ARM.

	// Subscription management endpoints
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux)
	middlewareMux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmSubscriptionGet)))
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		newMiddlewareLockSubscription(f.dbClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmSubscriptionPut)))

	// Deployment preflight endpoint
	postMuxMiddleware = NewMiddleware(
		MiddlewareLoggingPostMux,
		newMiddlewareValidateSubscriptionState(f.dbClient).handleRequest)
	middlewareMux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, "providers", api.ProviderNamespace, PatternDeployments, "preflight"),
		postMuxMiddleware.HandlerFunc(reportError(f.ArmDeploymentPreflight)))

	mux := http.NewServeMux()
	mux.HandleFunc("/", middlewareMux.ServeHTTP)

	// These endpoints do not use middleware. They are only called
	// from within the service cluster or via kubectl port forwarding.
	mux.HandleFunc(MuxPattern(http.MethodGet, "healthz"), f.Healthz)
	mux.HandleFunc(MuxPattern(http.MethodGet, "location"), f.Location)

	return mux
}

func (f *Frontend) metricsRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())

	return mux
}
