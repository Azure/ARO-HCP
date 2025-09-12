package database

import "github.com/Azure/ARO-HCP/internal/api"

type HCPClusterCRUD interface {
	TopLevelResourceCRUD[HCPCluster]

	ExternalAuthCRUD(subscriptionID, resourceGroupID, hcpClusterID string) NestedResourceCRUD[ExternalAuth]
	NodePoolCRUD(subscriptionID, resourceGroupID, hcpClusterID string) NestedResourceCRUD[NodePool]
}

type hcpClusterCRUD struct {
	*topLevelCosmosResourceCRUD[HCPCluster]
}

var _ HCPClusterCRUD = &hcpClusterCRUD{}

func (h *hcpClusterCRUD) ExternalAuthCRUD(subscriptionID, resourceGroupID, hcpClusterID string) NestedResourceCRUD[ExternalAuth] {
	return newNestedCosmosResourceCRUD[ExternalAuth](h.topLevelCosmosResourceCRUD, subscriptionID, resourceGroupID, hcpClusterID, &api.ExternalAuthResourceType)
}

func (h *hcpClusterCRUD) NodePoolCRUD(subscriptionID, resourceGroupID, hcpClusterID string) NestedResourceCRUD[NodePool] {
	return newNestedCosmosResourceCRUD[NodePool](h.topLevelCosmosResourceCRUD, subscriptionID, resourceGroupID, hcpClusterID, &api.NodePoolResourceType)
}
