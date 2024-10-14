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
	GetProvisionShardID() *string
	GetProvisionerNoOpProvision() bool
	GetProvisionerNoOpDeprovision() bool
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

func (csc *ClusterServiceClient) GetConn() *sdk.Connection     { return csc.Conn }
func (csc *ClusterServiceClient) GetProvisionShardID() *string { return csc.ProvisionShardID }
func (csc *ClusterServiceClient) GetProvisionerNoOpProvision() bool {
	return csc.ProvisionerNoOpProvision
}
func (csc *ClusterServiceClient) GetProvisionerNoOpDeprovision() bool {
	return csc.ProvisionerNoOpDeprovision
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

type MockClusterServiceClient struct {
	clusters  map[InternalID](*cmv1.Cluster)
	nodePools map[InternalID](*cmv1.NodePool)
}

// NewCache initializes a new Cache to allow for simple tests without needing a real CosmosDB. For production, use
// NewCosmosDBConfig instead.
func NewMockClusterServiceClient() MockClusterServiceClient {
	return MockClusterServiceClient{
		clusters:  make(map[InternalID]*cmv1.Cluster),
		nodePools: make(map[InternalID]*cmv1.NodePool),
	}
}

func (mcsc *MockClusterServiceClient) GetConn() *sdk.Connection { panic("GetConn not implemented") }
func (mcsc *MockClusterServiceClient) GetProvisionShardID() *string {
	mockProvisionShardID := "mock-client"
	return &mockProvisionShardID
}
func (mcsc *MockClusterServiceClient) GetProvisionerNoOpProvision() bool {
	return false
}
func (mcsc *MockClusterServiceClient) GetProvisionerNoOpDeprovision() bool {
	return false
}

func (mcsc *MockClusterServiceClient) GetCSCluster(ctx context.Context, internalID InternalID) (*cmv1.Cluster, error) {
	cluster, ok := mcsc.clusters[internalID]

	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return cluster, nil
}

func (mcsc *MockClusterServiceClient) PostCSCluster(ctx context.Context, cluster *cmv1.Cluster) (*cmv1.Cluster, error) {
	href := GenerateClusterHREF(cluster.Name())
	clusterBuilder := cmv1.NewCluster()
	enrichedCluster, err := clusterBuilder.Copy(cluster).HREF(href).Build()
	if err != nil {
		return nil, err
	}
	internalID, err := NewInternalID(href)
	if err != nil {
		return nil, err
	}
	mcsc.clusters[internalID] = enrichedCluster
	return enrichedCluster, nil
}

func (mcsc *MockClusterServiceClient) UpdateCSCluster(ctx context.Context, internalID InternalID, cluster *cmv1.Cluster) (*cmv1.Cluster, error) {

	_, ok := mcsc.clusters[internalID]
	if !ok {
		return nil, fmt.Errorf("Not Found")
	}

	mcsc.clusters[internalID] = cluster
	return cluster, nil

}

func (mcsc *MockClusterServiceClient) DeleteCSCluster(ctx context.Context, internalID InternalID) error {
	_, ok := mcsc.clusters[internalID]

	if !ok {
		return fmt.Errorf("Not Found")
	}
	delete(mcsc.clusters, internalID)
	return nil
}

func (mcsc *MockClusterServiceClient) GetCSNodePool(ctx context.Context, internalID InternalID) (*cmv1.NodePool, error) {
	nodePool, ok := mcsc.nodePools[internalID]

	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return nodePool, nil

}

func (mcsc *MockClusterServiceClient) PostCSNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error) {
	href := GenerateNodePoolHREF(clusterInternalID.path, nodePool.ID())
	npBuilder := cmv1.NewNodePool()
	enrichedNodePool, err := npBuilder.Copy(nodePool).HREF(href).Build()
	if err != nil {
		return nil, err
	}

	internalID, err := NewInternalID(href)
	if err != nil {
		return nil, err
	}
	mcsc.nodePools[internalID] = enrichedNodePool
	return enrichedNodePool, nil
}

func (mcsc *MockClusterServiceClient) UpdateCSNodePool(ctx context.Context, internalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error) {
	_, ok := mcsc.nodePools[internalID]
	if !ok {
		return nil, fmt.Errorf("Not Found")
	}
	mcsc.nodePools[internalID] = nodePool
	return nodePool, nil
}

func (mcsc *MockClusterServiceClient) DeleteCSNodePool(ctx context.Context, internalID InternalID) error {
	_, ok := mcsc.nodePools[internalID]

	if !ok {
		return fmt.Errorf("Not Found")
	}
	delete(mcsc.nodePools, internalID)
	return nil
}
