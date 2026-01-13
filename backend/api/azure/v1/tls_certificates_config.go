package v1

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

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

func (tlsConfig TLSCertificatesConfig) Validate(fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, tlsConfig.validateCertificatesGenerationSource(fldPath.Child("certificatesGenerationSource"))...)

	errs = append(errs, tlsConfig.validateIssuer(fldPath.Child("issuer"), fldPath.Child("certificatesGenerationSource"))...)

	return errs
}

func (tlsConfig TLSCertificatesConfig) validateCertificatesGenerationSource(fldPath *field.Path) field.ErrorList {
	acceptedCertificatesGenerationSourceValues := []CertificatesGenerationSource{
		CertificatesGenerationSourceAzureKeyVault,
		CertificatesGenerationSourceHypershift,
	}

	acceptedCertificatesGenerationSourceValuesStrings := []string{
		string(CertificatesGenerationSourceAzureKeyVault),
		string(CertificatesGenerationSourceHypershift),
	}

	if tlsConfig.CertificatesGenerationSource == "" {
		return field.ErrorList{field.Required(fldPath, "attribute is required")}
	}

	if !slices.Contains(acceptedCertificatesGenerationSourceValues, tlsConfig.CertificatesGenerationSource) {
		return field.ErrorList{
			field.Invalid(fldPath, tlsConfig.CertificatesGenerationSource,
				fmt.Sprintf("attribute is not supported. Accepted values are: %s",
					strings.Join(acceptedCertificatesGenerationSourceValuesStrings, ","),
				),
			),
		}
	}

	return nil
}

func (tlsConfig TLSCertificatesConfig) validateIssuer(
	fldPath *field.Path, certSourceFldPath *field.Path,
) field.ErrorList {
	acceptedIssuerValues := []TLSCertificateIssuerType{
		TLSCertificateIssuerSelf,
		TLSCertificateIssuerOneCert,
	}

	acceptedIssuerValuesStrings := []string{
		string(TLSCertificateIssuerSelf),
		string(TLSCertificateIssuerOneCert),
	}

	if tlsConfig.Issuer == "" {
		return field.ErrorList{field.Required(fldPath, "attribute is required")}
	}

	if !slices.Contains(acceptedIssuerValues, tlsConfig.Issuer) {
		return field.ErrorList{
			field.Invalid(fldPath, tlsConfig.Issuer,
				fmt.Sprintf("attribute is not supported. Accepted values are: %s",
					strings.Join(acceptedIssuerValuesStrings, ","),
				),
			),
		}
	}

	if tlsConfig.CertificatesGenerationSource == CertificatesGenerationSourceHypershift && tlsConfig.Issuer != "" {
		return field.ErrorList{field.Invalid(fldPath, tlsConfig.Issuer,
			fmt.Sprintf("attribute is not allowed when %s is %s",
				certSourceFldPath,
				CertificatesGenerationSourceHypershift,
			),
		)}
	}

	return nil
}
