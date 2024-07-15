package frontend

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	// Wildcard path segment names for request multiplexing, must be lowercase as we lowercase the request URL pattern when registering handlers
	PageSegmentLocation          = "location"
	PathSegmentSubscriptionID    = "subscriptionid"
	PathSegmentResourceGroupName = "resourcegroupname"
	PathSegmentResourceName      = "resourcename"
	PathSegmentDeploymentName    = "deploymentname"
	PathSegmentActionName        = "actionname"
	PathSegmentNodepoolName      = "nodepoolname"
)

func (f *Frontend) routes() *MiddlewareMux {
	subscriptionStateMuxValidator := NewSubscriptionStateMuxValidator(f.dbClient)

	// Setup metrics middleware
	metricsMiddleware := MetricsMiddleware{dbClient: f.dbClient, Emitter: f.metrics}

	mux := NewMiddlewareMux(
		MiddlewarePanic,
		MiddlewareLogging,
		MiddlewareBody,
		MiddlewareLowercase,
		MiddlewareSystemData,
		MiddlewareValidateStatic,
		metricsMiddleware.Metrics(),
	)

	// Unauthenticated routes
	mux.HandleFunc("/", f.NotFound)
	mux.HandleFunc("GET /healthz", f.Healthz)
	// TODO: determine where in the auth chain we should allow for this endpoint to be called by ARM
	mux.HandleFunc(
		fmt.Sprintf("GET /subscriptions/{%s}", PathSegmentSubscriptionID),
		f.ArmSubscriptionGet)
	mux.HandleFunc(
		fmt.Sprintf("PUT /subscriptions/{%s}", PathSegmentSubscriptionID),
		f.ArmSubscriptionPut)

	// Expose Prometheus metrics endpoint
	mux.Handle("GET /metrics", promhttp.Handler())

	// Authenticated routes
	postMuxMiddleware := NewMiddleware(
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		fmt.Sprintf("GET /subscriptions/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters", PathSegmentSubscriptionID),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		fmt.Sprintf("GET /subscriptions/{%s}/locations/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters", PathSegmentSubscriptionID, PageSegmentLocation),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		fmt.Sprintf("GET /subscriptions/{%s}/resourcegroups/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters", PathSegmentSubscriptionID, PathSegmentResourceGroupName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		fmt.Sprintf("GET /subscriptions/{%s}/resourcegroups/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{%s}", PathSegmentSubscriptionID, PathSegmentResourceGroupName, PathSegmentResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceRead))
	mux.Handle(
		fmt.Sprintf("PUT /subscriptions/{%s}/resourcegroups/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{%s}", PathSegmentSubscriptionID, PathSegmentResourceGroupName, PathSegmentResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		fmt.Sprintf("PATCH /subscriptions/{%s}/resourcegroups/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{%s}", PathSegmentSubscriptionID, PathSegmentResourceGroupName, PathSegmentResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceUpdate))
	mux.Handle(
		fmt.Sprintf("DELETE /subscriptions/{%s}/resourcegroups/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{%s}", PathSegmentSubscriptionID, PathSegmentResourceGroupName, PathSegmentResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceDelete))
	mux.Handle(
		fmt.Sprintf("POST /subscriptions/{%s}/resourcegroups/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{%s}/{%s}", PathSegmentSubscriptionID, PathSegmentResourceGroupName, PathSegmentResourceName, PathSegmentActionName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceAction))

	// node pools
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternNodepoolResource),
		postMuxMiddleware.HandlerFunc(f.GetNodePool))
	mux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternNodepoolResource),
		postMuxMiddleware.HandlerFunc(f.CreateNodePool))
	mux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternNodepoolResource),
		postMuxMiddleware.HandlerFunc(f.DeleteNodePool))

	// Exclude ARO-HCP API version validation for endpoints defined by ARM.
	postMuxMiddleware = NewMiddleware(
		MiddlewareLoggingPostMux,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		fmt.Sprintf("POST /subscriptions/{%s}/resourcegroups/{%s}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/deployments/{%s}/preflight", PathSegmentSubscriptionID, PathSegmentResourceGroupName, PathSegmentDeploymentName),
		postMuxMiddleware.HandlerFunc(f.ArmDeploymentPreflight))

	return mux
}
