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

	"strings"

	sdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api"
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

	// GetExternalAuth sends a GET request to fetch a node pool from Cluster Service.
	GetExternalAuth(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ExternalAuth, error)

	// PostExternalAuth sends a POST request to create a node pool in Cluster Service.
	PostExternalAuth(ctx context.Context, clusterInternalID InternalID, nodePool *arohcpv1alpha1.ExternalAuth) (*arohcpv1alpha1.ExternalAuth, error)

	// UpdateExternalAuth sends a PATCH request to update a node pool in Cluster Service.
	UpdateExternalAuth(ctx context.Context, internalID InternalID, nodePool *arohcpv1alpha1.ExternalAuth) (*arohcpv1alpha1.ExternalAuth, error)

	// DeleteExternalAuth sends a DELETE request to delete a node pool from Cluster Service.
	DeleteExternalAuth(ctx context.Context, internalID InternalID) error

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

	// GetVersion sends a GET request to fetch cluster version
	GetVersion(ctx context.Context, versionName string) (*arohcpv1alpha1.Version, error)

	// ListVersions prepares a GET request. Call Items() on
	// the returned iterator in a for/range loop to execute the request and paginate over results,
	// then call GetError() to check for an iteration error.
	ListVersions() VersionsListIterator
}

type clusterServiceClient struct {
	// Conn is an ocm-sdk-go connection to Cluster Service
	conn *sdk.Connection

	// ProvisionShardID sets the provision_shard_id property for all cluster requests to Cluster Service, which pins all
	// cluster requests to Cluster Service to a specific shard during testing
	provisionShardID string

	// ProvisionerNoOpProvision sets the provisioner_noop_provision property for all cluster requests to Cluster
	// Service, which short-circuits the full provision flow during testing
	provisionerNoOpProvision bool

	// ProvisionerNoOpDeprovision sets the provisioner_noop_deprovision property for all cluster requests to Cluster
	// Service, which short-circuits the full deprovision flow during testing
	provisionerNoOpDeprovision bool
}

func NewClusterServiceClient(conn *sdk.Connection, provisionShardID string, provisionerNoOpProvision, provisionerNoOpDeprovision bool) ClusterServiceClientSpec {
	return &clusterServiceClient{
		conn:                       conn,
		provisionShardID:           provisionShardID,
		provisionerNoOpProvision:   provisionerNoOpProvision,
		provisionerNoOpDeprovision: provisionerNoOpDeprovision,
	}
}

func (csc *clusterServiceClient) AddProperties(builder *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
	additionalProperties := map[string]string{}
	if csc.provisionShardID != "" {
		additionalProperties["provision_shard_id"] = csc.provisionShardID
	}
	if csc.provisionerNoOpProvision {
		additionalProperties["provisioner_noop_provision"] = "true"
	}
	if csc.provisionerNoOpDeprovision {
		additionalProperties["provisioner_noop_deprovision"] = "true"
	}
	return builder.Properties(additionalProperties)
}

