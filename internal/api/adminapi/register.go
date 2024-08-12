package adminapi

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

type AdminVersion struct {
	adminVersion api.Version
}

// String returns the api-version parameter value for this API.
func (v AdminVersion) String() string {
	return "admin"
}

func (v AdminVersion) NewHCPOpenShiftCluster(cluster *api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	return v.adminVersion.NewHCPOpenShiftCluster(cluster)
}

func (v AdminVersion) NewHCPOpenShiftClusterNodePool(nodePool *api.HCPOpenShiftClusterNodePool) api.VersionedHCPOpenShiftClusterNodePool {
	return v.adminVersion.NewHCPOpenShiftClusterNodePool(nodePool)
}

func init() {
	api.Register(AdminVersion{})
}
