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
)

const (
	keyCredentialType  = "AsymmetricX509Cert"
	keyCredentialUsage = "Verify"
)

var verificationInterval = 2 * time.Second

// Binding maps an Entra application display name to a Key Vault certificate.
type Binding struct {
	ApplicationDisplayName string
	CertificateName        string
	CertificateDNSName     string
}

// Options controls credential reconciliation.
type Options struct {
	ReplaceAll    bool
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
	certificates CertificateClient
	applications ApplicationClient
}

// NewPinner creates a certificate reconciler.
func NewPinner(certificates CertificateClient, applications ApplicationClient) *Pinner {
	return &Pinner{
		certificates: certificates,
		applications: applications,
	}
}

func (p *Pinner) Pin(ctx context.Context, bindings []Binding, options Options) error {
	if !options.ReplaceAll {
		return fmt.Errorf("replace-all must be explicitly enabled")
	}
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
		if result == resultUnchanged {
			unchanged++
		} else {
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

	certificate, err := p.certificates.GetCertificate(ctx, binding.CertificateName)
	if errors.Is(err, ErrCertificateNotFound) && options.CreateMissing {
		logger.Info("creating missing Key Vault certificate", "certificate", binding.CertificateName, "dnsName", binding.CertificateDNSName)
		certificate, err = p.certificates.CreateCertificate(ctx, binding.CertificateName, binding.CertificateDNSName, nil)
	} else if err == nil && options.Rotate {
		logger.Info("disruptively rotating Key Vault certificate", "certificate", binding.CertificateName, "dnsName", binding.CertificateDNSName)
		certificate, err = p.certificates.CreateCertificate(ctx, binding.CertificateName, binding.CertificateDNSName, certificate)
	}
	if err != nil {
		return resultUnchanged, fmt.Errorf("reconcile Key Vault certificate %q: %w", binding.CertificateName, err)
	}
	if len(certificate) == 0 {
		return resultUnchanged, fmt.Errorf("Key Vault certificate %q has no public certificate data", binding.CertificateName)
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

	if err := p.applications.ReplaceKeyCredentials(ctx, application.ID, binding.CertificateName, certificate); err != nil {
		return resultUnchanged, fmt.Errorf("replace key credentials: %w", err)
	}
	if err := p.waitForDesiredCredential(ctx, application.ID, thumbprint[:]); err != nil {
		return resultUnchanged, err
	}

	logger.Info("certificate pinned", "application", binding.ApplicationDisplayName, "appId", application.AppID, "certificate", binding.CertificateName)
	return resultPinned, nil
}

func (p *Pinner) waitForDesiredCredential(ctx context.Context, applicationID string, thumbprint []byte) error {
	var lastErr error
	for {
		updated, err := p.applications.GetApplication(ctx, applicationID)
		if err != nil {
			return fmt.Errorf("verify updated application: %w", err)
		}
		if hasDesiredCredential(updated.KeyCredentials, thumbprint) {
			return nil
		}
		lastErr = fmt.Errorf("desired certificate thumbprint is not the application's sole key credential")

		timer := time.NewTimer(verificationInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("verify updated application: %w (last error: %v)", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func hasDesiredCredential(credentials []KeyCredential, thumbprint []byte) bool {
	return len(credentials) == 1 &&
		credentials[0].Type == keyCredentialType &&
		credentials[0].Usage == keyCredentialUsage &&
		bytes.Equal(credentials[0].CustomKeyIdentifier, thumbprint)
}
