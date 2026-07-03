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

package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
	"github.com/Azure/ARO-Tools/tools/cmdutils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

const (
	certificateOperationPollInterval = 5 * time.Second
	certificateOperationTimeout      = 10 * time.Minute
)

var validContentTypes = []string{"x-pkcs12"}

// certificatePolicy builds a Key Vault certificate policy matching the sdp-pipelines wire format.
func certificatePolicy(commonName, contentType, san, issuer string) azcertificates.CertificatePolicy {
	return azcertificates.CertificatePolicy{
		KeyProperties: &azcertificates.KeyProperties{
			Exportable: to.Ptr(true),
			KeyType:    to.Ptr(azcertificates.KeyTypeRSA),
			KeySize:    to.Ptr(int32(2048)),
		},
		SecretProperties: &azcertificates.SecretProperties{
			ContentType: to.Ptr(fmt.Sprintf("application/%s", contentType)),
		},
		X509CertificateProperties: &azcertificates.X509CertificateProperties{
			Subject: to.Ptr(fmt.Sprintf("CN=%s", commonName)),
			SubjectAlternativeNames: &azcertificates.SubjectAlternativeNames{
				DNSNames: []*string{to.Ptr(san)},
			},
			ValidityInMonths: to.Ptr(int32(6)),
		},
		LifetimeActions: []*azcertificates.LifetimeAction{{
			Trigger: &azcertificates.LifetimeActionTrigger{
				LifetimePercentage: to.Ptr(int32(50)),
			},
			Action: &azcertificates.LifetimeActionType{
				ActionType: to.Ptr(azcertificates.CertificatePolicyActionAutoRenew),
			},
		}},
		IssuerParameters: &azcertificates.IssuerParameters{
			Name: to.Ptr(issuer),
		},
	}
}

// runCreateCertificateStep creates or updates a certificate in Azure Key Vault.
// It is idempotent: if a certificate with a matching policy already exists, creation is skipped.
func runCreateCertificateStep(ctx context.Context, step *types.CreateCertificateStep, options *StepRunOptions, id graph.Identifier, state *ExecutionState) error {
	logger := logr.FromContextOrDiscard(ctx)

	state.RLock()
	outputs := state.Outputs
	state.RUnlock()

	vaultBaseUrl, err := resolveValue(step.VaultBaseUrl, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return err
	}
	certificateName, err := resolveValue(step.CertificateName, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return err
	}
	contentType, err := resolveValue(step.ContentType, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return err
	}
	san, err := resolveValue(step.SAN, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return err
	}
	issuer, err := resolveValue(step.Issuer, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return err
	}
	commonName, err := resolveValue(step.CommonName, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return err
	}

	for _, field := range []struct{ name, val string }{
		{"vaultBaseUrl", vaultBaseUrl},
		{"certificateName", certificateName},
		{"contentType", contentType},
		{"san", san},
		{"issuer", issuer},
		{"commonName", commonName},
	} {
		if len(field.val) == 0 {
			return fmt.Errorf("resolved value for %q is empty", field.name)
		}
	}

	if !slices.Contains(validContentTypes, contentType) {
		return fmt.Errorf("invalid contentType %q, must be one of %v", contentType, validContentTypes)
	}

	logger.Info("Creating certificate",
		"vaultBaseUrl", vaultBaseUrl,
		"certificateName", certificateName,
		"issuer", issuer,
		"commonName", commonName,
		"san", san,
	)

	cred, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	client, err := azcertificates.NewClient(vaultBaseUrl, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create certificate client: %w", err)
	}

	desiredPolicy := certificatePolicy(commonName, contentType, san, issuer)

	existing, err := client.GetCertificate(ctx, certificateName, "", nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
			return fmt.Errorf("failed to get existing certificate %q in vault %q: %w", certificateName, vaultBaseUrl, err)
		}
	}
	if err == nil && existing.Policy != nil && policyMatches(existing.Policy, &desiredPolicy) {
		logger.Info("Certificate already exists with matching policy, skipping creation", "certificateName", certificateName)
		return nil
	}
	if err == nil {
		logger.Info("Certificate exists but policy differs, recreating", "certificateName", certificateName)
	}

	createResp, err := client.CreateCertificate(ctx, certificateName, azcertificates.CreateCertificateParameters{
		CertificatePolicy: &desiredPolicy,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to create certificate %q in vault %q: %w", certificateName, vaultBaseUrl, err)
	}

	pollCtx, cancel := context.WithTimeout(ctx, certificateOperationTimeout)
	defer cancel()
	if err := waitForCertificateOperation(pollCtx, logger, client, vaultBaseUrl, certificateName, createResp.Status); err != nil {
		return err
	}

	logger.Info("Certificate created successfully", "certificateName", certificateName)
	return nil
}

