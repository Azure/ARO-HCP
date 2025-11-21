package inventory

import (
	"context"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/aks"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

type GraphMgmtClusterInventoryBackend struct {
	region    string
	discovery *aks.AKSDiscovery
}

func NewGraphMgmtClusterInventoryBackend(region string, credential azcore.TokenCredential) *GraphMgmtClusterInventoryBackend {
	return &GraphMgmtClusterInventoryBackend{
		region:    region,
		discovery: aks.NewAKSDiscovery(credential),
	}
}

func (c *GraphMgmtClusterInventoryBackend) GetClusters(ctx context.Context) ([]aks.AKSCluster, error) {
	filter := aks.NewMgmtClusterFilter(c.region, "")
	clusters, err := c.discovery.DiscoverClusters(ctx, filter)
	if err != nil {
		return nil, err
	}
	return clusters, nil
}
