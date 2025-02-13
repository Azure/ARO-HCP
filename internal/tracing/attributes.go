package tracing

import "go.opentelemetry.io/otel/attribute"

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

const (
	// CorrelationIDKey is the span's attribute Key reporting the correlation
	// ID from the originating ARM request.
	CorrelationIDKey = attribute.Key("aro.correlation_id")

	// ClientRequestIDKey is the span's attribute Key reporting the client
	// request ID from the originating ARM request.
	ClientRequestIDKey = attribute.Key("aro.client.request_id")

	// RequestIDKey is the span's attribute Key reporting the unique request ID
	// assigned by the RP frontend.
	RequestIDKey = attribute.Key("aro.request_id")

	// SubscriptionIDKey is the span's attribute Key reporting the subscription
	// identifier associated to the current request.
	SubscriptionIDKey = attribute.Key("aro.subscription.id")

	// SubscriptionStateKey is the span's attribute Key reporting the
	// subscription state associated to the current request.
	SubscriptionStateKey = attribute.Key("aro.subscription.state")

	// ResourceGroupNameKey is the span's attribute Key reporting the resource
	// group name associated to the current request.
	ResourceGroupNameKey = attribute.Key("aro.resource_group.name")

	// ResourceNameKey is the span's attribute Key reporting the resource
	// name associated to the current request.
	ResourceNameKey = attribute.Key("aro.resource.name")

	// APIVersionKey is the span's attribute Key reporting the API version.
	APIVersionKey = attribute.Key("aro.api_version")
)
