package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"

	sdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

type ClusterServiceClientSpec interface {
	AddProperties(builder *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder
	GetCluster(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.Cluster, error)
	GetClusterStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ClusterStatus, error)
	PostCluster(ctx context.Context, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error)
	UpdateCluster(ctx context.Context, internalID InternalID, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error)
	DeleteCluster(ctx context.Context, internalID InternalID) error
	ListClusters(searchExpression string) ClusterListIterator
	GetNodePool(ctx context.Context, internalID InternalID) (*cmv1.NodePool, error)
	PostNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error)
	UpdateNodePool(ctx context.Context, internalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error)
	DeleteNodePool(ctx context.Context, internalID InternalID) error
	ListNodePools(clusterInternalID InternalID, searchExpression string) NodePoolListIterator
}

type ClusterServiceClient struct {
	// Conn is an ocm-sdk-go connection to Cluster Service
	Conn *sdk.Connection

	// ProvisionShardID sets the provision_shard_id property for all cluster requests to Cluster Service, which pins all
	// cluster requests to Cluster Service to a specific shard during testing
	ProvisionShardID *string

	// ProvisionerNoOpProvision sets the provisioner_noop_provision property for all cluster requests to Cluster
	// Service, which short-circuits the full provision flow during testing
	ProvisionerNoOpProvision bool

	// ProvisionerNoOpDeprovision sets the provisioner_noop_deprovision property for all cluster requests to Cluster
	// Service, which short-circuits the full deprovision flow during testing
	ProvisionerNoOpDeprovision bool
}

// AddProperties injects the some additional properties into the ClusterBuilder.
func (csc *ClusterServiceClient) AddProperties(builder *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
	additionalProperties := map[string]string{}
	if csc.ProvisionShardID != nil {
		additionalProperties["provision_shard_id"] = *csc.ProvisionShardID
	}
	if csc.ProvisionerNoOpProvision {
		additionalProperties["provisioner_noop_provision"] = "true"
	}
	if csc.ProvisionerNoOpDeprovision {
		additionalProperties["provisioner_noop_deprovision"] = "true"
	}
	return builder.Properties(additionalProperties)
}

// GetCluster creates and sends a GET request to fetch a cluster from Clusters Service
func (csc *ClusterServiceClient) GetCluster(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.Cluster, error) {
	client, ok := internalID.GetAroHCPClusterClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	clusterGetResponse, err := client.Get().SendContext(ctx)
	if err != nil {
		return nil, err
	}
	cluster, ok := clusterGetResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return cluster, nil
}

// GetClusterStatus creates and sends a GET request to fetch a cluster's status from Clusters Service
func (csc *ClusterServiceClient) GetClusterStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ClusterStatus, error) {
	client, ok := internalID.GetAroHCPClusterClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	clusterStatusGetResponse, err := client.Status().Get().SendContext(ctx)
	if err != nil {
		return nil, err
	}
	status, ok := clusterStatusGetResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return status, nil
}

// PostCluster creates and sends a POST request to create a cluster in Clusters Service
func (csc *ClusterServiceClient) PostCluster(ctx context.Context, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error) {
	clustersAddResponse, err := csc.Conn.AroHCP().V1alpha1().Clusters().Add().Body(cluster).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	cluster, ok := clustersAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return cluster, nil
}

// UpdateCluster sends a PATCH request to update a cluster in Clusters Service
func (csc *ClusterServiceClient) UpdateCluster(ctx context.Context, internalID InternalID, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error) {
	client, ok := internalID.GetAroHCPClusterClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	clusterUpdateResponse, err := client.Update().Body(cluster).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	cluster, ok = clusterUpdateResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return cluster, nil
}

// DeleteCluster creates and sends a DELETE request to delete a cluster from Clusters Service
func (csc *ClusterServiceClient) DeleteCluster(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetAroHCPClusterClient(csc.Conn)
	if !ok {
		return fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

// ListClusters prepares a GET request with the given search expression. Call Items() on
// the returned iterator in a for/range loop to execute the request and paginate over results,
// then call GetError() to check for an iteration error.
func (csc *ClusterServiceClient) ListClusters(searchExpression string) ClusterListIterator {
	clustersListRequest := csc.Conn.AroHCP().V1alpha1().Clusters().List()
	if searchExpression != "" {
		clustersListRequest.Search(searchExpression)
	}
	return ClusterListIterator{request: clustersListRequest}
}

// GetNodePool creates and sends a GET request to fetch a node pool from Clusters Service
func (csc *ClusterServiceClient) GetNodePool(ctx context.Context, internalID InternalID) (*cmv1.NodePool, error) {
	client, ok := internalID.GetNodePoolClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	nodePoolGetResponse, err := client.Get().SendContext(ctx)
	if err != nil {
		return nil, err
	}
	nodePool, ok := nodePoolGetResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return nodePool, nil
}

// PostNodePool creates and sends a POST request to create a node pool in Clusters Service
func (csc *ClusterServiceClient) PostNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error) {
	client, ok := clusterInternalID.GetClusterClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)
	}
	nodePoolsAddResponse, err := client.NodePools().Add().Body(nodePool).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	nodePool, ok = nodePoolsAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return nodePool, nil
}

// UpdateNodePool sends a PATCH request to update a node pool in Clusters Service
func (csc *ClusterServiceClient) UpdateNodePool(ctx context.Context, internalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error) {
	client, ok := internalID.GetNodePoolClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	nodePoolUpdateResponse, err := client.Update().Body(nodePool).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	nodePool, ok = nodePoolUpdateResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return nodePool, nil
}

// DeleteNodePool creates and sends a DELETE request to delete a node pool from Clusters Service
func (csc *ClusterServiceClient) DeleteNodePool(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetNodePoolClient(csc.Conn)
	if !ok {
		return fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

// ListNodePools prepares a GET request with the given search expression. Call Items() on
// the returned iterator in a for/range loop to execute the request and paginate over results,
// then call GetError() to check for an iteration error.
func (csc *ClusterServiceClient) ListNodePools(clusterInternalID InternalID, searchExpression string) NodePoolListIterator {
	client, ok := clusterInternalID.GetClusterClient(csc.Conn)
	if !ok {
		return NodePoolListIterator{err: fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)}
	}
	nodePoolsListRequest := client.NodePools().List()
	if searchExpression != "" {
		nodePoolsListRequest.Search(searchExpression)
	}
	return NodePoolListIterator{request: nodePoolsListRequest}
}
