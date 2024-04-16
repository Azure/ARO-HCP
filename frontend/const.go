package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

type contextKey int

const (
	// APIVersionKey is the request parameter name for the API version.
	APIVersionKey = "api-version"

	// Keys for request-scoped data in http.Request contexts
	ContextKeyOriginalPath contextKey = iota
	ContextKeyBody
	ContextKeyLogger
	ContextKeyVersion
	ContextKeyCorrelationData
	ContextKeySystemData

	// Wildcard path segment names for request multiplexing, must be lowercase as we lowercase the request URL pattern when registering handlers
	PageSegmentLocation          = "location"
	PathSegmentSubscriptionID    = "subscriptionid"
	PathSegmentResourceGroupName = "resourcegroupname"
	PathSegmentResourceName      = "resourcename"
	PathSegmentActionName        = "actionname"
)
