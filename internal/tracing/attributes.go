package tracing

import (
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

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

	// ClusterIDKey is the span's attribute Key reporting the internal cluster identifier.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	ClusterIDKey = attribute.Key("cs.cluster.id")

	// ClusterNameKey is the span's attribute Key reporting the internal cluster name.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	ClusterNameKey = attribute.Key("cs.cluster.name")

	// ClusterStateKey is the span's attribute Key reporting the internal cluster state.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	ClusterStateKey = attribute.Key("cs.cluster.state")
)

// SetClusterAttributes sets attributes on the span to identify the cluster.
func SetClusterAttributes(span trace.Span, cluster *arohcpv1alpha1.Cluster) {
	addAttributeIfPresent(span, ClusterIDKey, cluster.GetID)
	addAttributeIfPresent(span, ClusterNameKey, cluster.GetName)
	addAttributeIfPresent(span, ClusterStateKey, func() (string, bool) {
		v, present := cluster.GetState()
		return string(v), present
	})
}

func addAttributeIfPresent(span trace.Span, key attribute.Key, getter func() (string, bool)) {
	v, present := getter()
	if present {
		span.SetAttributes(key.String(v))
	}
}