func (csc *clusterServiceClient) GetCluster(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.Cluster, error) {
	client, ok := internalID.GetAroHCPClusterClient(csc.conn)
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

func (csc *clusterServiceClient) GetClusterStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ClusterStatus, error) {
	client, ok := internalID.GetAroHCPClusterClient(csc.conn)
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

func (csc *clusterServiceClient) GetClusterInflightChecks(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.InflightCheckList, error) {
	client, ok := internalID.GetAroHCPClusterClient(csc.conn)
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

func (csc *clusterServiceClient) PostCluster(ctx context.Context, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error) {
	clustersAddResponse, err := csc.conn.AroHCP().V1alpha1().Clusters().Add().Body(cluster).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	cluster, ok := clustersAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return cluster, nil
}

func (csc *clusterServiceClient) UpdateCluster(ctx context.Context, internalID InternalID, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error) {
	client, ok := internalID.GetAroHCPClusterClient(csc.conn)
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

func (csc *clusterServiceClient) DeleteCluster(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetAroHCPClusterClient(csc.conn)
	if !ok {
		return fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

func (csc *clusterServiceClient) ListClusters(searchExpression string) ClusterListIterator {
	clustersListRequest := csc.conn.AroHCP().V1alpha1().Clusters().List()
	if searchExpression != "" {
		clustersListRequest.Search(searchExpression)
	}
	return ClusterListIterator{request: clustersListRequest}
}

func (csc *clusterServiceClient) GetNodePool(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePool, error) {
	client, ok := internalID.GetNodePoolClient(csc.conn)
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

	// NodePoolGetResponse returns a NodePool with a VersionLink instead
	// of a Version. Clients are responsible for dereferencing links, so
	// we will do that now and rebuild the NodePool with a full Version.
	if nodePool.Version().Link() {
		versionClient := arohcpv1alpha1.NewVersionClient(csc.conn, nodePool.Version().HREF())

		versionGetResponse, err := versionClient.Get().SendContext(ctx)
		if err != nil {
			return nil, err
		}
		version, ok := versionGetResponse.GetBody()
		if !ok {
			return nil, fmt.Errorf("empty version response body")
		}

		versionBuilder := arohcpv1alpha1.NewVersion().Copy(version)

		nodePool, err = arohcpv1alpha1.NewNodePool().Copy(nodePool).Version(versionBuilder).Build()
		if err != nil {
			return nil, err
		}
	}

	return nodePool, nil
}

func (csc *clusterServiceClient) GetNodePoolStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePoolStatus, error) {
	client, ok := internalID.GetNodePoolClient(csc.conn)
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

func (csc *clusterServiceClient) PostNodePool(ctx context.Context, clusterInternalID InternalID, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error) {
	client, ok := clusterInternalID.GetAroHCPClusterClient(csc.conn)
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

func (csc *clusterServiceClient) UpdateNodePool(ctx context.Context, internalID InternalID, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error) {
	client, ok := internalID.GetNodePoolClient(csc.conn)
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

func (csc *clusterServiceClient) DeleteNodePool(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetNodePoolClient(csc.conn)
	if !ok {
		return fmt.Errorf("OCM path is not an external: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

func (csc *clusterServiceClient) GetExternalAuth(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ExternalAuth, error) {
	client, ok := internalID.GetExternalAuthClient(csc.conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not an external auth: %s", internalID)
	}
	externalAuthGetResponse, err := client.Get().SendContext(ctx)
	if err != nil {
		return nil, err
	}
	externalAuth, ok := externalAuthGetResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return externalAuth, nil
}

func (csc *clusterServiceClient) PostExternalAuth(ctx context.Context, clusterInternalID InternalID, externalAuth *arohcpv1alpha1.ExternalAuth) (*arohcpv1alpha1.ExternalAuth, error) {
	// client, ok := clusterInternalID.GetExternalAuthClient(csc.conn)
	_, ok := clusterInternalID.GetExternalAuthClient(csc.conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not an external auth: %s", clusterInternalID)
	}
	// nodePoolsAddResponse, err := client .Body(externalAuth).SendContext(ctx)
	// if err != nil {
	// 	return nil, err
	// }
	// externalAuth, ok = nodePoolsAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return externalAuth, nil

}

func (csc *clusterServiceClient) UpdateExternalAuth(ctx context.Context, internalID InternalID, externalAuth *arohcpv1alpha1.ExternalAuth) (*arohcpv1alpha1.ExternalAuth, error) {
	client, ok := internalID.GetExternalAuthClient(csc.conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not an external auth: %s", internalID)
	}
	nodePoolUpdateResponse, err := client.Update().Body(externalAuth).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	externalAuth, ok = nodePoolUpdateResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return externalAuth, nil
}

func (csc *clusterServiceClient) DeleteExternalAuth(ctx context.Context, internalID InternalID) error {
	client, ok := internalID.GetExternalAuthClient(csc.conn)
	if !ok {
		return fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

func (csc *clusterServiceClient) ListNodePools(clusterInternalID InternalID, searchExpression string) NodePoolListIterator {
	client, ok := clusterInternalID.GetAroHCPClusterClient(csc.conn)
	if !ok {
		return NodePoolListIterator{err: fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)}
	}
	nodePoolsListRequest := client.NodePools().List()
	if searchExpression != "" {
		nodePoolsListRequest.Search(searchExpression)
	}
	return NodePoolListIterator{request: nodePoolsListRequest}
}

func (csc *clusterServiceClient) GetBreakGlassCredential(ctx context.Context, internalID InternalID) (*cmv1.BreakGlassCredential, error) {
	client, ok := internalID.GetBreakGlassCredentialClient(csc.conn)
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

func (csc *clusterServiceClient) PostBreakGlassCredential(ctx context.Context, clusterInternalID InternalID) (*cmv1.BreakGlassCredential, error) {
	client, ok := clusterInternalID.GetClusterClient(csc.conn)
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

func (csc *clusterServiceClient) DeleteBreakGlassCredentials(ctx context.Context, clusterInternalID InternalID) error {
	client, ok := clusterInternalID.GetClusterClient(csc.conn)
	if !ok {
		return fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)
	}
	_, err := client.BreakGlassCredentials().Delete().SendContext(ctx)
	return err
}

func (csc *clusterServiceClient) ListBreakGlassCredentials(clusterInternalID InternalID, searchExpression string) BreakGlassCredentialListIterator {
	client, ok := clusterInternalID.GetClusterClient(csc.conn)
	if !ok {
		return BreakGlassCredentialListIterator{err: fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)}
	}
	breakGlassCredentialsListRequest := client.BreakGlassCredentials().List()
	if searchExpression != "" {
		breakGlassCredentialsListRequest.Search(searchExpression)
	}
	return BreakGlassCredentialListIterator{request: breakGlassCredentialsListRequest}
}

func (csc *clusterServiceClient) GetVersion(ctx context.Context, versionName string) (*arohcpv1alpha1.Version, error) {

	if !strings.HasPrefix(versionName, api.OpenShiftVersionPrefix) {
		versionName = api.OpenShiftVersionPrefix + versionName
	}
	client := csc.conn.AroHCP().V1alpha1().Versions().Version(versionName)

	resp, err := client.Get().SendContext(ctx)
	if err != nil {
		return nil, err
	}
	version, ok := resp.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return version, nil
}

func (csc *clusterServiceClient) ListVersions() VersionsListIterator {
	versionsListRequest := csc.conn.AroHCP().V1alpha1().Versions().List()
	return VersionsListIterator{request: versionsListRequest}
}

// NewOpenShiftVersionXY parses the given version, stripping off any
// OpenShift prefix ("openshift-"), and returns a new Version X.Y.
func NewOpenShiftVersionXY(v string) string {
	v = ConvertOpenshiftVersionNoPrefix(v)
	parts := strings.Split(v, ".")
	if len(parts) >= 2 {
		v = parts[0] + "." + parts[1]
	}
	return v
}

// NewOpenShiftVersionXYZ parses the given version and converts it to CS readable version
func NewOpenShiftVersionXYZ(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) == 1 {
		parts = append(parts, "0")
	}
	parts = append(parts[:2], "0")
	return api.OpenShiftVersionPrefix + strings.Join(parts, ".")
}

// ConvertOpenshiftVersionNoPrefix strips off openshift-v prefix
func ConvertOpenshiftVersionNoPrefix(v string) string {
	return strings.Replace(v, api.OpenShiftVersionPrefix, "", 1)
}

// ConvertOpenshiftVersionAddPrefix adds openshift-v prefix
func ConvertOpenshiftVersionAddPrefix(v string) string {
	if !strings.HasPrefix(v, api.OpenShiftVersionPrefix) {
		return api.OpenShiftVersionPrefix + v
	}
	return v
}
