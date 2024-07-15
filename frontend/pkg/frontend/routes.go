package frontend

import (
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	mux.HandleFunc("GET /subscriptions/{subscriptionid}", f.ArmSubscriptionGet)
	mux.HandleFunc("PUT /subscriptions/{subscriptionid}", f.ArmSubscriptionPut)

	// Expose Prometheus metrics endpoint
	mux.Handle("GET /metrics", promhttp.Handler())

	// Authenticated routes
	postMuxMiddleware := NewMiddleware(
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		"GET /subscriptions/{subscriptionid}/providers/microsoft.redhatopenshift/hcpopenshiftclusters",
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		"GET /subscriptions/{subscriptionid}/locations/{location}/providers/microsoft.redhatopenshift/hcpopenshiftclusters",
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		"GET /subscriptions/{subscriptionid}/resourcegroups/{resourcegroupname}/providers/microsoft.redhatopenshift/hcpopenshiftclusters",
		postMuxMiddleware.HandlerFunc(f.ArmResourceList))
	mux.Handle(
		"GET /subscriptions/{subscriptionid}/resourcegroups/{resourcegroupname}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{resourcename}",
		postMuxMiddleware.HandlerFunc(f.ArmResourceRead))
	mux.Handle(
		"PUT /subscriptions/{subscriptionid}/resourcegroups/{resourcegroupname}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{resourcename}",
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		"PATCH /subscriptions/{subscriptionid}/resourcegroups/{resourcegroupname}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{resourcename}",
		postMuxMiddleware.HandlerFunc(f.ArmResourceUpdate))
	mux.Handle(
		"DELETE /subscriptions/{subscriptionid}/resourcegroups/{resourcegroupname}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{resourcename}",
		postMuxMiddleware.HandlerFunc(f.ArmResourceDelete))
	mux.Handle(
		"POST /subscriptions/{subscriptionid}/resourcegroups/{resourcegroupname}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/{resourcename}/{actionname}",
		postMuxMiddleware.HandlerFunc(f.ArmResourceAction))

	// Exclude ARO-HCP API version validation for endpoints defined by ARM.
	postMuxMiddleware = NewMiddleware(
		MiddlewareLoggingPostMux,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		"POST /subscriptions/{subscriptionid}/resourcegroups/{resourcegroupname}/providers/microsoft.redhatopenshift/hcpopenshiftclusters/deployments/{deploymentname}/preflight",
		postMuxMiddleware.HandlerFunc(f.ArmDeploymentPreflight))

	return mux
}
