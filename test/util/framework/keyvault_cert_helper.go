// Copyright 2025 Microsoft Corporation
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

package framework

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

// KeyVaultCertResult describes a newly-issued (or newly-rotated) Key Vault
// certificate. SHA256 is the lowercase hex SHA-256 over the leaf DER bytes —
// what callers should compare against the contents of `tls.crt` after the
// cert syncs into the workload cluster.
type KeyVaultCertResult struct {
	VaultURL string
	Name     string
	Version  string
	SHA256   string
}

// CreateOrRotateSelfSignedKVCert ensures a self-signed certificate named
// certName exists in the given vault with a SAN of `*.<dnsName>` and a
// LifetimeAction set to AutoRenew at renewAtPercent of its lifetime. Calling
// it the first time creates v1; calling it again creates a new version (v2,
// v3, …) under the same name and policy. The "latest" pointer in AKV
// follows the most recent version, which is what ESO sees when it fetches by
// name without a version pin.
//
// validity is the requested certificate lifetime in months. renewAtPercent is
// the LifetimePercentage at which AKV should attempt auto-renew (cosmetic in
// CI, where we trigger rotation explicitly by calling this helper again).
func CreateOrRotateSelfSignedKVCert(
	ctx context.Context,
	cred azcore.TokenCredential,
	vaultURL, certName, dnsName string,
	validityMonths int32,
	renewAtPercent int32,
) (*KeyVaultCertResult, error) {
	return createOrRotateKVCert(ctx, cred, vaultURL, certName, dnsName,
		azcertificates.IssuerParameters{Name: to.Ptr("Self")},
		validityMonths, renewAtPercent, 2*time.Minute)
}

// CreateOrRotateDigiCertKVCert requests an OV TLS certificate through a DigiCert
// issuer already configured in the vault. The organization and dnsName (including
// its wildcard) must be validated in DigiCert CertCentral for unattended issuance.
// The policy uses an exportable RSA key, PFX secret content, and SANs for dnsName
// and *.<dnsName>, with AutoRenew at renewAtPercent of the certificate lifetime.
// DigiCert may cap the requested validityMonths according to its issuance rules.
//
// Each call submits one issuance request, then polls the asynchronous operation
// until completion, failure, cancellation, or timeout. Calling it again creates a
// new certificate version. A timeout does not cancel the pending request in Key
// Vault; callers should inspect that operation before submitting another request.
func CreateOrRotateDigiCertKVCert(
	ctx context.Context,
	cred azcore.TokenCredential,
	vaultURL, certName, issuerName, dnsName string,
	validityMonths int32,
	renewAtPercent int32,
	timeout time.Duration,
) (*KeyVaultCertResult, error) {
	if issuerName == "" || strings.EqualFold(issuerName, "Self") || strings.EqualFold(issuerName, "Unknown") {
		return nil, fmt.Errorf("DigiCert issuer must name a configured Key Vault issuer, got %q", issuerName)
	}
	return createOrRotateKVCert(ctx, cred, vaultURL, certName, dnsName,
		azcertificates.IssuerParameters{
			Name:            to.Ptr(issuerName),
			CertificateType: to.Ptr("OV-SSL"),
		}, validityMonths, renewAtPercent, timeout)
}

func createOrRotateKVCert(
	ctx context.Context,
	cred azcore.TokenCredential,
	vaultURL, certName, dnsName string,
	issuer azcertificates.IssuerParameters,
	validityMonths, renewAtPercent int32,
	timeout time.Duration,
) (*KeyVaultCertResult, error) {
	client, err := azcertificates.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("constructing azcertificates client for %s: %w", vaultURL, err)
	}

	policy := azcertificates.CertificatePolicy{
		IssuerParameters: &issuer,
		KeyProperties: &azcertificates.KeyProperties{
			Exportable: to.Ptr(true),
			KeyType:    to.Ptr(azcertificates.KeyTypeRSA),
			KeySize:    to.Ptr(int32(2048)),
			ReuseKey:   to.Ptr(false),
		},
		SecretProperties: &azcertificates.SecretProperties{
			ContentType: to.Ptr("application/x-pkcs12"),
		},
		X509CertificateProperties: &azcertificates.X509CertificateProperties{
			Subject: to.Ptr(fmt.Sprintf("CN=*.%s", dnsName)),
			SubjectAlternativeNames: &azcertificates.SubjectAlternativeNames{
				DNSNames: []*string{to.Ptr(dnsName), to.Ptr(fmt.Sprintf("*.%s", dnsName))},
			},
			KeyUsage: []*azcertificates.KeyUsageType{
				to.Ptr(azcertificates.KeyUsageTypeDigitalSignature),
				to.Ptr(azcertificates.KeyUsageTypeKeyEncipherment),
			},
			ValidityInMonths: to.Ptr(validityMonths),
		},
		LifetimeActions: []*azcertificates.LifetimeAction{
			{
				Trigger: &azcertificates.LifetimeActionTrigger{
					LifetimePercentage: to.Ptr(renewAtPercent),
				},
				Action: &azcertificates.LifetimeActionType{
					ActionType: to.Ptr(azcertificates.CertificatePolicyActionAutoRenew),
				},
			},
		},
	}

	return createKVCertAndWait(ctx, client, vaultURL, certName, policy, timeout)
}

