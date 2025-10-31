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

// The patch version is managed by Red Hat.
const OpenShift419Patch = "7"

type ClusterServiceClientSpec interface {
	// GetCluster sends a GET request to fetch a cluster from Cluster Service.
	GetCluster(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.Cluster, error)

	// GetClusterStatus sends a GET request to fetch a cluster's status from Cluster Service.
	GetClusterStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ClusterStatus, error)

	// GetClusterInflightChecks sends a GET request to fetch a cluster's inflight checks from Cluster Service.
	GetClusterInflightChecks(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.InflightCheckList, error)

	// PostCluster sends a POST request to create a cluster in Cluster Service.
	PostCluster(ctx context.Context, clusterBuilder *arohcpv1alpha1.ClusterBuilder, autoscalerBuilder *arohcpv1alpha1.ClusterAutoscalerBuilder) (*arohcpv1alpha1.Cluster, error)

	// UpdateCluster sends a PATCH request to update a cluster in Cluster Service.
	UpdateCluster(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error)

	// UpdateClusterAutoscaler sends a PATCH request to update cluster autoscaling values in Cluster Service.
	UpdateClusterAutoscaler(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ClusterAutoscalerBuilder) (*arohcpv1alpha1.ClusterAutoscaler, error)

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
	PostNodePool(ctx context.Context, clusterInternalID InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error)

	// UpdateNodePool sends a PATCH request to update a node pool in Cluster Service.
	UpdateNodePool(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error)

	// DeleteNodePool sends a DELETE request to delete a node pool from Cluster Service.
	DeleteNodePool(ctx context.Context, internalID InternalID) error

	// ListNodePools prepares a GET request with the given search expression. Call Items() on
	// the returned iterator in a for/range loop to execute the request and paginate over results,
	// then call GetError() to check for an iteration error.
	ListNodePools(clusterInternalID InternalID, searchExpression string) NodePoolListIterator

	// GetExternalAuth sends a GET request to fetch an external auth config from Cluster Service.
	GetExternalAuth(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ExternalAuth, error)

	// PostExternalAuth sends a POST request to create an external auth config in Cluster Service.
	PostExternalAuth(ctx context.Context, clusterInternalID InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error)

	// UpdateExternalAuth sends a PATCH request to update an external auth config in Cluster Service.
	UpdateExternalAuth(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error)

	// DeleteExternalAuth sends a DELETE request to delete an external auth config from Cluster Service.
	DeleteExternalAuth(ctx context.Context, internalID InternalID) error

	// ListExternalAuths prepares a GET request with the given search expression. Call Items() on
	// the returned iterator in a for/range loop to execute the request and paginate over results,
	// then call GetError() to check for an iteration error.
	ListExternalAuths(clusterInternalID InternalID, searchExpression string) ExternalAuthListIterator

	// GetBreakGlassCredential sends a GET request to fetch a break-glass cluster credential from Cluster Service.
	GetBreakGlassCredential(ctx context.Context, internalID InternalID) (*cmv1.BreakGlassCredential, error)

	// PostBreakGlassCredential sends a POST request to create a break-glass cluster credential in Cluster Service.
	PostBreakGlassCredential(ctx context.Context, clusterInternalID InternalID) (*cmv1.BreakGlassCredential, error)

	// DeleteBreakGlassCredentials sends a DELETE request to revoke all break-glass credentials for a cluster in Cluster Service.
	DeleteBreakGlassCredentials(ctx context.Context, clusterInternalID InternalID) error

	// ListBreakGlassCredentials prepares a GET request with the given search expression. Call
	// Items() on the returned iterator in a for/range loop to execute the request and paginate
	// over results, then call GetError() to check for an iteration error.
	ListBreakGlassCredentials(clusterInternalID InternalID, searchExpression string) *BreakGlassCredentialListIterator

	// GetVersion sends a GET request to fetch cluster version
	GetVersion(ctx context.Context, versionName string) (*arohcpv1alpha1.Version, error)

	// ListVersions prepares a GET request. Call Items() on
	// the returned iterator in a for/range loop to execute the request and paginate over results,
	// then call GetError() to check for an iteration error.
	ListVersions() *VersionsListIterator
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

