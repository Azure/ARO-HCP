package api


import "time"

// HCPOpenShiftClusterAdminCredential represents a temporary admin
// credential for an ARO HCP OpenShift cluster.
type HCPOpenShiftClusterAdminCredential struct {
	ExpirationTimestamp time.Time `json:"expirationTimestamp"`
	Kubeconfig          string    `json:"kubeconfig"`
}
