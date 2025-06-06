package ocm

import (
	"context"

	"github.com/Azure/ARO-HCP/internal/tracing"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"go.opentelemetry.io/otel/trace"
)

type clusterServiceClientWithTracing struct {
	csc        ClusterServiceClientSpec
	tracerName string
}

func NewClusterServiceClientWithTracing(csc ClusterServiceClientSpec, tracerName string) ClusterServiceClientSpec {
	return &clusterServiceClientWithTracing{
		csc:        csc,
		tracerName: tracerName,
	}
}

func (csc *clusterServiceClientWithTracing) AddProperties(builder *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
	return csc.csc.AddProperties(builder)
}

func (csc *clusterServiceClientWithTracing) GetCluster(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.Cluster, error) {
	span := trace.SpanFromContext(ctx)

	cluster, err := csc.csc.GetCluster(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetClusterAttributes(span, cluster)
	}

	return cluster, err
}

func (csc *clusterServiceClientWithTracing) GetClusterStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ClusterStatus, error) {
	span := trace.SpanFromContext(ctx)

	clusterStatus, err := csc.csc.GetClusterStatus(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetClusterStatusAttributes(span, clusterStatus)
	}

	return clusterStatus, err
}

func (csc *clusterServiceClientWithTracing) GetClusterInflightChecks(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.InflightCheckList, error) {
	return csc.csc.GetClusterInflightChecks(ctx, internalID)
}

func (csc *clusterServiceClientWithTracing) PostCluster(ctx context.Context, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error) {
	span := trace.SpanFromContext(ctx)

	cluster, err := csc.csc.PostCluster(ctx, cluster)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetClusterAttributes(span, cluster)
	}

	return cluster, err
}

func (csc *clusterServiceClientWithTracing) UpdateCluster(ctx context.Context, internalID InternalID, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error) {
	span := trace.SpanFromContext(ctx)

	cluster, err := csc.csc.UpdateCluster(ctx, internalID, cluster)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetClusterAttributes(span, cluster)
	}

	return cluster, err
}

func (csc *clusterServiceClientWithTracing) DeleteCluster(ctx context.Context, internalID InternalID) error {
	span := trace.SpanFromContext(ctx)

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
	span := trace.SpanFromContext(ctx)

	nodePool, err := csc.csc.GetNodePool(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetNodePoolAttributes(span, nodePool)
	}

	return nodePool, err
}

func (csc *clusterServiceClientWithTracing) GetNodePoolStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePoolStatus, error) {
	span := trace.SpanFromContext(ctx)

	nodePoolStatus, err := csc.csc.GetNodePoolStatus(ctx, internalID)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetNodePoolStatusAttributes(span, nodePoolStatus)
	}

	return nodePoolStatus, err
}

func (csc *clusterServiceClientWithTracing) PostNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error) {
	span := trace.SpanFromContext(ctx)

	nodePool, err := csc.csc.PostNodePool(ctx, clusterInternalID, nodePool)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetNodePoolAttributes(span, nodePool)
	}

	return nodePool, err
}

func (csc *clusterServiceClientWithTracing) UpdateNodePool(ctx context.Context, internalID InternalID, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error) {
	span := trace.SpanFromContext(ctx)

	nodePool, err := csc.csc.UpdateNodePool(ctx, internalID, nodePool)
	if err != nil {
		span.RecordError(err)
	} else {
		tracing.SetNodePoolAttributes(span, nodePool)
	}

	return nodePool, err
}

func (csc *clusterServiceClientWithTracing) DeleteNodePool(ctx context.Context, internalID InternalID) error {
	span := trace.SpanFromContext(ctx)

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
	return csc.csc.GetBreakGlassCredential(ctx, internalID)
}

func (csc *clusterServiceClientWithTracing) PostBreakGlassCredential(ctx context.Context, clusterInternalID InternalID) (*cmv1.BreakGlassCredential, error) {
	return csc.csc.PostBreakGlassCredential(ctx, clusterInternalID)
}

func (csc *clusterServiceClientWithTracing) DeleteBreakGlassCredentials(ctx context.Context, clusterInternalID InternalID) error {
	return csc.csc.DeleteBreakGlassCredentials(ctx, clusterInternalID)
}

func (csc *clusterServiceClientWithTracing) ListBreakGlassCredentials(clusterInternalID InternalID, searchExpression string) BreakGlassCredentialListIterator {
	return csc.csc.ListBreakGlassCredentials(clusterInternalID, searchExpression)
}
