package frontend

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Azure/ARO-HCP/internal/api"
)

const (
	PatternSubscriptions    = "subscriptions/{" + PathSegmentSubscriptionID + "}"
	PatternLocations        = "locations/{" + PageSegmentLocation + "}"
	PatternProviders        = "providers/" + api.ResourceType
	PatternNodePoolResource = api.NodePoolResourceTypeName + "/{" + PathSegmentNodePoolName + "}"
	PatternDeployments      = "deployments/{" + PathSegmentDeploymentName + "}"
	PatternResourceGroups   = "resourcegroups/{" + PathSegmentResourceGroupName + "}"
	PatternResourceName     = "{" + PathSegmentResourceName + "}"
	PatternActionName       = "{" + PathSegmentActionName + "}"
)

// MuxPattern forms a URL pattern suitable for passing to http.ServeMux.
// Literal path segments must be lowercase because MiddlewareLowercase
// converts the request URL to lowercase before multiplexing.
func MuxPattern(method string, segments ...string) string {
	return fmt.Sprintf("%s /%s", method, strings.ToLower(path.Join(segments...)))
}

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
	mux.HandleFunc(MuxPattern(http.MethodGet, "healthz"), f.Healthz)
	mux.Handle(MuxPattern(http.MethodGet, "metrics"), promhttp.Handler())

	// List endpoints
	postMuxMiddleware := NewMiddleware(
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternLocations, PatternProviders),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders),
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))

	// Resource ID endpoints
	// Request context holds an azcorearm.ResourceID
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceRead))
	mux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceDelete))
	mux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternActionName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceAction))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternNodePoolResource),
		postMuxMiddleware.HandlerFunc(f.GetNodePool))
	mux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternNodePoolResource),
		postMuxMiddleware.HandlerFunc(f.CreateOrUpdateNodePool))
	mux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternNodePoolResource),
		postMuxMiddleware.HandlerFunc(f.CreateOrUpdateNodePool))
	mux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternNodePoolResource),
		postMuxMiddleware.HandlerFunc(f.DeleteNodePool))

	// Exclude ARO-HCP API version validation for the following endpoints defined by ARM.

	// Subscription management endpoints
	postMuxMiddleware = NewMiddleware(
		MiddlewareResourceID,
		MiddlewareLoggingPostMux)
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions),
		postMuxMiddleware.HandlerFunc(f.ArmSubscriptionGet))
	mux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions),
		postMuxMiddleware.HandlerFunc(f.ArmSubscriptionPut))

	// Deployment preflight endpoint
	postMuxMiddleware = NewMiddleware(
		MiddlewareLoggingPostMux,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, "providers", api.ProviderNamespace, PatternDeployments, "preflight"),
		postMuxMiddleware.HandlerFunc(f.ArmDeploymentPreflight))

	return mux
}
