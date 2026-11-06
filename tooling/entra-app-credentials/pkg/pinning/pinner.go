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

package pinning

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/tooling/entra-app-credentials/pkg/expiry"
)

const (
	keyCredentialType  = "AsymmetricX509Cert"
	keyCredentialUsage = "Verify"

	defaultVerificationInterval = 2 * time.Second
)

// Binding maps an Entra application display name to a Key Vault certificate.
type Binding struct {
	ApplicationDisplayName string
	CertificateName        string
	CertificateDNSName     string
}

// Options controls credential reconciliation.
type Options struct {
	CreateMissing bool
	Rotate        bool
	DryRun        bool
	// ExpiryWarning is how long before expiry an existing certificate is
	// reported as expiring; zero reports expiry only.
	ExpiryWarning time.Duration
	Timeout       time.Duration
}

// Application contains the Graph fields used during reconciliation.
type Application struct {
	ID             string
	AppID          string
	DisplayName    string
	KeyCredentials []KeyCredential
}

// KeyCredential contains the Graph fields used to compare pinned certificates.
type KeyCredential struct {
	CustomKeyIdentifier []byte
	Type                string
	Usage               string
}

// Certificate is a public Key Vault certificate.
type Certificate struct {
	// CER is the DER-encoded public certificate.
	CER []byte
	// NotAfter is when the certificate expires.
	NotAfter time.Time
}

// CertificateClient reads public certificates.
type CertificateClient interface {
	GetCertificate(ctx context.Context, name string) (Certificate, error)
	CreateCertificate(ctx context.Context, name, dnsName string, previousCertificate []byte) ([]byte, error)
}

// ApplicationClient reads and updates Entra applications.
type ApplicationClient interface {
	FindApplication(ctx context.Context, displayName string) (*Application, error)
	ReplaceKeyCredentials(ctx context.Context, applicationID, certificateName string, certificate []byte) error
	GetApplication(ctx context.Context, applicationID string) (*Application, error)
}

// Pinner reconciles Key Vault certificates onto Entra applications.
type Pinner struct {
	certificates         CertificateClient
	applications         ApplicationClient
	verificationInterval time.Duration
	now                  func() time.Time
}

// WithClock sets the clock used to judge certificate expiry.
func WithClock(now func() time.Time) PinnerOption {
	return func(p *Pinner) {
		p.now = now
	}
}

func dnsNameOrPlaceholder(binding Binding) string {
	if binding.CertificateDNSName == "" {
		return "<dns-name>"
	}
	return binding.CertificateDNSName
}

// PinnerOption configures a Pinner.
type PinnerOption func(*Pinner)

// WithVerificationInterval sets how long to wait between reads while verifying
// that Microsoft Graph returns the pinned credential.
func WithVerificationInterval(interval time.Duration) PinnerOption {
	return func(p *Pinner) {
		p.verificationInterval = interval
	}
}

// NewPinner creates a certificate reconciler.
func NewPinner(certificates CertificateClient, applications ApplicationClient, options ...PinnerOption) *Pinner {
	pinner := &Pinner{
		certificates:         certificates,
		applications:         applications,
		verificationInterval: defaultVerificationInterval,
		now:                  time.Now,
	}
	for _, option := range options {
		option(pinner)
	}
	return pinner
}

func (p *Pinner) Pin(ctx context.Context, bindings []Binding, options Options) error {
	if options.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}

	logger := logr.FromContextOrDiscard(ctx)
	var errs []error
	pinned := 0
	unchanged := 0
	for _, binding := range bindings {
		bindingCtx, cancel := context.WithTimeout(ctx, options.Timeout)
		result, err := p.pinOne(bindingCtx, binding, options)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", binding.ApplicationDisplayName, err))
			logger.Error(err, "failed to pin certificate", "application", binding.ApplicationDisplayName, "certificate", binding.CertificateName)
			continue
		}

		switch result {
		case resultUnchanged:
			unchanged++
		case resultPinned:
			pinned++
		}
	}

	logger.Info("certificate pinning completed", "requested", len(bindings), "pinned", pinned, "unchanged", unchanged, "failed", len(errs), "dryRun", options.DryRun)
	return errors.Join(errs...)
}

type pinResult int

const (
	resultUnchanged pinResult = iota
	resultPinned
)

