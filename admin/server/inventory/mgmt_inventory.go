package inventory

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/aks"
)

type MgmtClusterInventoryBackend interface {
	GetClusters(ctx context.Context) ([]aks.AKSCluster, error)
}

type MgmtClusterInventory struct {
	backend MgmtClusterInventoryBackend
}

func NewMgmtClusterInventory(backend MgmtClusterInventoryBackend) *MgmtClusterInventory {
	return &MgmtClusterInventory{
		backend: backend,
	}
}

func (d *MgmtClusterInventory) GetClusters(ctx context.Context) ([]aks.AKSCluster, error) {
	return d.backend.GetClusters(ctx)
}

func (d *MgmtClusterInventory) GetForResourceID(ctx context.Context, resourceID string) (aks.AKSCluster, error) {
	clusters, err := d.backend.GetClusters(ctx)
	if err != nil {
		return aks.AKSCluster{}, fmt.Errorf("failed to get management clusters: %w", err)
	}

	if len(clusters) == 0 {
		return aks.AKSCluster{}, fmt.Errorf("no management clusters found")
	}

	for _, cluster := range clusters {
		if cluster.ResourceID == resourceID {
			return cluster, nil
		}
	}

	return aks.AKSCluster{}, fmt.Errorf("management cluster not found %s", resourceID)
}
