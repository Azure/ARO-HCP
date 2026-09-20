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
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/utils/ptr"

	configtypes "github.com/Azure/ARO-Tools/config/types"
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

var jobOwnedCertificateName = regexp.MustCompile(`^(?:(?:frontend|admin-api|sessiongate)-cert-(ci00|ci01)|maestro-server)-j[0-9]{7}$`)

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

// effectiveCertificatePolicy also reports whether the certificate is transient CI-owned.
func effectiveCertificatePolicy(options *StepRunOptions, vaultBaseURL, certificateName, commonName, contentType, san, issuer string) (azcertificates.CertificatePolicy, bool) {
	policy := certificatePolicy(commonName, contentType, san, issuer)
	if options.Cloud != "dev" || (options.Environment != "ci00" && options.Environment != "ci01") {
		return policy, false
	}
	match := jobOwnedCertificateName.FindStringSubmatch(certificateName)
	if match == nil || (match[1] != "" && match[1] != options.Environment) {
		return policy, false
	}
	vault, err := url.Parse(vaultBaseURL)
	if err != nil || vault.Scheme != "https" || vault.Host != "aro-hcp-dev-svc-kv.vault.azure.net" ||
		vault.User != nil || (vault.Path != "" && vault.Path != "/") || vault.RawPath != "" ||
		vault.RawQuery != "" || vault.ForceQuery || vault.Fragment != "" {
		return policy, false
	}
	// Transient CI naming is a templatize-only rule, not an EV2 schema setting.
	// Explicitly replace AutoRenew: omitting lifetime actions may leave it enabled.
	policy.LifetimeActions[0].Action.ActionType = to.Ptr(azcertificates.CertificatePolicyActionEmailContacts)
	return policy, true
}

// runCreateCertificateStep creates or updates a certificate in Azure Key Vault.
// It is idempotent: if a certificate with a matching policy already exists, creation is skipped.
func runCreateCertificateStep(ctx context.Context, step *types.CreateCertificateStep, options *StepRunOptions, id graph.Identifier, state *ExecutionState) error {
	logger := logr.FromContextOrDiscard(ctx)

	state.RLock()
	outputs := state.GetOutputs(id.Stamp)
	state.RUnlock()

	skip, err := shouldSkipCertificateStep(step.Manage, options.Configuration, outputs, id.ServiceGroup)
	if err != nil {
		return err
	}
	if skip {
		logger.Info("Skipping certificate step, manage is Disabled", "step", step.StepName())
		return nil
	}

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

	desiredPolicy, transient := effectiveCertificatePolicy(options, vaultBaseUrl, certificateName, commonName, contentType, san, issuer)
	return reconcileCertificate(ctx, logger, client, vaultBaseUrl, certificateName, desiredPolicy, transient)
}

func reconcileCertificate(ctx context.Context, logger logr.Logger, client *azcertificates.Client, vaultBaseUrl, certificateName string, desiredPolicy azcertificates.CertificatePolicy, transient bool) error {
	existing, err := client.GetCertificate(ctx, certificateName, "", nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
			return fmt.Errorf("failed to get existing certificate %q in vault %q: %w", certificateName, vaultBaseUrl, err)
		}
	}
	if err == nil && existing.Policy != nil {
		// Lifetime actions affect future renewal, not the issued certificate. Avoid
		// creating a paid new version for transient CI certificates when only these
		// settings differ. Keep the existing recreation behavior everywhere else.
		withoutRenewalDifference := *existing.Policy
		if transient {
			withoutRenewalDifference.LifetimeActions = desiredPolicy.LifetimeActions
		}
		if policyMatches(&withoutRenewalDifference, &desiredPolicy) {
			if !lifetimeActionsMatch(existing.Policy.LifetimeActions, desiredPolicy.LifetimeActions) {
				if _, err := client.UpdateCertificatePolicy(ctx, certificateName, desiredPolicy, nil); err != nil {
					return fmt.Errorf("failed to update certificate policy %q in vault %q: %w", certificateName, vaultBaseUrl, err)
				}
			}
			logger.Info("Certificate already exists with matching policy, skipping creation", "certificateName", certificateName)
			return storeCertificateThumbprintTag(ctx, logger, client, certificateName, vaultBaseUrl)
		}
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
	return storeCertificateThumbprintTag(ctx, logger, client, certificateName, vaultBaseUrl)
}

// storeCertificateThumbprintTag fetches the certificate thumbprint and stores it as a tag
// on the certificate. Tags set on certificates propagate to their associated secrets,
// making the thumbprint readable from ARM/bicep via the secret's tags.
//
// WARNING: The tag becomes stale when Key Vault auto-renews the certificate
// and is only reconciled on the next pipeline run. Do not rely on tag-based
// thumbprint lookup outside of dev environments. We will discontinue this feature
// once we remove Maestro and EventGrid.
func storeCertificateThumbprintTag(ctx context.Context, logger logr.Logger, client *azcertificates.Client, certificateName, vaultBaseUrl string) error {
	certResp, err := client.GetCertificate(ctx, certificateName, "", nil)
	if err != nil {
		return fmt.Errorf("failed to get certificate %q for thumbprint tagging in vault %q: %w", certificateName, vaultBaseUrl, err)
	}

	thumbprint := strings.ToUpper(hex.EncodeToString(certResp.X509Thumbprint))
	if len(thumbprint) == 0 {
		return fmt.Errorf("certificate %q in vault %q has empty X509Thumbprint", certificateName, vaultBaseUrl)
	}

	if certResp.Tags != nil && ptr.Deref(certResp.Tags["thumbprint"], "") == thumbprint {
		logger.Info("Certificate thumbprint tag already set", "certificateName", certificateName, "thumbprint", thumbprint)
		return nil
	}

	tags := make(map[string]*string)
	maps.Copy(tags, certResp.Tags)
	tags["thumbprint"] = to.Ptr(thumbprint)

	_, err = client.UpdateCertificate(ctx, certificateName, "", azcertificates.UpdateCertificateParameters{
		Tags: tags,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to update thumbprint tag on certificate %q in vault %q: %w", certificateName, vaultBaseUrl, err)
	}

	logger.Info("Certificate thumbprint tag updated", "certificateName", certificateName, "thumbprint", thumbprint)
	return nil
}

func shouldSkipCertificateStep(manage *types.Value, cfg configtypes.Configuration, outputs Outputs, serviceGroup string) (bool, error) {
	if manage == nil {
		return false, nil
	}
	resolved, err := resolveValue(*manage, cfg, outputs, serviceGroup)
	if err != nil {
		return false, err
	}
	switch resolved {
	case types.CertificateManageEnabled:
		return false, nil
	case types.CertificateManageDisabled:
		return true, nil
	default:
		return false, fmt.Errorf("manage field must be %q or %q, got %q", types.CertificateManageEnabled, types.CertificateManageDisabled, resolved)
	}
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
		if existing.X509CertificateProperties.SubjectAlternativeNames != desired.X509CertificateProperties.SubjectAlternativeNames {
			return false
		}
	} else {
		existingSANs := ptrSliceToStrings(existing.X509CertificateProperties.SubjectAlternativeNames.DNSNames)
		desiredSANs := ptrSliceToStrings(desired.X509CertificateProperties.SubjectAlternativeNames.DNSNames)
		slices.Sort(existingSANs)
		slices.Sort(desiredSANs)
		if !slices.Equal(existingSANs, desiredSANs) {
			return false
		}
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
