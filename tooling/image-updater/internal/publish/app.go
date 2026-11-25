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
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// githubAPIBaseURL is overridden by tests to reach a local server.
var githubAPIBaseURL = "https://api.github.com"

const githubAPIVersion = "2022-11-28"

// AppTokenSource mints GitHub App installation tokens for one repository.
type AppTokenSource struct {
	AppID   string
	KeyPath string
	Client  *http.Client
}

// Token resolves the installation for repository and returns its access token.
func (s AppTokenSource) Token(ctx context.Context, repository string) (string, error) {
	key, err := readPrivateKey(s.KeyPath)
	if err != nil {
		return "", err
	}
	assertion, err := appJWT(s.AppID, key, time.Now())
	if err != nil {
		return "", err
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	installation, err := installationID(ctx, client, assertion, repository)
	if err != nil {
		return "", err
	}
	return installationToken(ctx, client, assertion, installation)
}

// readPrivateKey loads an RSA private key in PKCS#1 or PKCS#8 PEM form.
func readPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the GitHub App private key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("the GitHub App private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse the GitHub App private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the GitHub App private key is not RSA")
	}
	return key, nil
}

// appJWT signs the short-lived assertion that authenticates the App itself.
func appJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer:    appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign the GitHub App assertion: %w", err)
	}
	return signed, nil
}

// installationID resolves the App installation that serves repository.
func installationID(ctx context.Context, client *http.Client, assertion, repository string) (int64, error) {
	var response struct {
		ID int64 `json:"id"`
	}
	url := githubAPIBaseURL + "/repos/" + repository + "/installation"
	if err := appRequest(ctx, client, assertion, http.MethodGet, url, &response); err != nil {
		return 0, err
	}
	if response.ID <= 0 {
		return 0, fmt.Errorf("the GitHub App installation for %s is unknown", repository)
	}
	return response.ID, nil
}

// installationToken exchanges the App assertion for an installation token.
func installationToken(ctx context.Context, client *http.Client, assertion string, installation int64) (string, error) {
	var response struct {
		Token string `json:"token"`
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPIBaseURL, installation)
	if err := appRequest(ctx, client, assertion, http.MethodPost, url, &response); err != nil {
		return "", err
	}
	if response.Token == "" {
		return "", fmt.Errorf("the GitHub App installation token is empty")
	}
	return response.Token, nil
}

// appRequest performs one App-authenticated call and decodes its JSON body.
func appRequest(ctx context.Context, client *http.Client, assertion, method, url string, target any) error {
	request, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return fmt.Errorf("build the GitHub App request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+assertion)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("call %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return fmt.Errorf("call %s returned %d", url, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode the response from %s: %w", url, err)
	}
	return nil
}
