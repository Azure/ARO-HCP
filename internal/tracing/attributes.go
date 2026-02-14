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

package tracing

import (
	"time"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

// Correlation data and generic attributes.
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

	// APIVersionKey is the span's attribute Key reporting the API version.
	APIVersionKey = attribute.Key("aro.api_version")
)

// Subscription attributes.
const (
	// SubscriptionIDKey is the span's attribute Key reporting the subscription
	// identifier associated to the current request.
	SubscriptionIDKey = attribute.Key("aro.subscription.id")

	// SubscriptionStateKey is the span's attribute Key reporting the
	// subscription state associated to the current request.
	SubscriptionStateKey = attribute.Key("aro.subscription.state")
)

// Resource attributes.
const (
	// ResourceGroupNameKey is the span's attribute Key reporting the resource
	// group name associated to the current request.
	ResourceGroupNameKey = attribute.Key("aro.resource_group.name")

	// ResourceNameKey is the span's attribute Key reporting the resource
	// name associated to the current request.
	ResourceNameKey = attribute.Key("aro.resource.name")

	// ResourceIDKey is the span's attribute Key reporting the resource
	// identifier associated to the current request.
	ResourceIDKey = semconv.CloudResourceIDKey

	// ResourceTypeKey is the span's attribute Key reporting the resource
	// type associated to the current request.
	ResourceTypeKey = attribute.Key("aro.resource.type")
)

// Operation attributes.
const (
	// OperationIDKey is the span's attribute Key reporting the operation
	// identifier.
	OperationIDKey = attribute.Key("aro.operation.id")

	// OperationTypeKey is the span's attribute Key reporting the operation
	// type.
	OperationTypeKey = attribute.Key("aro.operation.type")

	// OperationStatusKey is the span's attribute Key reporting the operation
	// status.
	OperationStatusKey = attribute.Key("aro.operation.status")
)

const (
	// ProcessedItemsKey is the span's attribute Key reporting the number of
	// items (subscriptions or operations) which have been processed by the
	// Resource Provider backend.
	ProcessedItemsKey = attribute.Key("aro.backend.processed_items")
)

// Clusters service attributes.
const (
	// ClusterIDKey is the span's attribute Key reporting the internal cluster identifier.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	ClusterIDKey = attribute.Key("cs.cluster.id")

	// ClusterNameKey is the span's attribute Key reporting the internal cluster name.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	ClusterNameKey = attribute.Key("cs.cluster.name")

	// ClusterStateKey is the span's attribute Key reporting the internal cluster state.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	ClusterStateKey = attribute.Key("cs.cluster.state")

	// NodePoolIDKey is the span's attribute Key reporting the internal node pool identifier.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	NodePoolIDKey = attribute.Key("cs.nodepool.id")

	// NodePoolStateKey is the span's attribute Key reporting the internal cluster state.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	NodePoolStateKey = attribute.Key("cs.nodepool.state")

	// ExternalAuthIDKey is the span's attribute Key reporting the internal external auth identifier.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	ExternalAuthIDKey = attribute.Key("cs.externalauth.id")

	// ExternalAuthStateKey is the span's attribute Key reporting the internal cluster state.
	// The key needs to be kept in sync with the key used by the Clusters Service.
	ExternalAuthStateKey = attribute.Key("cs.externalauth.state")

	// BreakGlassCredentialIDKey is the attribute key for the break-glass credential ID.
	BreakGlassCredentialIDKey attribute.Key = "cs.break_glass_credential.id"

	// BreakGlassCredentialStatus is the attribute key for the break-glass credential status.
	BreakGlassCredentialStatusKey attribute.Key = "cs.break_glass_credential.status"

	// BreakGlassCredentialRevocationTimestampKey is the attribute key for the
	// break-glass credential's revocation timetstamp .
	BreakGlassCredentialRevocationTimestampKey attribute.Key = "cs.break_glass_credential.revocation_time"

	// BreakGlassCredentialExpirationTimestampKey is the attribute key for the
	// break-glass credential's expiration timetstamp .
	BreakGlassCredentialExpirationTimestampKey attribute.Key = "cs.break_glass_credential.expiration_time"
)

// Feature flag attributes.
const (
	// FeatureSingleReplicaKey is the span's attribute Key reporting whether
	// single-replica control plane is enabled for a cluster operation.
	FeatureSingleReplicaKey = attribute.Key("aro.feature.single_replica")

	// FeatureSizeOverrideKey is the span's attribute Key reporting whether
	// the cluster size override is enabled for a cluster operation.
	FeatureSizeOverrideKey = attribute.Key("aro.feature.size_override")
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

// SetClusterAttributes sets attributes on the span to identify the cluster status.
func SetClusterStatusAttributes(span trace.Span, clusterStatus *arohcpv1alpha1.ClusterStatus) {
	addAttributeIfPresent(span, ClusterIDKey, clusterStatus.GetID)
	addAttributeIfPresent(span, ClusterStateKey, func() (string, bool) {
		v, present := clusterStatus.GetState()
		return string(v), present
	})
}

// SetNodePoolAttributes sets attributes on the span to identify the node pool.
func SetNodePoolAttributes(span trace.Span, nodePool *arohcpv1alpha1.NodePool) {
	addAttributeIfPresent(span, NodePoolIDKey, nodePool.GetID)
	addAttributeIfPresent(span, NodePoolStateKey, nodePool.Status().State().GetNodePoolStateValue)
}

// SetNodePoolStatusAttributes sets attributes on the span to identify the node pool status.
func SetNodePoolStatusAttributes(span trace.Span, nodePoolStatus *arohcpv1alpha1.NodePoolStatus) {
	addAttributeIfPresent(span, NodePoolIDKey, nodePoolStatus.GetID)
	addAttributeIfPresent(span, NodePoolStateKey, nodePoolStatus.State().GetNodePoolStateValue)
}

// SetExternalAuthAttributes sets attributes on the span to identify the external auth.
func SetExternalAuthAttributes(span trace.Span, externalAuth *arohcpv1alpha1.ExternalAuth) {
	addAttributeIfPresent(span, ExternalAuthIDKey, externalAuth.GetID)
	// addAttributeIfPresent(span, ExternalAuthStateKey, externalAuth.Status().State().)
}

// SetBreakGlassCredentialAttributes sets attributes on the span to identify the break-glass credential
func SetBreakGlassCredentialAttributes(span trace.Span, credential *cmv1.BreakGlassCredential) {
	addAttributeIfPresent(span, BreakGlassCredentialIDKey, credential.GetID)
	addAttributeIfPresent(span, BreakGlassCredentialStatusKey, func() (string, bool) {
		v, present := credential.GetStatus()
		return string(v), present
	})
	addAttributeIfPresent(span, BreakGlassCredentialExpirationTimestampKey, func() (string, bool) {
		ts, present := credential.GetExpirationTimestamp()
		if !present {
			return "", false
		}
		return ts.Format(time.RFC3339), true
	})
	addAttributeIfPresent(span, BreakGlassCredentialRevocationTimestampKey, func() (string, bool) {
		ts, present := credential.GetRevocationTimestamp()
		if !present {
			return "", false
		}
		return ts.Format(time.RFC3339), true
	})
}

func addAttributeIfPresent(span trace.Span, key attribute.Key, getter func() (string, bool)) {
	v, present := getter()
	if present {
		span.SetAttributes(key.String(v))
	}
}
