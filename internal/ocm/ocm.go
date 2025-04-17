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
	"fmt"

	sdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

type ClusterServiceClientSpec interface {
	// AddProperties injects the some additional properties into the ClusterBuilder.
	AddProperties(builder *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder

	// GetCluster sends a GET request to fetch a cluster from Cluster Service.
	GetCluster(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.Cluster, error)

	// GetClusterStatus sends a GET request to fetch a cluster's status from Cluster Service.
	GetClusterStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ClusterStatus, error)

	// GetClusterInflightChecks sends a GET request to fetch a cluster's inflight checks from Cluster Service.
	GetClusterInflightChecks(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.InflightCheckList, error)

	// PostCluster sends a POST request to create a cluster in Cluster Service.
	PostCluster(ctx context.Context, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error)

	// UpdateCluster sends a PATCH request to update a cluster in Cluster Service.
	UpdateCluster(ctx context.Context, internalID InternalID, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error)

	// DeleteCluster sends a DELETE request to delete a cluster from Cluster Service.
	DeleteCluster(ctx context.Context, internalID InternalID) error

	// ListClusters prepares a GET request with the given search expression. Call Items() on
	// the returned iterator in a for/range loop to execute the request and paginate over results,
	// then call GetError() to check for an iteration error.
	ListClusters(searchExpression string) ClusterListIterator

	// GetNodePool sends a GET request to fetch a node pool from Cluster Service.
	GetNodePool(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePool, error)

	// GetNodePoolStatus sends a GET request to fetch a node pool's status from Cluster Service.
	GetNodePoolStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePoolStatus, error)

	// PostNodePool sends a POST request to create a node pool in Cluster Service.
	PostNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error)

	// UpdateNodePool sends a PATCH request to update a node pool in Cluster Service.
	UpdateNodePool(ctx context.Context, internalID InternalID, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error)

	// DeleteNodePool sends a DELETE request to delete a node pool from Cluster Service.
	DeleteNodePool(ctx context.Context, internalID InternalID) error

	// ListNodePools prepares a GET request with the given search expression. Call Items() on
	// the returned iterator in a for/range loop to execute the request and paginate over results,
	// then call GetError() to check for an iteration error.
	ListNodePools(clusterInternalID InternalID, searchExpression string) NodePoolListIterator

	// GetBreakGlassCredential sends a GET request to fetch a break-glass cluster credential from Cluster Service.
	GetBreakGlassCredential(ctx context.Context, internalID InternalID) (*cmv1.BreakGlassCredential, error)

	// PostBreakGlassCredential sends a POST request to create a break-glass cluster credential in Cluster Service.
	PostBreakGlassCredential(ctx context.Context, clusterInternalID InternalID) (*cmv1.BreakGlassCredential, error)

	// DeleteBreakGlassCredentials sends a DELETE request to revoke all break-glass credentials for a cluster in Cluster Service.
	DeleteBreakGlassCredentials(ctx context.Context, clusterInternalID InternalID) error

	// ListBreakGlassCredentials prepares a GET request with the given search expression. Call
	// Items() on the returned iterator in a for/range loop to execute the request and paginate
	// over results, then call GetError() to check for an iteration error.
	ListBreakGlassCredentials(clusterInternalID InternalID, searchExpression string) BreakGlassCredentialListIterator
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

func (csc *ClusterServiceClient) GetClusterInflightChecks(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.InflightCheckList, error) {
	client, ok := internalID.GetAroHCPClusterClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	clusterInflightChecksResponse, err := client.InflightChecks().List().SendContext(ctx)
	if err != nil {
		return nil, err
	}
	inflightChecks, ok := clusterInflightChecksResponse.GetItems()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return inflightChecks, nil
}

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

func (csc *ClusterServiceClient) DeleteCluster(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetAroHCPClusterClient(csc.Conn)
	if !ok {
		return fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

func (csc *ClusterServiceClient) ListClusters(searchExpression string) ClusterListIterator {
	clustersListRequest := csc.Conn.AroHCP().V1alpha1().Clusters().List()
	if searchExpression != "" {
		clustersListRequest.Search(searchExpression)
	}
	return ClusterListIterator{request: clustersListRequest}
}

func (csc *ClusterServiceClient) GetNodePool(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePool, error) {
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

func (csc *ClusterServiceClient) GetNodePoolStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePoolStatus, error) {
	client, ok := internalID.GetNodePoolClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	nodePoolStatusGetResponse, err := client.Status().Get().SendContext(ctx)
	if err != nil {
		return nil, err
	}
	status, ok := nodePoolStatusGetResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return status, nil
}

func (csc *ClusterServiceClient) PostNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error) {
	client, ok := clusterInternalID.GetAroHCPClusterClient(csc.Conn)
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

func (csc *ClusterServiceClient) UpdateNodePool(ctx context.Context, internalID InternalID, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error) {
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

func (csc *ClusterServiceClient) DeleteNodePool(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetNodePoolClient(csc.Conn)
	if !ok {
		return fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

func (csc *ClusterServiceClient) ListNodePools(clusterInternalID InternalID, searchExpression string) NodePoolListIterator {
	client, ok := clusterInternalID.GetAroHCPClusterClient(csc.Conn)
	if !ok {
		return NodePoolListIterator{err: fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)}
	}
	nodePoolsListRequest := client.NodePools().List()
	if searchExpression != "" {
		nodePoolsListRequest.Search(searchExpression)
	}
	return NodePoolListIterator{request: nodePoolsListRequest}
}

func (csc *ClusterServiceClient) GetBreakGlassCredential(ctx context.Context, internalID InternalID) (*cmv1.BreakGlassCredential, error) {
	client, ok := internalID.GetBreakGlassCredentialClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a break-glass credential: %s", internalID)
	}
	breakGlassCredentialGetResponse, err := client.Get().SendContext(ctx)
	if err != nil {
		return nil, err
	}
	breakGlassCredential, ok := breakGlassCredentialGetResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return breakGlassCredential, nil
}

func (csc *ClusterServiceClient) PostBreakGlassCredential(ctx context.Context, clusterInternalID InternalID) (*cmv1.BreakGlassCredential, error) {
	client, ok := clusterInternalID.GetClusterClient(csc.Conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)
	}
	breakGlassCredential, err := cmv1.NewBreakGlassCredential().Build()
	if err != nil {
		return nil, err
	}
	breakGlassCredentialsAddResponse, err := client.BreakGlassCredentials().Add().Body(breakGlassCredential).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	breakGlassCredential, ok = breakGlassCredentialsAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return breakGlassCredential, nil
}

func (csc *ClusterServiceClient) DeleteBreakGlassCredentials(ctx context.Context, clusterInternalID InternalID) error {
	client, ok := clusterInternalID.GetClusterClient(csc.Conn)
	if !ok {
		return fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)
	}
	_, err := client.BreakGlassCredentials().Delete().SendContext(ctx)
	return err
}

func (csc *ClusterServiceClient) ListBreakGlassCredentials(clusterInternalID InternalID, searchExpression string) BreakGlassCredentialListIterator {
	client, ok := clusterInternalID.GetClusterClient(csc.Conn)
	if !ok {
		return BreakGlassCredentialListIterator{err: fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)}
	}
	breakGlassCredentialsListRequest := client.BreakGlassCredentials().List()
	if searchExpression != "" {
		breakGlassCredentialsListRequest.Search(searchExpression)
	}
	return BreakGlassCredentialListIterator{request: breakGlassCredentialsListRequest}
}
