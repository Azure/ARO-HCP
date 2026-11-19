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

package mockidentities

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates"
)

func publicTestCertificate(t *testing.T) ([]byte, *x509.Certificate) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, template
}

func TestCertificateAdmissionReadOnly(t *testing.T) {
	certificate, template := publicTestCertificate(t)
	thumbprint := sha1.Sum(certificate)
	mismatched := sha1.Sum([]byte("different certificate"))
	name := "aro-hcp-slot-test-cs"
	for _, test := range []struct {
		name, thumbprint, wantError string
		enabled                     bool
	}{
		{name: "healthy", enabled: true, thumbprint: base64.StdEncoding.EncodeToString(thumbprint[:])},
		{name: "hex wire", enabled: true, thumbprint: strings.ToUpper(hex.EncodeToString(thumbprint[:]))},
		{name: "ASCII hex bytes", enabled: true, thumbprint: base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(thumbprint[:])))},
		{name: "disabled", thumbprint: base64.StdEncoding.EncodeToString(thumbprint[:]), wantError: "certificate is disabled"},
		{name: "mismatched pin", enabled: true, thumbprint: base64.StdEncoding.EncodeToString([]byte("wrong")), wantError: "thumbprint is not pinned"},
		{name: "mismatched raw pin", enabled: true, thumbprint: base64.StdEncoding.EncodeToString(mismatched[:]), wantError: "thumbprint is not pinned"},
		{name: "mismatched hex wire", enabled: true, thumbprint: strings.ToUpper(hex.EncodeToString(mismatched[:])), wantError: "thumbprint is not pinned"},
		{name: "mismatched ASCII hex bytes", enabled: true, thumbprint: base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(mismatched[:]))), wantError: "thumbprint is not pinned"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			transport := ownerTransport(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || strings.TrimSuffix(request.URL.Path, "/") != "/certificates/"+name {
					t.Fatalf("unexpected certificate admission request %s %s", request.Method, request.URL)
				}
				reads++
				response := fmt.Sprintf(`{"cer":%q,"attributes":{"enabled":%t}}`, base64.StdEncoding.EncodeToString(certificate), test.enabled)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
			})
			certificateAPI, err := azcertificates.NewClient(VaultURL, testCredential{}, &azcertificates.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			response := fmt.Sprintf(`{"keyCredentials":[{"customKeyIdentifier":%q,"type":"AsymmetricX509Cert","usage":"Verify","startDateTime":%q,"endDateTime":%q}]}`, test.thumbprint, template.NotBefore.Format(time.RFC3339), template.NotAfter.Format(time.RFC3339))
			store := &azureStore{certificateAPI: certificateAPI, graph: graphTestClient(t, []graphExchange{{method: "GET", path: "applications(uniqueName='" + name + "')", query: map[string]string{"$select": "keyCredentials"}, response: response}})}
			err = store.Certificate(t.Context(), name, false)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got error %v, want %q", err, test.wantError)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if reads != 1 {
				t.Fatalf("expected one public certificate read, got %d", reads)
			}
		})
	}
}

func TestCertificateValidation(t *testing.T) {
	certificate, template := publicTestCertificate(t)
	now := time.Now().UTC().Truncate(time.Second)
	thumbprint := sha1.Sum(certificate)
	key := models.NewKeyCredential()
	key.SetCustomKeyIdentifier(thumbprint[:])
	key.SetTypeEscaped(ptr.To("AsymmetricX509Cert"))
	key.SetUsage(ptr.To("Verify"))
	key.SetStartDateTime(&template.NotBefore)
	key.SetEndDateTime(&template.NotAfter)
	keys := []models.KeyCredentialable{key}
	if err := validateCertificate(certificate, keys, now); err != nil {
		t.Fatal(err)
	}
	key.SetTypeEscaped(ptr.To("Symmetric"))
	if err := validateCertificate(certificate, keys, now); err == nil {
		t.Fatal("accepted invalid credential type")
	}
	key.SetTypeEscaped(ptr.To("AsymmetricX509Cert"))
	key.SetUsage(ptr.To("Sign"))
	if err := validateCertificate(certificate, keys, now); err == nil {
		t.Fatal("accepted invalid credential usage")
	}
	key.SetUsage(ptr.To("Verify"))
	for _, instant := range []time.Time{now.Add(-2 * time.Hour), now.Add(2 * time.Hour)} {
		if err := validateCertificate(certificate, keys, instant); err == nil {
			t.Fatal("accepted invalid certificate validity")
		}
	}
	if err := validateCertificate([]byte("invalid"), keys, now); err == nil {
		t.Fatal("accepted malformed DER")
	}
	if err := validateCertificate(certificate, nil, now); err == nil {
		t.Fatal("accepted missing pinned key")
	}
	if err := validateCertificate(certificate, append(keys, key), now); err == nil {
		t.Fatal("accepted multiple pinned keys")
	}
	key.SetEndDateTime(ptr.To(now.Add(-time.Minute)))
	if err := validateCertificate(certificate, keys, now); err == nil {
		t.Fatal("accepted expired Graph key")
	}
	key.SetEndDateTime(&template.NotAfter)
	key.SetCustomKeyIdentifier([]byte("wrong"))
	if err := validateCertificate(certificate, keys, now); err == nil {
		t.Fatal("accepted mismatched thumbprint")
	}
}
