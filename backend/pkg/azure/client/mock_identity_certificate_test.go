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

package client

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/go-logr/logr"
)

type mockCertificateTransport struct{ certificates []*x509.Certificate }

func (s mockCertificateTransport) Do(r *http.Request) (*http.Response, error) {
	body := map[string]any{}
	if strings.Contains(r.URL.Path, ".well-known") {
		body = map[string]any{"authorization_endpoint": "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize", "token_endpoint": "https://login.microsoftonline.com/tenant/oauth2/v2.0/token", "issuer": "https://login.microsoftonline.com/tenant/v2.0"}
	} else if strings.Contains(r.URL.Path, "discovery/instance") {
		body = map[string]any{"tenant_discovery_endpoint": "https://login.microsoftonline.com/tenant/v2.0/.well-known/openid-configuration", "metadata": []any{map[string]any{"preferred_network": "login.microsoftonline.com", "preferred_cache": "login.microsoftonline.com", "aliases": []string{"login.microsoftonline.com"}}}}
	} else if strings.HasSuffix(r.URL.Path, "/token") {
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		parts := strings.Split(r.Form.Get("client_assertion"), ".")
		if len(parts) != 3 {
			return nil, fmt.Errorf("missing signed client assertion")
		}
		signature, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		serial := ""
		for _, certificate := range s.certificates {
			key := certificate.PublicKey.(*rsa.PublicKey)
			if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) == nil || rsa.VerifyPSS(key, crypto.SHA256, digest[:], signature, nil) == nil {
				serial = certificate.SerialNumber.String()
				break
			}
		}
		if serial == "" {
			return nil, fmt.Errorf("assertion is not signed by a test certificate")
		}
		body = map[string]any{"access_token": serial, "token_type": "Bearer", "expires_in": 600}
	} else {
		return nil, fmt.Errorf("unexpected request: %s", r.URL)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
}

func newMockCertificateBundle(t *testing.T, serial int64) ([]byte, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "rotation"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return append(bundle, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})...), certificate
}

func TestMockIdentityCertificateRetainedConsumersRotate(t *testing.T) {
	a, certA := newMockCertificateBundle(t, 1)
	b, certB := newMockCertificateBundle(t, 2)
	c, certC := newMockCertificateBundle(t, 3)
	options := azcore.ClientOptions{Transport: mockCertificateTransport{[]*x509.Certificate{certA, certB, certC}}}
	options.Retry.MaxRetries = -1
	ctx, cancel := context.WithCancel(logr.NewContext(context.Background(), logr.Discard()))
	defer cancel()
	root := t.TempDir()
	swap := func(version string, bundle []byte) {
		t.Helper()
		dir := filepath.Join(root, version)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "certificate.pem"), bundle, 0600); err != nil {
			t.Fatal(err)
		}
		temp := filepath.Join(root, "..next")
		if err := os.Symlink(version, temp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, filepath.Join(root, "..data")); err != nil {
			t.Fatal(err)
		}
	}
	swap("..v1", a)
	path := filepath.Join(root, "certificate.pem")
	if err := os.Symlink("..data/certificate.pem", path); err != nil {
		t.Fatal(err)
	}
	source, err := newMockIdentityCertificate(ctx, path, "client", "tenant", options, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	identity := &HardcodedIdentity{ClientID: "client", PrincipalID: "principal", TenantID: "tenant", certificate: source}
	builder := NewHardcodedIdentityFPAMIDataplaneClientBuilder(&cloud.AzurePublic, identity)
	resourceID, err := azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/test")
	if err != nil {
		t.Fatal(err)
	}
	smi := NewServiceManagedIdentityClientBuilder(builder, &azcorearm.ClientOptions{ClientOptions: options}).(*serviceManagedIdentityClientBuilder)
	retainedSMI, err := smi.credentialsForServiceManagedIdentity(ctx, "unused", resourceID)
	if err != nil {
		t.Fatal(err)
	}
	retainedTokenRetriever, err := NewMIDataplaneBasedIdentityAccessTokenRetrieverBuilder(builder, options).Build("unused", resourceID)
	if err != nil {
		t.Fatal(err)
	}
	var sequence atomic.Int32
	assertToken := func(credential azcore.TokenCredential, want string) {
		t.Helper()
		token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{fmt.Sprintf("https://audit-%d.invalid/.default", sequence.Add(1))}})
		if err != nil {
			t.Fatal(err)
		}
		if token.Token != want {
			t.Fatalf("signed with generation %s, want %s", token.Token, want)
		}
	}
	waitBundle := func(bundle []byte) {
		t.Helper()
		want := sha256.Sum256(bundle)
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for source.generation.Load().digest != want {
			select {
			case <-deadline.C:
				t.Fatal("rotation did not reach retained consumers")
			case <-ticker.C:
			}
		}
	}
	assertToken(retainedSMI, "1")
	assertToken(retainedTokenRetriever, "1")
	initial := source.generation.Load()
	if err := source.reload(ctx); err != nil {
		t.Fatal(err)
	}
	if source.generation.Load() != initial {
		t.Fatal("unchanged content replaced the credential/cache generation")
	}
	swap("..v2", b)
	waitBundle(b)
	assertToken(retainedSMI, "2")
	assertToken(retainedTokenRetriever, "2")
	swap("..invalid", []byte("not a certificate"))
	if err := source.reload(ctx); err == nil {
		t.Fatal("invalid replacement was accepted")
	}
	assertToken(retainedSMI, "2")
	assertToken(retainedTokenRetriever, "2")
	swap("..v3", c)
	waitBundle(c)
	assertToken(retainedSMI, "3")
	assertToken(retainedTokenRetriever, "3")
}

func TestMockIdentityCertificateRejectsInvalidStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "certificate.pem")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(logr.NewContext(context.Background(), logr.Discard()))
	defer cancel()
	if _, err := NewHardcodedIdentity(ctx, path, "client", "principal", "tenant", azcore.ClientOptions{}); err == nil {
		t.Fatal("invalid startup certificate was accepted")
	}
	if _, err := NewHardcodedIdentity(ctx, path+"-missing", "client", "principal", "tenant", azcore.ClientOptions{}); err == nil {
		t.Fatal("missing startup certificate was accepted")
	}
}
