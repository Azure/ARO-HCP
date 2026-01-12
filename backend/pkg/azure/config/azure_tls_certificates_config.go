package config

// TLSCertificatesConfig holds the configuration used to generate tls
// certificates for user-facing apis, such as kube-apiserver and ingress.
type TLSCertificatesConfig struct {
	issuer                       TLSCertificateIssuerType
	certificatesGenerationSource CertificatesGenerationSource
}

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

func NewTLSCertificatesConfig(issuer TLSCertificateIssuerType, source CertificatesGenerationSource) TLSCertificatesConfig {
	return TLSCertificatesConfig{
		issuer:                       issuer,
		certificatesGenerationSource: source,
	}
}

func (t TLSCertificatesConfig) Issuer() TLSCertificateIssuerType {
	return t.issuer
}

func (t TLSCertificatesConfig) CertificatesGenerationSource() CertificatesGenerationSource {
	return t.certificatesGenerationSource
}
