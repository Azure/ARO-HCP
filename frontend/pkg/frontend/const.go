package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

const (
	ProgramName = "ARO HCP Frontend"

	// APIVersionKey is the request parameter name for the API version.
	APIVersionKey = "api-version"

	// Wildcard path segment names for request multiplexing, must be lowercase as we lowercase the request URL pattern when registering handlers
	PageSegmentLocation          = "location"
	PathSegmentSubscriptionID    = "subscriptionid"
	PathSegmentResourceGroupName = "resourcegroupname"
	PathSegmentResourceName      = "resourcename"
	PathSegmentDeploymentName    = "deploymentname"
	PathSegmentActionName        = "actionname"
)