// waitForCertificateOperation polls until a certificate operation reaches a terminal state.
func waitForCertificateOperation(ctx context.Context, logger logr.Logger, client *azcertificates.Client, vaultBaseUrl, certificateName string, initialStatus *string) error {
	if initialStatus != nil && *initialStatus == "completed" {
		return nil
	}

	ticker := time.NewTicker(certificateOperationPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for certificate %q in vault %q: %w", certificateName, vaultBaseUrl, ctx.Err())
		case <-ticker.C:
			op, err := client.GetCertificateOperation(ctx, certificateName, nil)
			if err != nil {
				return fmt.Errorf("failed to get certificate operation for %q in vault %q: %w", certificateName, vaultBaseUrl, err)
			}
			status := ptr.Deref(op.Status, "")
			logger.Info("Polling certificate operation", "certificateName", certificateName, "status", status)
			switch status {
			case "completed":
				return nil
			case "cancelled", "error":
				if op.Error != nil {
					return fmt.Errorf("certificate operation for %q in vault %q failed with status %q: code=%s, %s",
						certificateName, vaultBaseUrl, status,
						op.Error.Code, op.Error.Error())
				}
				return fmt.Errorf("certificate operation for %q in vault %q failed with status %q", certificateName, vaultBaseUrl, status)
			}
		}
	}
}

// policyMatches returns true if the existing certificate policy matches the desired one,
// used to determine whether an existing certificate needs to be recreated.
func policyMatches(existing, desired *azcertificates.CertificatePolicy) bool {
	if existing == nil || desired == nil {
		return false
	}

	if existing.IssuerParameters == nil || desired.IssuerParameters == nil ||
		ptr.Deref(existing.IssuerParameters.Name, "") != ptr.Deref(desired.IssuerParameters.Name, "") {
		return false
	}

	if existing.X509CertificateProperties == nil || desired.X509CertificateProperties == nil ||
		ptr.Deref(existing.X509CertificateProperties.Subject, "") != ptr.Deref(desired.X509CertificateProperties.Subject, "") ||
		ptr.Deref(existing.X509CertificateProperties.ValidityInMonths, 0) != ptr.Deref(desired.X509CertificateProperties.ValidityInMonths, 0) {
		return false
	}

	if existing.X509CertificateProperties.SubjectAlternativeNames == nil || desired.X509CertificateProperties.SubjectAlternativeNames == nil {
		return existing.X509CertificateProperties.SubjectAlternativeNames == desired.X509CertificateProperties.SubjectAlternativeNames
	}

	existingSANs := ptrSliceToStrings(existing.X509CertificateProperties.SubjectAlternativeNames.DNSNames)
	desiredSANs := ptrSliceToStrings(desired.X509CertificateProperties.SubjectAlternativeNames.DNSNames)
	slices.Sort(existingSANs)
	slices.Sort(desiredSANs)
	if !slices.Equal(existingSANs, desiredSANs) {
		return false
	}

	if existing.SecretProperties == nil || desired.SecretProperties == nil ||
		ptr.Deref(existing.SecretProperties.ContentType, "") != ptr.Deref(desired.SecretProperties.ContentType, "") {
		return false
	}

	if existing.KeyProperties == nil || desired.KeyProperties == nil ||
		ptr.Deref(existing.KeyProperties.KeySize, 0) != ptr.Deref(desired.KeyProperties.KeySize, 0) ||
		ptr.Deref(existing.KeyProperties.KeyType, "") != ptr.Deref(desired.KeyProperties.KeyType, "") ||
		ptr.Deref(existing.KeyProperties.Exportable, false) != ptr.Deref(desired.KeyProperties.Exportable, false) {
		return false
	}

	if !lifetimeActionsMatch(existing.LifetimeActions, desired.LifetimeActions) {
		return false
	}

	return true
}

func lifetimeActionsMatch(existing, desired []*azcertificates.LifetimeAction) bool {
	return slices.EqualFunc(existing, desired, lifetimeActionEqual)
}

func lifetimeActionEqual(a, b *azcertificates.LifetimeAction) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !triggerEqual(a.Trigger, b.Trigger) {
		return false
	}
	if a.Action == nil || b.Action == nil {
		return a.Action == b.Action
	}
	return ptr.Equal(a.Action.ActionType, b.Action.ActionType)
}

func triggerEqual(a, b *azcertificates.LifetimeActionTrigger) bool {
	if a == nil || b == nil {
		return a == b
	}
	return ptr.Equal(a.DaysBeforeExpiry, b.DaysBeforeExpiry) &&
		ptr.Equal(a.LifetimePercentage, b.LifetimePercentage)
}

func ptrSliceToStrings(ptrs []*string) []string {
	result := make([]string, 0, len(ptrs))
	for _, p := range ptrs {
		if p != nil {
			result = append(result, *p)
		}
	}
	return result
}
