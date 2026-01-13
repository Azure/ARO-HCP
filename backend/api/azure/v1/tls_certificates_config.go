package v1

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/sets"
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
	// TLSCertificateIssuerOneCert generates tls certificates with Microsoft's
	// OneCertV2-PublicCA issuer
	TLSCertificateIssuerOneCert TLSCertificateIssuerType = "OneCertV2-PublicCA"
)

var (
	// validIssuerTypes is a set of valid issuer types.
	validIssuerTypes = sets.New[TLSCertificateIssuerType](
		TLSCertificateIssuerSelf,
		TLSCertificateIssuerOneCert,
	)

	// validIssuerTypesStrings is a slice of strings representing the valid issuer types.
	// This is used to list the valid values in a a sorted way to be used in messages
	validIssuerTypesStrings = []string{
		string(TLSCertificateIssuerSelf),
		string(TLSCertificateIssuerOneCert),
	}
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
	CertificatesGenerationSourceHypershift CertificatesGenerationSource = "NotSupportedMayRemove_Hypershift"
)

var (
	// validCertificatesGenerationSources is a set of valid certificates generation sources.
	validCertificatesGenerationSources = sets.New[CertificatesGenerationSource](
		CertificatesGenerationSourceAzureKeyVault,
		CertificatesGenerationSourceHypershift,
	)

	// validCertificatesGenerationSourcesStrings is a slice of strings representing the valid certificates generation sources.
	// This is used to list the valid values in a a sorted way to be used in messages
	validCertificatesGenerationSourcesStrings = []string{
		string(CertificatesGenerationSourceAzureKeyVault),
		string(CertificatesGenerationSourceHypershift),
	}
)

func (tlsConfig TLSCertificatesConfig) Validate(fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, tlsConfig.validateCertificatesGenerationSource(fldPath.Child("certificatesGenerationSource"))...)

	errs = append(errs, tlsConfig.validateIssuer(fldPath.Child("issuer"), fldPath.Child("certificatesGenerationSource"))...)

	return errs
}

func (tlsConfig TLSCertificatesConfig) validateCertificatesGenerationSource(fldPath *field.Path) field.ErrorList {
	if len(tlsConfig.CertificatesGenerationSource) == 0 {
		return field.ErrorList{
			field.Required(
				fldPath,
				fmt.Sprintf("attribute is required. Accepted values are: %s",
					strings.Join(validCertificatesGenerationSourcesStrings, ","),
				),
			),
		}
	}

	return validate.Enum(context.Background(), operation.Operation{}, fldPath,
		&tlsConfig.CertificatesGenerationSource, nil, validCertificatesGenerationSources,
	)
}

func (tlsConfig TLSCertificatesConfig) validateIssuer(
	fldPath *field.Path, certSourceFldPath *field.Path,
) field.ErrorList {
	if len(tlsConfig.Issuer) == 0 {
		return field.ErrorList{field.Required(fldPath, "attribute is required")}
	}

	validationErrrors := validate.Enum(context.Background(), operation.Operation{}, fldPath,
		&tlsConfig.Issuer, nil, validIssuerTypes,
	)
	if len(validationErrrors) != 0 {
		return validationErrrors
	}

	if tlsConfig.CertificatesGenerationSource == CertificatesGenerationSourceHypershift && len(tlsConfig.Issuer) != 0 {
		return field.ErrorList{
			field.Forbidden(fldPath,
				fmt.Sprintf("attribute is not allowed when %s is %s",
					certSourceFldPath,
					CertificatesGenerationSourceHypershift,
				),
			),
		}
	}

	if tlsConfig.CertificatesGenerationSource == CertificatesGenerationSourceAzureKeyVault && len(tlsConfig.Issuer) == 0 {
		return field.ErrorList{
			field.Required(fldPath,
				fmt.Sprintf("attribute is required when %s is %s",
					certSourceFldPath,
					CertificatesGenerationSourceAzureKeyVault,
				),
			),
		}
	}

	return nil
}
