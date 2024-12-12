package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"

	sdk "github.com/openshift-online/ocm-sdk-go"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

type ClusterServiceClientSpec interface {
	GetConn() *sdk.Connection
	AddProperties(builder *cmv1.ClusterBuilder) *cmv1.ClusterBuilder
	GetCSCluster(ctx context.Context, internalID InternalID) (*cmv1.Cluster, error)
	PostCSCluster(ctx context.Context, cluster *cmv1.Cluster) (*cmv1.Cluster, error)
	UpdateCSCluster(ctx context.Context, internalID InternalID, cluster *cmv1.Cluster) (*cmv1.Cluster, error)
	DeleteCSCluster(ctx context.Context, internalID InternalID) error
	GetCSNodePool(ctx context.Context, internalID InternalID) (*cmv1.NodePool, error)
	PostCSNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error)
	UpdateCSNodePool(ctx context.Context, internalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error)
	DeleteCSNodePool(ctx context.Context, internalID InternalID) error
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

func (csc *ClusterServiceClient) GetConn() *sdk.Connection { return csc.Conn }

// AddProperties injects the some addtional properties into the CSCluster Object.
func (csc *ClusterServiceClient) AddProperties(builder *cmv1.ClusterBuilder) *cmv1.ClusterBuilder {
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

// GetCSCluster creates and sends a GET request to fetch a cluster from Clusters Service
func (csc *ClusterServiceClient) GetCSCluster(ctx context.Context, internalID InternalID) (*cmv1.Cluster, error) {
	client, ok := internalID.GetClusterClient(csc.Conn)
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

// GetCSClusterStatus creates and sends a GET request to fetch a cluster's status from Clusters Service
func (csc *ClusterServiceClient) GetCSClusterStatus(ctx context.Context, internalID InternalID) (*cmv1.ClusterStatus, error) {
	client, ok := internalID.GetClusterClient(csc.Conn)
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

// PostCSCluster creates and sends a POST request to create a cluster in Clusters Service
func (csc *ClusterServiceClient) PostCSCluster(ctx context.Context, cluster *cmv1.Cluster) (*cmv1.Cluster, error) {
	clustersAddResponse, err := csc.Conn.ClustersMgmt().V1().Clusters().Add().Body(cluster).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	cluster, ok := clustersAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return cluster, nil
}

// UpdateCSCluster sends a PATCH request to update a cluster in Clusters Service
func (csc *ClusterServiceClient) UpdateCSCluster(ctx context.Context, internalID InternalID, cluster *cmv1.Cluster) (*cmv1.Cluster, error) {
	client, ok := internalID.GetClusterClient(csc.Conn)
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

// DeleteCSCluster creates and sends a DELETE request to delete a cluster from Clusters Service
func (csc *ClusterServiceClient) DeleteCSCluster(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetClusterClient(csc.Conn)
	if !ok {
		return fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

// GetCSNodePool creates and sends a GET request to fetch a node pool from Clusters Service
func (csc *ClusterServiceClient) GetCSNodePool(ctx context.Context, internalID InternalID) (*cmv1.NodePool, error) {
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

// PostCSNodePool creates and sends a POST request to create a node pool in Clusters Service
func (csc *ClusterServiceClient) PostCSNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error) {
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

// UpdateCSNodePool sends a PATCH request to update a node pool in Clusters Service
func (csc *ClusterServiceClient) UpdateCSNodePool(ctx context.Context, internalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error) {
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

// DeleteCSNodePool creates and sends a DELETE request to delete a node pool from Clusters Service
func (csc *ClusterServiceClient) DeleteCSNodePool(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetNodePoolClient(csc.Conn)
	if !ok {
		return fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}
