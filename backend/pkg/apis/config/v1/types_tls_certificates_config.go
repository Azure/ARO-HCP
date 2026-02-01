// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1

import (
	"context"
	"fmt"

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
)

func (tlsConfig TLSCertificatesConfig) Validate(ctx context.Context, op operation.Operation, fldPath *field.Path) field.ErrorList {
	errs := field.ErrorList{}

	errs = append(errs, tlsConfig.validateCertificatesGenerationSource(ctx, op, fldPath.Child("certificatesGenerationSource"))...)

	errs = append(errs, tlsConfig.validateIssuer(ctx, op, fldPath.Child("issuer"), fldPath.Child("certificatesGenerationSource"))...)

	return errs
}

func (tlsConfig TLSCertificatesConfig) validateCertificatesGenerationSource(
	ctx context.Context, op operation.Operation, fldPath *field.Path,
) field.ErrorList {
	return validate.Enum(ctx, op, fldPath,
		&tlsConfig.CertificatesGenerationSource, nil, validCertificatesGenerationSources,
	)
}

func (tlsConfig TLSCertificatesConfig) validateIssuer(
	ctx context.Context, op operation.Operation, fldPath *field.Path, certSourceFldPath *field.Path,
) field.ErrorList {
	var errs field.ErrorList

	errs = append(errs, validate.Enum(ctx, op, fldPath, &tlsConfig.Issuer, nil, validIssuerTypes)...)

	if tlsConfig.CertificatesGenerationSource == CertificatesGenerationSourceHypershift && len(tlsConfig.Issuer) > 0 {
		errs = append(errs, field.Forbidden(fldPath,
			fmt.Sprintf("attribute is not allowed when %s is %s",
				certSourceFldPath,
				CertificatesGenerationSourceHypershift,
			),
		))
	}

	if tlsConfig.CertificatesGenerationSource == CertificatesGenerationSourceAzureKeyVault && len(tlsConfig.Issuer) == 0 {
		errs = append(errs, field.Required(fldPath,
			fmt.Sprintf("attribute is required when %s is %s",
				certSourceFldPath,
				CertificatesGenerationSourceAzureKeyVault,
			),
		))
	}

	return errs
}
