package v20240610preview


import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

func newHCPOpenShiftClusterAdminCredential(from *api.HCPOpenShiftClusterAdminCredential) *generated.HcpOpenShiftClusterAdminCredential {
	return &generated.HcpOpenShiftClusterAdminCredential{
		ExpirationTimestamp: api.Ptr(from.ExpirationTimestamp),
		Kubeconfig:          api.Ptr(from.Kubeconfig),
	}
}

func (v version) MarshalHCPOpenShiftClusterAdminCredential(from *api.HCPOpenShiftClusterAdminCredential) ([]byte, error) {
	return arm.MarshalJSON(newHCPOpenShiftClusterAdminCredential(from))
}
