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

package ocm

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/tracing"
)

type clusterServiceClientWithTracing struct {
	// NOTE: we chose not to embed ClusterServiceClientSpec into the struct to
	// make sure that if new method(s) are added to the interface, we also have
	// to implement them here.
	csc        ClusterServiceClientSpec
	tracerName string
}

func NewClusterServiceClientWithTracing(csc ClusterServiceClientSpec, tracerName string) ClusterServiceClientSpec {
	return &clusterServiceClientWithTracing{
		csc:        csc,
		tracerName: tracerName,
	}
}

// startChildSpan creates a new span linked to the parent span from the current context.
func (csc *clusterServiceClientWithTracing) startChildSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return trace.SpanFromContext(ctx).
		TracerProvider().
		Tracer(csc.tracerName).
		Start(ctx, name)
}

func (csc *clusterServiceClientWithTracing) GetCluster(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.Cluster, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.GetCluster")
	defer span.End()

	cluster, err := csc.csc.GetCluster(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetClusterAttributes(span, cluster)
	}

	return cluster, err
}

func (csc *clusterServiceClientWithTracing) GetClusterStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ClusterStatus, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.GetClusterStatus")
	defer span.End()

	clusterStatus, err := csc.csc.GetClusterStatus(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetClusterStatusAttributes(span, clusterStatus)
	}

	return clusterStatus, err
}

func (csc *clusterServiceClientWithTracing) GetClusterInflightChecks(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.InflightCheckList, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.GetClusterInflightChecks")
	defer span.End()

	return csc.csc.GetClusterInflightChecks(ctx, internalID)
}

func (csc *clusterServiceClientWithTracing) PostCluster(ctx context.Context, clusterBuilder *arohcpv1alpha1.ClusterBuilder, autoscalerBuilder *arohcpv1alpha1.ClusterAutoscalerBuilder) (*arohcpv1alpha1.Cluster, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.PostCluster")
	defer span.End()

	cluster, err := csc.csc.PostCluster(ctx, clusterBuilder, autoscalerBuilder)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetClusterAttributes(span, cluster)
	}

	return cluster, err
}

func (csc *clusterServiceClientWithTracing) UpdateCluster(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.UpdateCluster")
	defer span.End()

	cluster, err := csc.csc.UpdateCluster(ctx, internalID, builder)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetClusterAttributes(span, cluster)
	}

	return cluster, err
}

func (csc *clusterServiceClientWithTracing) UpdateClusterAutoscaler(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ClusterAutoscalerBuilder) (*arohcpv1alpha1.ClusterAutoscaler, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.UpdateClusterAutoscaler")
	defer span.End()

	autoscaler, err := csc.csc.UpdateClusterAutoscaler(ctx, internalID, builder)
	if err != nil {
		span.RecordError(err)
	}

	// FIXME Can't call tracing.SetClusterAttributes to identify the cluster.
	//       Do we need a tracing function that picks apart an InternalID?

	return autoscaler, err
}

func (csc *clusterServiceClientWithTracing) DeleteCluster(ctx context.Context, internalID InternalID) error {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.DeleteCluster")
	defer span.End()

	span.SetAttributes(
		tracing.ClusterIDKey.String(internalID.ID()),
	)
	err := csc.csc.DeleteCluster(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (csc *clusterServiceClientWithTracing) ListClusters(searchExpression string) ClusterListIterator {
	return csc.csc.ListClusters(searchExpression)
}

func (csc *clusterServiceClientWithTracing) GetNodePool(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePool, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.GetNodePool")
	defer span.End()

	nodePool, err := csc.csc.GetNodePool(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetNodePoolAttributes(span, nodePool)
	}

	return nodePool, err
}

func (csc *clusterServiceClientWithTracing) GetNodePoolStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePoolStatus, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.GetNodePoolStatus")
	defer span.End()

	nodePoolStatus, err := csc.csc.GetNodePoolStatus(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetNodePoolStatusAttributes(span, nodePoolStatus)
	}

	return nodePoolStatus, err
}

func (csc *clusterServiceClientWithTracing) PostNodePool(ctx context.Context, clusterInternalID InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.PostNodePool")
	defer span.End()

	nodePool, err := csc.csc.PostNodePool(ctx, clusterInternalID, builder)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetNodePoolAttributes(span, nodePool)
	}

	return nodePool, err
}

