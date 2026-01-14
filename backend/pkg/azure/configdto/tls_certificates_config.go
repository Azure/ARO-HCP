package configdto

import "fmt"

// TLSCertificatesConfig holds the configuration used to generate tls
// certificates for user-facing apis, such as kube-apiserver and ingress.
type TLSCertificatesConfig struct {
	// Issuer holds the issuer used to generated the TLS certificates in
	// Azure Key vault. When Enabled is true, Issuer is required.
	Issuer TLSCertificateIssuerType `json:"issuer"`
	// Enabled indicates whether to generate TLS certificates in Azure Key
	// Vault or not. When Enabled is false, the default Hypershift generated
	// TLS Certificates are used.
	Enabled bool `json:"enabled"`
}

type TLSCertificateIssuerType string

const (
	// TLSCertificateIssuerSelf generates tls certificates with a self signed issuer
	TLSCertificateIssuerSelf TLSCertificateIssuerType = "Self"
	// TLSCertificateIssuerOneCert generates tls certificates with a self signed issuer
	TLSCertificateIssuerOneCert TLSCertificateIssuerType = "OneCertV2-PublicCA"
)

func (tlsConfig TLSCertificatesConfig) Validate() error {
	if !tlsConfig.Enabled {
		return nil
	}

	if tlsConfig.Issuer != TLSCertificateIssuerSelf &&
		tlsConfig.Issuer != TLSCertificateIssuerOneCert {
		return fmt.Errorf(
			"'tls_certificates_config.issuer' is invalid, valid values are: %q or %q",
			TLSCertificateIssuerSelf, TLSCertificateIssuerOneCert,
		)
	}
	return nil
}