func (p *Pinner) pinOne(ctx context.Context, binding Binding, options Options) (pinResult, error) {
	logger := logr.FromContextOrDiscard(ctx)

	certificate, existing, err := p.reconcileCertificate(ctx, binding, options)
	if err != nil {
		return resultUnchanged, fmt.Errorf("reconcile Key Vault certificate %q: %w", binding.CertificateName, err)
	}
	if len(certificate) == 0 {
		return resultUnchanged, fmt.Errorf("certificate %q in Key Vault has no public certificate data", binding.CertificateName)
	}
	if existing != nil {
		problem := p.inspectCertificate(logger, binding, *existing, options.ExpiryWarning)
		switch {
		case problem != nil && options.Rotate:
			// Only reachable as a dry run: a real rotation replaces the certificate.
			logger.Info("WARNING: " + problem.Error())
		case problem != nil:
			return resultUnchanged, fmt.Errorf("%w; rotate it with pin --rotate --mapping '%s;%s;%s'", problem, binding.ApplicationDisplayName, binding.CertificateName, dnsNameOrPlaceholder(binding))
		}
	}
	thumbprint := sha1.Sum(certificate) //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.

	application, err := p.applications.FindApplication(ctx, binding.ApplicationDisplayName)
	if err != nil {
		return resultUnchanged, fmt.Errorf("find Entra application: %w", err)
	}
	if options.DryRun && options.Rotate {
		logger.Info("would rotate Key Vault certificate and replace key credentials", "application", binding.ApplicationDisplayName, "appId", application.AppID, "certificate", binding.CertificateName)
		return resultPinned, nil
	}
	if hasDesiredCredential(application.KeyCredentials, thumbprint[:]) {
		logger.Info("certificate already pinned", "application", binding.ApplicationDisplayName, "appId", application.AppID, "certificate", binding.CertificateName)
		return resultUnchanged, nil
	}
	if options.DryRun {
		logger.Info("would replace key credentials", "application", binding.ApplicationDisplayName, "appId", application.AppID, "certificate", binding.CertificateName)
		return resultPinned, nil
	}

	err = p.applications.ReplaceKeyCredentials(ctx, application.ID, binding.CertificateName, certificate)
	if err != nil {
		return resultUnchanged, fmt.Errorf("replace key credentials: %w", err)
	}
	err = p.waitForDesiredCredential(ctx, application.ID, thumbprint[:])
	if err != nil {
		return resultUnchanged, err
	}

	logger.Info("certificate pinned", "application", binding.ApplicationDisplayName, "appId", application.AppID, "certificate", binding.CertificateName)
	return resultPinned, nil
}

// reconcileCertificate returns the Key Vault certificate to pin, creating it
// when missing or rotating it as requested. existing is set when the returned
// certificate was already in Key Vault, neither created nor rotated by this call.
// A dry-run rotation reads the current certificate and replaces nothing.
func (p *Pinner) reconcileCertificate(ctx context.Context, binding Binding, options Options) (certificate []byte, existing *Certificate, err error) {
	logger := logr.FromContextOrDiscard(ctx)

	current, err := p.certificates.GetCertificate(ctx, binding.CertificateName)
	switch {
	case errors.Is(err, ErrCertificateNotFound) && options.CreateMissing:
		logger.Info("creating missing Key Vault certificate", "certificate", binding.CertificateName, "dnsName", binding.CertificateDNSName)
		certificate, err = p.certificates.CreateCertificate(ctx, binding.CertificateName, binding.CertificateDNSName, nil)
		return certificate, nil, err
	case err != nil:
		return nil, nil, err
	case options.Rotate && !options.DryRun:
		logger.Info("disruptively rotating Key Vault certificate", "certificate", binding.CertificateName, "dnsName", binding.CertificateDNSName)
		certificate, err = p.certificates.CreateCertificate(ctx, binding.CertificateName, binding.CertificateDNSName, current.CER)
		return certificate, nil, err
	default:
		return current.CER, &current, nil
	}
}

// inspectCertificate logs the expiry of a certificate already in Key Vault and
// returns an error when it has expired. Expiry within the warning window only logs.
func (p *Pinner) inspectCertificate(logger logr.Logger, binding Binding, certificate Certificate, warning time.Duration) error {
	now := p.now()
	end := certificate.NotAfter.UTC().Format(time.RFC3339)
	switch expiry.Classify(certificate.NotAfter, now, warning) {
	case expiry.Expired:
		return fmt.Errorf("certificate %q expired at %s", binding.CertificateName, end)
	case expiry.Expiring:
		logger.Info("WARNING: certificate expires soon", "certificate", binding.CertificateName, "expires", end, "daysRemaining", expiry.Days(certificate.NotAfter, now))
	default:
		logger.Info("certificate is valid", "certificate", binding.CertificateName, "expires", end, "daysRemaining", expiry.Days(certificate.NotAfter, now))
	}
	return nil
}

func (p *Pinner) waitForDesiredCredential(ctx context.Context, applicationID string, thumbprint []byte) error {
	err := wait.PollUntilContextCancel(ctx, p.verificationInterval, true, func(ctx context.Context) (bool, error) {
		updated, err := p.applications.GetApplication(ctx, applicationID)
		if err != nil {
			return false, fmt.Errorf("verify updated application: %w", err)
		}
		return hasDesiredCredential(updated.KeyCredentials, thumbprint), nil
	})
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return fmt.Errorf("verify updated application: desired certificate thumbprint is not the application's sole key credential: %w", ctx.Err())
	default:
		return err
	}
}

func hasDesiredCredential(credentials []KeyCredential, thumbprint []byte) bool {
	return len(credentials) == 1 &&
		credentials[0].Type == keyCredentialType &&
		credentials[0].Usage == keyCredentialUsage &&
		bytes.Equal(credentials[0].CustomKeyIdentifier, thumbprint)
}
