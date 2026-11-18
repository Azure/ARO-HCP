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
	"context"
	"crypto/sha1" //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

var expiryTestNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func TestGetCertificateReportsKeyVaultExpiry(t *testing.T) {
	expires := expiryTestNow.AddDate(1, 0, 0)
	client := &fakeAzureCertificateClient{getResults: [][]byte{[]byte("cer")}, expires: &expires}

	certificate, err := NewCertificateClient(client).GetCertificate(context.Background(), "cert")

	require.NoError(t, err)
	assert.Equal(t, Certificate{CER: []byte("cer"), NotAfter: expires}, certificate)
}

func TestGetCertificateRejectsMissingExpiry(t *testing.T) {
	client := &noExpiryCertificateClient{}

	_, err := NewCertificateClient(client).GetCertificate(context.Background(), "cert")

	require.ErrorContains(t, err, "no expiry")
}

type noExpiryCertificateClient struct{ fakeAzureCertificateClient }

func (noExpiryCertificateClient) GetCertificate(context.Context, string, string, *azcertificates.GetCertificateOptions) (azcertificates.GetCertificateResponse, error) {
	return azcertificates.GetCertificateResponse{Certificate: azcertificates.Certificate{CER: []byte("cer")}}, nil
}

// pinnedFixture returns a pinner whose application already pins the certificate,
// so that pin outcomes depend only on expiry handling.
func pinnedFixture(t *testing.T, notAfter time.Time) (*Pinner, *fakeCertificates, *fakeApplications) {
	t.Helper()
	certificate := []byte("certificate")
	thumbprint := sha1.Sum(certificate) //nolint:gosec // Microsoft Entra certificate thumbprints are defined as SHA-1.
	certificates := &fakeCertificates{
		values:   map[string][]byte{"cert": certificate},
		notAfter: map[string]time.Time{"cert": notAfter},
	}
	apps := &fakeApplications{
		applications: map[string]*Application{"app": {
			ID: "object-id",
			KeyCredentials: []KeyCredential{{
				CustomKeyIdentifier: thumbprint[:],
				Type:                keyCredentialType,
				Usage:               keyCredentialUsage,
			}},
		}},
		getResults: map[string][]*Application{},
	}
	return NewPinner(certificates, apps, WithClock(func() time.Time { return expiryTestNow })), certificates, apps
}

var expiryTestBinding = []Binding{{ApplicationDisplayName: "app", CertificateName: "cert", CertificateDNSName: "cert.example.com"}}

func TestPinFailsForExpiredCertificate(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		pinner, _, apps := pinnedFixture(t, expiryTestNow.Add(-time.Hour))

		err := pinner.Pin(context.Background(), expiryTestBinding, Options{DryRun: dryRun, ExpiryWarning: 90 * 24 * time.Hour, Timeout: time.Second})

		require.ErrorContains(t, err, `certificate "cert" expired at`)
		require.ErrorContains(t, err, "pin --rotate --mapping 'app;cert;cert.example.com'")
		assert.Empty(t, apps.replaceCalls)
	}
}

func TestPinOnlyWarnsForExpiringCertificate(t *testing.T) {
	pinner, _, _ := pinnedFixture(t, expiryTestNow.Add(30*24*time.Hour))

	err := pinner.Pin(context.Background(), expiryTestBinding, Options{ExpiryWarning: 90 * 24 * time.Hour, Timeout: time.Second})

	require.NoError(t, err)
}

func TestPinDryRunRotateReportsExpiredCertificateWithoutFailingOrChanging(t *testing.T) {
	pinner, certificates, apps := pinnedFixture(t, expiryTestNow.Add(-time.Hour))

	err := pinner.Pin(context.Background(), expiryTestBinding, Options{Rotate: true, DryRun: true, Timeout: time.Second})

	require.NoError(t, err)
	assert.Empty(t, certificates.createCalls)
	assert.Empty(t, apps.replaceCalls)
}

func TestPinRotationReplacesExpiredCertificate(t *testing.T) {
	pinner, certificates, apps := pinnedFixture(t, expiryTestNow.Add(-time.Hour))
	pinner.verificationInterval = time.Millisecond

	err := pinner.Pin(context.Background(), expiryTestBinding, Options{Rotate: true, Timeout: time.Second})

	require.NoError(t, err)
	assert.Equal(t, []string{"cert=cert.example.com"}, certificates.createCalls)
	assert.Equal(t, []string{"object-id=cert"}, apps.replaceCalls)
}