func createKVCertAndWait(
	ctx context.Context,
	client *azcertificates.Client,
	vaultURL, certName string,
	policy azcertificates.CertificatePolicy,
	timeout time.Duration,
) (*KeyVaultCertResult, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("certificate issuance timeout must be > 0, got %s", timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	operation, err := client.CreateCertificate(ctx, certName, azcertificates.CreateCertificateParameters{
		CertificatePolicy: &policy,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("starting create of cert %s/%s: %w", vaultURL, certName, err)
	}

	// Azure can return the CSR at creation or in a later operation response.
	// Log the first available CSR before handling terminal status, so failures
	// retain these diagnostics without repeating them on every poll.
	csrLogged := false
	logCSR := func(operation azcertificates.CertificateOperation) {
		if csrLogged || len(operation.CSR) == 0 {
			return
		}
		csrLogged = true
		requestID := "not returned"
		if operation.RequestID != nil {
			requestID = *operation.RequestID
		}
		csr, err := x509.ParseCertificateRequest(operation.CSR)
		if err != nil {
			ginkgo.GinkgoLogr.Error(err, "failed to parse Key Vault CSR for diagnostics",
				"vaultURL", vaultURL, "certificate", certName, "requestID", requestID)
			return
		}
		sum := sha256.Sum256(operation.CSR)
		ginkgo.GinkgoLogr.Info("Key Vault certificate signing request",
			"vaultURL", vaultURL, "certificate", certName, "requestID", requestID,
			"subject", csr.Subject.String(), "dnsNames", csr.DNSNames,
			"sha256", hex.EncodeToString(sum[:]))
	}
	logCSR(operation.CertificateOperation)

	lastStatus, lastDetails := "not observed", ""
	for {
		status, err := client.GetCertificateOperation(ctx, certName, nil)
		if err != nil {
			return nil, fmt.Errorf("polling cert operation %s/%s; want completed, last status=%q details=%q: %w", vaultURL, certName, lastStatus, lastDetails, err)
		}
		logCSR(status.CertificateOperation)
		lastStatus, lastDetails = "", ""
		if status.Status != nil {
			lastStatus = *status.Status
		}
		if status.StatusDetails != nil {
			lastDetails = *status.StatusDetails
		}
		if lastStatus == "completed" {
			break
		}
		if lastStatus == "failed" || lastStatus == "canceled" {
			if status.Error != nil {
				return nil, fmt.Errorf("cert operation %s/%s %s; want completed, details=%q: %w", vaultURL, certName, lastStatus, lastDetails, status.Error)
			}
			return nil, fmt.Errorf("cert operation %s/%s %s; want completed, details=%q", vaultURL, certName, lastStatus, lastDetails)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for cert operation %s/%s; want completed, last status=%q details=%q: %w", vaultURL, certName, lastStatus, lastDetails, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}

	// Fetch the final certificate to extract its DER and version.
	got, err := client.GetCertificate(ctx, certName, "", nil)
	if err != nil {
		return nil, fmt.Errorf("fetching issued cert %s/%s: %w", vaultURL, certName, err)
	}
	if len(got.CER) == 0 {
		return nil, fmt.Errorf("issued cert %s/%s has no DER bytes", vaultURL, certName)
	}

	parsed, err := x509.ParseCertificate(got.CER)
	if err != nil {
		return nil, fmt.Errorf("parsing issued cert DER %s/%s: %w", vaultURL, certName, err)
	}
	sum := sha256.Sum256(parsed.Raw)

	var version string
	if got.ID != nil {
		version = got.ID.Version()
	}
	return &KeyVaultCertResult{
		VaultURL: vaultURL,
		Name:     certName,
		Version:  version,
		SHA256:   hex.EncodeToString(sum[:]),
	}, nil
}
