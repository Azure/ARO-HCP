package certs

import "crypto/x509/pkix"

const (
	// CN must start with "system:sre-break-glass:" to pass the HyperShift
	// CSR signer validation. The RBAC on the HCP cluster binds to this
	// user and group identity.
	DiagnosticsCommonName   = "system:sre-break-glass:aro-diagnostics"
	DiagnosticsOrganization = "system:aro-diagnostics"
)

func BuildDiagnosticsSubject() pkix.Name {
	return pkix.Name{
		CommonName:   DiagnosticsCommonName,
		Organization: []string{DiagnosticsOrganization},
	}
}
