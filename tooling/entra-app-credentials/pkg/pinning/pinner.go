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

// CertificateClient reads public certificates.
type CertificateClient interface {
	GetCertificate(ctx context.Context, name string) ([]byte, error)
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

	certificate, err := p.reconcileCertificate(ctx, binding, options)
	if err != nil {
		return resultUnchanged, fmt.Errorf("reconcile Key Vault certificate %q: %w", binding.CertificateName, err)
	}
	if len(certificate) == 0 {
		return resultUnchanged, fmt.Errorf("certificate %q in Key Vault has no public certificate data", binding.CertificateName)
	}
	thumbprint := sha1.Sum(certificate) //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.

	application, err := p.applications.FindApplication(ctx, binding.ApplicationDisplayName)
	if err != nil {
		return resultUnchanged, fmt.Errorf("find Entra application: %w", err)
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
// when missing or rotating it as requested.
func (p *Pinner) reconcileCertificate(ctx context.Context, binding Binding, options Options) ([]byte, error) {
	logger := logr.FromContextOrDiscard(ctx)

	certificate, err := p.certificates.GetCertificate(ctx, binding.CertificateName)
	switch {
	case errors.Is(err, ErrCertificateNotFound) && options.CreateMissing:
		logger.Info("creating missing Key Vault certificate", "certificate", binding.CertificateName, "dnsName", binding.CertificateDNSName)
		return p.certificates.CreateCertificate(ctx, binding.CertificateName, binding.CertificateDNSName, nil)
	case err == nil && options.Rotate:
		logger.Info("disruptively rotating Key Vault certificate", "certificate", binding.CertificateName, "dnsName", binding.CertificateDNSName)
		return p.certificates.CreateCertificate(ctx, binding.CertificateName, binding.CertificateDNSName, certificate)
	default:
		return certificate, err
	}
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
