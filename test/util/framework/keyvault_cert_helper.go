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
	"time"

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
	client, err := azcertificates.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("constructing azcertificates client for %s: %w", vaultURL, err)
	}

	policy := azcertificates.CertificatePolicy{
		IssuerParameters: &azcertificates.IssuerParameters{
			Name: to.Ptr("Self"),
		},
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
			Subject: to.Ptr(fmt.Sprintf("CN=%s", dnsName)),
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

	if _, err := client.CreateCertificate(ctx, certName, azcertificates.CreateCertificateParameters{
		CertificatePolicy: &policy,
	}, nil); err != nil {
		return nil, fmt.Errorf("starting create of cert %s/%s: %w", vaultURL, certName, err)
	}

	// Self-signed cert issuance is fast (<10s typical) but can spike; poll for
	// a couple of minutes before giving up.
	deadline := time.Now().Add(2 * time.Minute)
	for {
		status, err := client.GetCertificateOperation(ctx, certName, nil)
		if err != nil {
			return nil, fmt.Errorf("polling cert operation %s/%s: %w", vaultURL, certName, err)
		}
		if status.Status != nil && *status.Status == "completed" {
			break
		}
		if status.Status != nil && *status.Status == "failed" {
			return nil, fmt.Errorf("cert operation %s/%s failed: %+v", vaultURL, certName, status.Error)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for cert operation %s/%s; last status: %v", vaultURL, certName, status.Status)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
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
