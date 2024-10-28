package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"

	sdk "github.com/openshift-online/ocm-sdk-go"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift-online/ocm-sdk-go/errors"
)

// MockClusterServiceClient allows for unit testing functions
// that make calls to the ClusterServiceClient interface.
type MockClusterServiceClient struct {
	clusters  map[InternalID](*cmv1.Cluster)
	nodePools map[InternalID](*cmv1.NodePool)
}

// mockNotFoundError is based on errors.SendNotFound.
func mockNotFoundError(internalID InternalID) error {
	reason := fmt.Sprintf("Can't find resource for path '%s'", internalID)
	// ErrorBuilder.Build() never returns an error.
	body, _ := errors.NewError().
		ID("404").
		Reason(reason).
		Build()
	return body
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

func (csc *MockClusterServiceClient) AddProperties(builder *cmv1.ClusterBuilder) *cmv1.ClusterBuilder {
	additionalProperties := getDefaultAdditionalProperities()
	return builder.Properties(additionalProperties)
}

func (mcsc *MockClusterServiceClient) GetCSCluster(ctx context.Context, internalID InternalID) (*cmv1.Cluster, error) {
	cluster, ok := mcsc.clusters[internalID]

	if !ok {
		return nil, mockNotFoundError(internalID)
	}
	return cluster, nil
}

func (mcsc *MockClusterServiceClient) PostCSCluster(ctx context.Context, cluster *cmv1.Cluster) (*cmv1.Cluster, error) {
	href := GenerateClusterHREF(cluster.Name())
	// Adding the HREF to correspond with what the full client does when crating the body
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
		return nil, mockNotFoundError(internalID)
	}
	mcsc.clusters[internalID] = cluster
	return cluster, nil

}

func (mcsc *MockClusterServiceClient) DeleteCSCluster(ctx context.Context, internalID InternalID) error {
	_, ok := mcsc.clusters[internalID]
	if !ok {
		return mockNotFoundError(internalID)
	}
	delete(mcsc.clusters, internalID)
	return nil
}

func (mcsc *MockClusterServiceClient) GetCSNodePool(ctx context.Context, internalID InternalID) (*cmv1.NodePool, error) {
	nodePool, ok := mcsc.nodePools[internalID]
	if !ok {
		return nil, mockNotFoundError(internalID)
	}
	return nodePool, nil

}

func (mcsc *MockClusterServiceClient) PostCSNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *cmv1.NodePool) (*cmv1.NodePool, error) {
	href := GenerateNodePoolHREF(clusterInternalID.path, nodePool.ID())
	// Adding the HREF to correspond with what the full client does when crating the body
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
		return nil, mockNotFoundError(internalID)
	}
	mcsc.nodePools[internalID] = nodePool
	return nodePool, nil
}

func (mcsc *MockClusterServiceClient) DeleteCSNodePool(ctx context.Context, internalID InternalID) error {
	_, ok := mcsc.nodePools[internalID]
	if !ok {
		return mockNotFoundError(internalID)
	}
	delete(mcsc.nodePools, internalID)
	return nil
}
