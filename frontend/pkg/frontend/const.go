package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

const (
	ProgramName = "ARO HCP Frontend"

	// APIVersionKey is the request parameter name for the API version.
	APIVersionKey = "api-version"

	// Wildcard path segment names for request multiplexing, must be lowercase as we lowercase the request URL pattern when registering handlers
	PathSegmentActionName        = "actionname"
	PathSegmentDeploymentName    = "deploymentname"
	PathSegmentLocation          = "location"
	PathSegmentNodePoolName      = "nodepoolname"
	PathSegmentOperationID       = "operationid"
	PathSegmentResourceGroupName = "resourcegroupname"
	PathSegmentResourceName      = "resourcename"
	PathSegmentSubscriptionID    = "subscriptionid"

	healthGaugeName     = "frontend_health"
	requestCounterName  = "frontend_http_requests_total"
	requestDurationName = "frontend_http_requests_duration_seconds"

	noMatchRouteLabel   = "<no match>"
	unknownVersionLabel = "<unknown>"
)