func (csc *clusterServiceClient) addProperties(builder *arohcpv1alpha1.ClusterBuilder) *arohcpv1alpha1.ClusterBuilder {
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

// resolveClusterLinks replaces link objects with full objects that are
// necessary to fully construct an HCPOpenShiftCluster model.
func resolveClusterLinks(ctx context.Context, conn *sdk.Connection, cluster *arohcpv1alpha1.Cluster) (*arohcpv1alpha1.Cluster, error) {
	builder := arohcpv1alpha1.NewCluster().Copy(cluster)

	autoscaler, ok := cluster.GetAutoscaler()
	if ok && autoscaler.Link() {
		autoscalerClient := arohcpv1alpha1.NewAutoscalerClient(conn, autoscaler.HREF())

		autoscalerGetResponse, err := autoscalerClient.Get().SendContext(ctx)
		if err != nil {
			return nil, err
		}
		autoscaler, ok = autoscalerGetResponse.GetBody()
		if !ok {
			return nil, fmt.Errorf("empty autoscaler response body")
		}

		builder.Autoscaler(arohcpv1alpha1.NewClusterAutoscaler().Copy(autoscaler))
	}

	return builder.Build()
}

// resolveNodePoolLinks replaces link objects with full objects that are
// necessary to fully construct an HCPOpenShiftClusterNodePool model.
func resolveNodePoolLinks(ctx context.Context, conn *sdk.Connection, nodePool *arohcpv1alpha1.NodePool) (*arohcpv1alpha1.NodePool, error) {
	builder := arohcpv1alpha1.NewNodePool().Copy(nodePool)

	version, ok := nodePool.GetVersion()
	if ok && version.Link() {
		versionClient := arohcpv1alpha1.NewVersionClient(conn, version.HREF())

		versionGetResponse, err := versionClient.Get().SendContext(ctx)
		if err != nil {
			return nil, err
		}
		version, ok = versionGetResponse.GetBody()
		if !ok {
			return nil, fmt.Errorf("empty version response body")
		}

		builder.Version(arohcpv1alpha1.NewVersion().Copy(version))
	}

	return builder.Build()
}

func (csc *clusterServiceClient) GetCluster(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.Cluster, error) {
	client, ok := getAroHCPClusterClient(internalID, csc.conn)
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
	return resolveClusterLinks(ctx, csc.conn, cluster)
}

func (csc *clusterServiceClient) GetClusterStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ClusterStatus, error) {
	client, ok := getAroHCPClusterClient(internalID, csc.conn)
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
	client, ok := getAroHCPClusterClient(internalID, csc.conn)
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

func (csc *clusterServiceClient) PostCluster(ctx context.Context, clusterBuilder *arohcpv1alpha1.ClusterBuilder, autoscalerBuilder *arohcpv1alpha1.ClusterAutoscalerBuilder) (*arohcpv1alpha1.Cluster, error) {
	if autoscalerBuilder != nil {
		clusterBuilder.Autoscaler(autoscalerBuilder)
	}
	cluster, err := csc.addProperties(clusterBuilder).Build()
	if err != nil {
		return nil, err
	}
	clustersAddResponse, err := csc.conn.AroHCP().V1alpha1().Clusters().Add().Body(cluster).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	cluster, ok := clustersAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return resolveClusterLinks(ctx, csc.conn, cluster)
}

func (csc *clusterServiceClient) UpdateCluster(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
	cluster, err := csc.addProperties(builder).Build()
	if err != nil {
		return nil, err
	}
	client, ok := getAroHCPClusterClient(internalID, csc.conn)
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
	return resolveClusterLinks(ctx, csc.conn, cluster)
}

func (csc *clusterServiceClient) UpdateClusterAutoscaler(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ClusterAutoscalerBuilder) (*arohcpv1alpha1.ClusterAutoscaler, error) {
	autoscaler, err := builder.Build()
	if err != nil {
		return nil, err
	}
	client, ok := getAroHCPClusterClient(internalID, csc.conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", internalID)
	}
	autoscalerUpdateResponse, err := client.Autoscaler().Update().Body(autoscaler).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	autoscaler, ok = autoscalerUpdateResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return autoscaler, nil
}

func (csc *clusterServiceClient) DeleteCluster(ctx context.Context, internalID InternalID) error {
	client, ok := getAroHCPClusterClient(internalID, csc.conn)
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
	return &clusterListIterator{conn: csc.conn, request: clustersListRequest}
}

func (csc *clusterServiceClient) GetNodePool(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePool, error) {
	client, ok := GetNodePoolClient(internalID, csc.conn)
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

	return resolveNodePoolLinks(ctx, csc.conn, nodePool)
}

func (csc *clusterServiceClient) GetNodePoolStatus(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.NodePoolStatus, error) {
	client, ok := GetNodePoolClient(internalID, csc.conn)
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

func (csc *clusterServiceClient) PostNodePool(ctx context.Context, clusterInternalID InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
	client, ok := getAroHCPClusterClient(clusterInternalID, csc.conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)
	}
	nodePool, err := builder.Build()
	if err != nil {
		return nil, err
	}
	nodePoolsAddResponse, err := client.NodePools().Add().Body(nodePool).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	nodePool, ok = nodePoolsAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return resolveNodePoolLinks(ctx, csc.conn, nodePool)
}

func (csc *clusterServiceClient) UpdateNodePool(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
	client, ok := GetNodePoolClient(internalID, csc.conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	nodePool, err := builder.Build()
	if err != nil {
		return nil, err
	}
	nodePoolUpdateResponse, err := client.Update().Body(nodePool).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	nodePool, ok = nodePoolUpdateResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return resolveNodePoolLinks(ctx, csc.conn, nodePool)
}

func (csc *clusterServiceClient) DeleteNodePool(ctx context.Context, internalID InternalID) error {
	client, ok := GetNodePoolClient(internalID, csc.conn)
	if !ok {
		return fmt.Errorf("OCM path is not a node pool: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

func (csc *clusterServiceClient) ListNodePools(clusterInternalID InternalID, searchExpression string) NodePoolListIterator {
	client, ok := getAroHCPClusterClient(clusterInternalID, csc.conn)
	if !ok {
		return &nodePoolListIterator{err: fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)}
	}
	nodePoolsListRequest := client.NodePools().List()
	if searchExpression != "" {
		nodePoolsListRequest.Search(searchExpression)
	}
	return &nodePoolListIterator{conn: csc.conn, request: nodePoolsListRequest}
}

func (csc *clusterServiceClient) GetExternalAuth(ctx context.Context, internalID InternalID) (*arohcpv1alpha1.ExternalAuth, error) {
	client, ok := GetExternalAuthClient(internalID, csc.conn)
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

func (csc *clusterServiceClient) PostExternalAuth(ctx context.Context, clusterInternalID InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
	client, ok := getAroHCPClusterClient(clusterInternalID, csc.conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)
	}
	externalAuth, err := builder.Build()
	if err != nil {
		return nil, err
	}
	externalAuthsAddResponse, err := client.ExternalAuthConfig().ExternalAuths().Add().Body(externalAuth).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	externalAuth, ok = externalAuthsAddResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return externalAuth, nil

}

func (csc *clusterServiceClient) UpdateExternalAuth(ctx context.Context, internalID InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
	client, ok := GetExternalAuthClient(internalID, csc.conn)
	if !ok {
		return nil, fmt.Errorf("OCM path is not an external auth: %s", internalID)
	}
	externalAuth, err := builder.Build()
	if err != nil {
		return nil, err
	}
	externalAuthUpdateResponse, err := client.Update().Body(externalAuth).SendContext(ctx)
	if err != nil {
		return nil, err
	}
	externalAuth, ok = externalAuthUpdateResponse.GetBody()
	if !ok {
		return nil, fmt.Errorf("empty response body")
	}
	return externalAuth, nil
}

func (csc *clusterServiceClient) DeleteExternalAuth(ctx context.Context, internalID InternalID) error {
	client, ok := GetExternalAuthClient(internalID, csc.conn)
	if !ok {
		return fmt.Errorf("OCM path is not a external auth: %s", internalID)
	}
	_, err := client.Delete().SendContext(ctx)
	return err
}

func (csc *clusterServiceClient) ListExternalAuths(clusterInternalID InternalID, searchExpression string) ExternalAuthListIterator {
	client, ok := getAroHCPClusterClient(clusterInternalID, csc.conn)
	if !ok {
		return &externalAuthListIterator{err: fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)}
	}
	externalAuthsListRequest := client.ExternalAuthConfig().ExternalAuths().List()
	// FIXME ExternalAuthsListRequest is missing a Search method.
	//       Presently it doesn't matter since we only support one
	//       ExternalAuth instance per cluster. Search will become
	//       more important if we ever support multiple.
	//if searchExpression != "" {
	//	externalAuthsListRequest.Search(searchExpression)
	//}
	return &externalAuthListIterator{request: externalAuthsListRequest}
}

func (csc *clusterServiceClient) GetBreakGlassCredential(ctx context.Context, internalID InternalID) (*cmv1.BreakGlassCredential, error) {
	client, ok := GetBreakGlassCredentialClient(internalID, csc.conn)
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
	client, ok := getClusterClient(clusterInternalID, csc.conn)
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
	client, ok := getClusterClient(clusterInternalID, csc.conn)
	if !ok {
		return fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)
	}
	_, err := client.BreakGlassCredentials().Delete().SendContext(ctx)
	return err
}

func (csc *clusterServiceClient) ListBreakGlassCredentials(clusterInternalID InternalID, searchExpression string) *BreakGlassCredentialListIterator {
	client, ok := getClusterClient(clusterInternalID, csc.conn)
	if !ok {
		return &BreakGlassCredentialListIterator{err: fmt.Errorf("OCM path is not a cluster: %s", clusterInternalID)}
	}
	breakGlassCredentialsListRequest := client.BreakGlassCredentials().List()
	if searchExpression != "" {
		breakGlassCredentialsListRequest.Search(searchExpression)
	}
	return &BreakGlassCredentialListIterator{request: breakGlassCredentialsListRequest}
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

func (csc *clusterServiceClient) ListVersions() *VersionsListIterator {
	versionsListRequest := csc.conn.AroHCP().V1alpha1().Versions().List()
	return &VersionsListIterator{request: versionsListRequest}
}

// NewOpenShiftVersionXY parses the given version, stripping off any
// OpenShift prefix ("openshift-"), and suffix ("-<channel_group>") and returns a new Version X.Y.
func NewOpenShiftVersionXY(v string) string {
	v = ConvertOpenShiftVersionNoPrefix(v)
	parts := strings.Split(v, ".")
	if len(parts) >= 2 {
		v = parts[0] + "." + parts[1]
	}
	return v
}

// NewOpenShiftVersionXYZ parses the given version and converts it to CS readable version
// CS readable version "openshift-v<X.Y.Z>" or "openshift-v<X.Y.Z>-channel_group"
func NewOpenShiftVersionXYZ(v, cg string) string {
	var csVersion string

	if len(v) > 0 {
		// Separate version from prerelease (e.g., "4.19.0" from "0.nightly-2025-01-01")
		versionPart := v
		prereleasePart := ""
		if hyphenIndex := strings.Index(v, "-"); hyphenIndex != -1 {
			versionPart = v[:hyphenIndex]
			prereleasePart = v[hyphenIndex:] // includes the hyphen
		}

		parts := strings.Split(versionPart, ".")
		if len(parts) == 1 {
			parts = append(parts, "0")
		}

		// If no patch version provided (X.Y format), append default patch version
		// Otherwise preserve the provided patch version (X.Y.Z format)
		if len(parts) == 2 {
			parts = append(parts, OpenShift419Patch)
		}

		csVersion = api.OpenShiftVersionPrefix + strings.Join(parts, ".") + prereleasePart

		// Only append channel group if it's not empty and not "stable"
		// Versions will look as:
		// stable: opensfhit-vX.Y.Z
		// candidate: openshift-vX.Y.Z-candidate
		// or candidate: openshift-vX.Y.Z[-prerelease]-candidate
		// which can be <-rc.V> or <-ec.V>
		// nightly: openshift-vX.Y.Z[-prerelease]-candidate
		// [-prerelease] is in the format -0.nightly-<arch>-<timestamp>
		// i.e. `-0.nightly-multi-2025-11-07-08293`
		if len(cg) > 0 && cg != "stable" {
			csVersion = csVersion + "-" + cg
		}
	}

	return csVersion
}

// ConvertOpenShiftVersionNoPrefix strips off openshift-v prefix
func ConvertOpenShiftVersionNoPrefix(v string) string {
	return strings.Replace(v, api.OpenShiftVersionPrefix, "", 1)
}

// ConvertOpenShiftVersionAddPrefix adds openshift-v prefix
func ConvertOpenShiftVersionAddPrefix(v string) string {
	if len(v) > 0 && !strings.HasPrefix(v, api.OpenShiftVersionPrefix) {
		return api.OpenShiftVersionPrefix + v
	}
	return v
}
