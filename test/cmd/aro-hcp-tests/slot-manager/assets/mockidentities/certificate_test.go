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
	"math/big"
	"testing"
	"time"

	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"k8s.io/utils/ptr"
)

func TestCertificateValidation(t *testing.T) {
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
