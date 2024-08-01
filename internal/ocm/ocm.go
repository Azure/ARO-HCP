package ocm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"

	sdk "github.com/openshift-online/ocm-sdk-go"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

type ClusterServiceConfig struct {
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

// GetCSCluster creates and sends a GET request to fetch a cluster from Clusters Service
func (csc *ClusterServiceConfig) GetCSCluster(clusterID string) (*cmv1.Cluster, error) {
	clusterGetResponse, err := csc.Conn.ClustersMgmt().V1().Clusters().Cluster(clusterID).Get().Send()
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
func (csc *ClusterServiceConfig) PostCSCluster(cluster *cmv1.Cluster) (*cmv1.Cluster, error) {
	clustersAddResponse, err := csc.Conn.ClustersMgmt().V1().Clusters().Add().Body(cluster).Send()
	if err != nil {
		return nil, err
	}
	cluster, ok := clustersAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return cluster, nil
}

// UpdateCSCluster sends a POST request to update a cluster in Clusters Service
func (csc *ClusterServiceConfig) UpdateCSCluster(clusterID string, cluster *cmv1.Cluster) (*cmv1.Cluster, error) {
	clusterUpdateResponse, err := csc.Conn.ClustersMgmt().V1().Clusters().Cluster(clusterID).Update().Body(cluster).Send()
	if err != nil {
		return nil, err
	}
	cluster, ok := clusterUpdateResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return cluster, nil
}

// DeleteCSCluster creates and sends a DELETE request to delete a cluster from Clusters Service
func (csc *ClusterServiceConfig) DeleteCSCluster(clusterID string) error {
	_, err := csc.Conn.ClustersMgmt().V1().Clusters().Cluster(clusterID).Delete().Send()
	return err
}

func (csc *ClusterServiceConfig) GetCSNodePool(clusterID, nodePoolID string) (*cmv1.NodePool, error) {
	nodePoolGetResponse, err := csc.Conn.ClustersMgmt().V1().Clusters().Cluster(clusterID).NodePools().NodePool(nodePoolID).Get().Send()
	if err != nil {
		return nil, err
	}
	nodePool, ok := nodePoolGetResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return nodePool, nil
}

func (csc *ClusterServiceConfig) CreateCSNodePool(clusterID string, nodePool *cmv1.NodePool) (*cmv1.NodePool, error) {
	nodePoolsAddResponse, err := csc.Conn.ClustersMgmt().V1().Clusters().Cluster(clusterID).NodePools().Add().Body(nodePool).Send()
	if err != nil {
		return nil, err
	}
	nodePool, ok := nodePoolsAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return nodePool, nil
}

func (csc *ClusterServiceConfig) DeleteCSNodePool(clusterID, nodePoolID string) error {
	_, err := csc.Conn.ClustersMgmt().V1().Clusters().Cluster(clusterID).NodePools().NodePool(nodePoolID).Delete().Send()
	return err
}