func (csc *clusterServiceClientWithTracing) UpdateNodePool(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.UpdateNodePool")
	defer span.End()

	nodePool, err := csc.csc.UpdateNodePool(ctx, internalID, builder)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetNodePoolAttributes(span, nodePool)
	}

	return nodePool, err
}

func (csc *clusterServiceClientWithTracing) DeleteNodePool(ctx context.Context, internalID InternalID) error {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.DeleteNodePool")
	defer span.End()

	span.SetAttributes(
		tracing.NodePoolIDKey.String(internalID.ID()),
	)
	err := csc.csc.DeleteNodePool(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (csc *clusterServiceClientWithTracing) ListNodePools(clusterInternalID InternalID, searchExpression string) NodePoolListIterator {
	return csc.csc.ListNodePools(clusterInternalID, searchExpression)
}

func (csc *clusterServiceClientWithTracing) GetBreakGlassCredential(ctx context.Context, internalID InternalID) (*cmv1.BreakGlassCredential, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.GetBreakGlassCredential")
	defer span.End()

	credential, err := csc.csc.GetBreakGlassCredential(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetBreakGlassCredentialAttributes(span, credential)
	}

	return credential, err
}

func (csc *clusterServiceClientWithTracing) GetExternalAuth(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ExternalAuth, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.GetExternalAuth")
	defer span.End()

	externalAuth, err := csc.csc.GetExternalAuth(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetExternalAuthAttributes(span, externalAuth)
	}

	return externalAuth, err
}

func (csc *clusterServiceClientWithTracing) PostExternalAuth(ctx context.Context, clusterInternalID InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.PostExternalAuth")
	defer span.End()

	externalAuth, err := csc.csc.PostExternalAuth(ctx, clusterInternalID, builder)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetExternalAuthAttributes(span, externalAuth)
	}

	return externalAuth, err
}

func (csc *clusterServiceClientWithTracing) UpdateExternalAuth(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.UpdateExternalAuth")
	defer span.End()

	externalAuth, err := csc.csc.UpdateExternalAuth(ctx, internalID, builder)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetExternalAuthAttributes(span, externalAuth)
	}

	return externalAuth, err
}

func (csc *clusterServiceClientWithTracing) DeleteExternalAuth(ctx context.Context, internalID InternalID) error {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.DeleteExternalAuth")
	defer span.End()

	span.SetAttributes(
		tracing.ExternalAuthIDKey.String(internalID.ID()),
	)
	err := csc.csc.DeleteExternalAuth(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (csc *clusterServiceClientWithTracing) ListExternalAuths(clusterInternalID InternalID, searchExpression string) ExternalAuthListIterator {
	return csc.csc.ListExternalAuths(clusterInternalID, searchExpression)
}

func (csc *clusterServiceClientWithTracing) PostBreakGlassCredential(ctx context.Context, clusterInternalID InternalID) (*cmv1.BreakGlassCredential, error) {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.PostBreakGlassCredential")
	defer span.End()

	credential, err := csc.csc.PostBreakGlassCredential(ctx, clusterInternalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetBreakGlassCredentialAttributes(span, credential)
	}

	return credential, err
}

func (csc *clusterServiceClientWithTracing) DeleteBreakGlassCredentials(ctx context.Context, clusterInternalID InternalID) error {
	ctx, span := csc.startChildSpan(ctx, "ClusterServiceClient.DeleteBreakGlassCredentials")
	defer span.End()

	span.SetAttributes(
		tracing.ClusterIDKey.String(clusterInternalID.ID()),
	)
	err := csc.csc.DeleteBreakGlassCredentials(ctx, clusterInternalID)
	if err != nil {
		span.RecordError(err)
	}

	return err
}

func (csc *clusterServiceClientWithTracing) ListBreakGlassCredentials(clusterInternalID InternalID, searchExpression string) *BreakGlassCredentialListIterator {
	return csc.csc.ListBreakGlassCredentials(clusterInternalID, searchExpression)
}

func (csc *clusterServiceClientWithTracing) GetVersion(ctx context.Context, versionName string) (*arohcpv1alpha1.Version, error) {
	return csc.csc.GetVersion(ctx, versionName)
}

func (csc *clusterServiceClientWithTracing) ListVersions() *VersionsListIterator {
	return csc.csc.ListVersions()
}

func (csc *clusterServiceClientWithTracing) GetClusterProvisionShard(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ProvisionShard, error) {
	return csc.csc.GetClusterProvisionShard(ctx, internalID)
}
