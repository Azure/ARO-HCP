package adminapi

import (
	"github.com/Azure/ARO-HCP/internal/api"
)

type version struct{}

// String returns the api-version parameter value for this API.
func (v version) String() string {
	return "admin"
}

var (
	validate            = api.NewValidator()
	clusterStructTagMap = api.NewStructTagMap[api.HCPOpenShiftCluster]()
)

func (v version) NewHCPOpenShiftCluster(cluster *api.HCPOpenShiftCluster) api.VersionedHCPOpenShiftCluster {
	return NewDefaultHCPOpenShiftCluster()
}

func (v version) NewHCPOpenShiftClusterNodePool(cluster *api.HCPOpenShiftClusterNodePool) api.VersionedHCPOpenShiftClusterNodePool {
	return NewDefaultHCPOpenShiftClusterNodepool()
}

func init() {

	api.Register(version{})
}
