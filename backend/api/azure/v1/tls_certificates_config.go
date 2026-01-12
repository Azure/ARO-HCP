package v1

import "fmt"

// TLSCertificatesConfig holds the configuration used to generate tls
// certificates for user-facing apis, such as kube-apiserver and ingress.
type TLSCertificatesConfig struct {
	// Issuer holds the issuer used to generated the TLS certificates in
	// Azure Key vault. When CertificatesGenerationSource is AzureKeyVault,
	// Issuer is required. Only used when CertificatesGenerationSource is
	// AzureKeyVault.
	Issuer TLSCertificateIssuerType `json:"issuer"`
	// CertificatesGenerationSource indicates what is the source to be used to
	// generate the TLS certificates. Required.
	CertificatesGenerationSource CertificatesGenerationSource `json:"certificatesGenerationSource"`
}

// TLSCertificateIssuerType indicates the issuer used to generated the TLS
// certificates in Azure Key vault.
type TLSCertificateIssuerType string

const (
	// TLSCertificateIssuerSelf generates tls certificates with a self signed issuer
	TLSCertificateIssuerSelf TLSCertificateIssuerType = "Self"
	// TLSCertificateIssuerOneCert generates tls certificates with a self signed issuer
	TLSCertificateIssuerOneCert TLSCertificateIssuerType = "OneCertV2-PublicCA"
)

// CertificatesGenerationSource indicates what is the source to be used to
// generate the TLS certificates.
type CertificatesGenerationSource string

const (
	// CertificatesGenerationSourceAzureKeyVault signals TLS certificates to be
	// generated in Azure Key Vault.
	CertificatesGenerationSourceAzureKeyVault CertificatesGenerationSource = "AzureKeyVault"
	// CertificatesGenerationSourceHypershift signals TLS certificates to be
	// generated using the default Hypershift generated TLS Certificates.
	CertificatesGenerationSourceHypershift CertificatesGenerationSource = "Hypershift"
)

func (tlsConfig TLSCertificatesConfig) Validate() error {
	if tlsConfig.CertificatesGenerationSource != CertificatesGenerationSourceAzureKeyVault &&
		tlsConfig.CertificatesGenerationSource != CertificatesGenerationSourceHypershift {
		return fmt.Errorf(
			"'tls_certificates_config.certificatesGenerationSource' is invalid, valid values are: %q or %q",
			CertificatesGenerationSourceAzureKeyVault, CertificatesGenerationSourceHypershift,
		)
	}

	if tlsConfig.CertificatesGenerationSource == CertificatesGenerationSourceHypershift && tlsConfig.Issuer != "" {
		return fmt.Errorf(
			"'tls_certificates_config.issuer' is not allowed when 'tls_certificates_config.certificatesGenerationSource' is %q",
			CertificatesGenerationSourceHypershift,
		)
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
