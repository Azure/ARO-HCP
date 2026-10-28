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

package framework

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
	certificatesfake "github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates/fake"
)

func TestCreateKVCertAndWait(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1)}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, publicKey, privateKey)
	require.NoError(t, err)
	wantSHA := sha256.Sum256(der)

	const vaultURL = "https://test.vault.azure.net"
	for _, test := range []struct {
		name       string
		statuses   []string
		timeout    time.Duration
		cancel     bool
		wantErr    error
		wantStatus string
	}{
		{name: "pending then completed", statuses: []string{"inProgress", "completed"}, timeout: time.Minute},
		{name: "CA rejected request", statuses: []string{"failed"}, timeout: time.Minute, wantStatus: "failed"},
		{name: "CA canceled request", statuses: []string{"canceled"}, timeout: time.Minute, wantStatus: "canceled"},
		{name: "issuance timed out", statuses: []string{"inProgress"}, timeout: 100 * time.Millisecond, wantErr: context.DeadlineExceeded},
		{name: "caller canceled", statuses: []string{"inProgress"}, timeout: time.Minute, cancel: true, wantErr: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			creates, polls, gets := 0, 0, 0
			server := certificatesfake.Server{
				CreateCertificate: func(_ context.Context, _ string, _ azcertificates.CreateCertificateParameters, _ *azcertificates.CreateCertificateOptions) (resp azfake.Responder[azcertificates.CreateCertificateResponse], errResp azfake.ErrorResponder) {
					creates++
					resp.SetResponse(http.StatusAccepted, azcertificates.CreateCertificateResponse{
						CertificateOperation: azcertificates.CertificateOperation{Status: to.Ptr("inProgress")},
					}, nil)
					return
				},
				GetCertificateOperation: func(_ context.Context, _ string, _ *azcertificates.GetCertificateOperationOptions) (resp azfake.Responder[azcertificates.GetCertificateOperationResponse], errResp azfake.ErrorResponder) {
					status := test.statuses[min(polls, len(test.statuses)-1)]
					polls++
					operation := azcertificates.CertificateOperation{Status: to.Ptr(status)}
					if status == "failed" {
						operation.StatusDetails = to.Ptr("DigiCert domain validation failed")
						operation.Error = &azcertificates.ErrorInfo{Code: "Rejected"}
					}
					resp.SetResponse(http.StatusOK, azcertificates.GetCertificateOperationResponse{CertificateOperation: operation}, nil)
					if test.cancel {
						cancel()
					}
					return
				},
				GetCertificate: func(_ context.Context, _ string, _ string, _ *azcertificates.GetCertificateOptions) (resp azfake.Responder[azcertificates.GetCertificateResponse], errResp azfake.ErrorResponder) {
					gets++
					resp.SetResponse(http.StatusOK, azcertificates.GetCertificateResponse{
						Certificate: azcertificates.Certificate{
							CER: der,
							ID:  to.Ptr(azcertificates.ID(vaultURL + "/certificates/ingress-default/version-2")),
						},
					}, nil)
					return
				},
			}
			client, err := azcertificates.NewClient(vaultURL, &azfake.TokenCredential{}, &azcertificates.ClientOptions{
				ClientOptions: azcore.ClientOptions{Transport: certificatesfake.NewServerTransport(&server)},
			})
			require.NoError(t, err)

			result, err := createKVCertAndWait(ctx, client, vaultURL, "ingress-default", azcertificates.CertificatePolicy{}, test.timeout)
			assert.Equal(t, 1, creates, "waiting for issuance must not submit another certificate request")
			if test.wantErr != nil || test.wantStatus != "" {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Zero(t, gets, "must not return the previous certificate while issuance is incomplete")
				if test.wantErr != nil {
					assert.ErrorIs(t, err, test.wantErr)
				}
				if test.wantStatus != "" {
					assert.ErrorContains(t, err, test.wantStatus)
				}
				if test.wantStatus == "failed" {
					assert.ErrorContains(t, err, "DigiCert domain validation failed")
					var caErr *azcertificates.ErrorInfo
					require.ErrorAs(t, err, &caErr)
					assert.Equal(t, "Rejected", caErr.Code)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 2, polls, "pending issuance should be polled until completion")
			assert.Equal(t, 1, gets)
			require.NotNil(t, result)
			assert.Equal(t, vaultURL, result.VaultURL)
			assert.Equal(t, "ingress-default", result.Name)
			assert.Equal(t, "version-2", result.Version)
			assert.Equal(t, hex.EncodeToString(wantSHA[:]), result.SHA256)
		})
	}
}
