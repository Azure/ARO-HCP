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

package publish

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// TestAppTokenSourceMintsInstallationToken covers the App authentication flow.
func TestAppTokenSourceMintsInstallationToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "app.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, encoded, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	var assertion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/Azure/ARO-HCP/installation" && r.Method == http.MethodGet:
			assertion = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1234}`))
		case r.URL.Path == "/app/installations/1234/access_tokens" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"installation-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	original := githubAPIBaseURL
	githubAPIBaseURL = server.URL
	t.Cleanup(func() { githubAPIBaseURL = original })

	token, err := AppTokenSource{AppID: "99", KeyPath: keyPath, Client: server.Client()}.Token(context.Background(), "Azure/ARO-HCP")
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if token != "installation-token" {
		t.Errorf("Token() = %q, want installation-token", token)
	}
	parsed, err := jwt.Parse(assertion, func(*jwt.Token) (any, error) { return key.Public(), nil })
	if err != nil {
		t.Fatalf("assertion is not signed by the App key: %v", err)
	}
	if issuer, err := parsed.Claims.GetIssuer(); err != nil || issuer != "99" {
		t.Errorf("assertion issuer = %q, err %v, want 99", issuer, err)
	}
}
