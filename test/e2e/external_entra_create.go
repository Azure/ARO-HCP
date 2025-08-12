// Copyright 2025 Microsoft Corporation
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

// Copyright 2025 Microsoft Corporation
// Licensed under the Apache License, Version 2.0



package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azidentity "github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/labels"
)


type entraSecretFiles struct {
	TenantID        string    `json:"tenant_id"`
	AppObjectID     string    `json:"app_object_id"`
	ClientID        string    `json:"client_id"`
	ClientSecret    string    `json:"client_secret"`
	DisplayName     string    `json:"display_name"`
	CreatedAt       time.Time `json:"created_at"`
	SecretExpiresAt time.Time `json:"secret_expires_at"`
}

type entraApplication struct {
	ID          string  `json:"id"`    // object id in Entra ID
	AppID       string  `json:"appId"` // client_id
	DisplayName *string `json:"displayName,omitempty"`
}

type entraAddPasswordRequest struct {
	PasswordCredential struct {
		DisplayName   string    `json:"displayName"`
		StartDateTime time.Time `json:"startDateTime"`
		EndDateTime   time.Time `json:"endDateTime"`
	} `json:"passwordCredential"`
}

type entraAddPasswordResponse struct {
	SecretText string `json:"secretText"` // client_secret (only returned once)
	KeyID      string `json:"keyId"`
}

func entraGraphToken(ctx context.Context) (string, error) {
	cred, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		return "", fmt.Errorf("AzureCLICredential init failed: %w", err)
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get Graph token from Azure CLI session: %w", err)
	}
	return tok.Token, nil
}

func entraGraphPOST[T any](ctx context.Context, token, url string, body any, out *T, want int) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		return fmt.Errorf("POST %s: got %d, want %d. request-id=%q body=%s",
			url, resp.StatusCode, want, resp.Header.Get("request-id"), string(b))
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("decode response: %w; body=%s", err, string(b))
		}
	}
	return nil
}

func entraGraphGET[T any](ctx context.Context, token, url string, out *T, want int) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		return fmt.Errorf("GET %s: got %d, want %d. request-id=%q body=%s",
			url, resp.StatusCode, want, resp.Header.Get("request-id"), string(b))
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("decode response: %w; body=%s", err, string(b))
		}
	}
	return nil
}

func entraResolveTenantID(ctx context.Context, token string) (string, error) {
	var org struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	if err := entraGraphGET(ctx, token, "https://graph.microsoft.com/v1.0/organization?$select=id", &org, http.StatusOK); err != nil {
		return "", err
	}
	if len(org.Value) == 0 || org.Value[0].ID == "" {
		return "", fmt.Errorf("no organizations returned; ensure `az account set --tenant <TENANT_ID>`")
	}
	return org.Value[0].ID, nil
}

var _ = Describe("ExternalEntra Create", func() {
	It("creates an Entra app registration and secret, and writes JSON",
		labels.RequireNothing, labels.Critical, labels.Positive, labels.ExternalEntra, labels.RequireHappyPathInfra,
		func(ctx context.Context) {
			token, err := entraGraphToken(ctx)
			Expect(err).NotTo(HaveOccurred())

			tenantID, err := entraResolveTenantID(ctx, token)
			Expect(err).NotTo(HaveOccurred())

			displayName := "e2e-hypershift-oidc"
			outFile := os.Getenv("ENTRA_E2E_SECRET_PATH")
			if outFile == "" {
				outFile = "test/e2e/out/entra_app_secret.json"
			}

			// 1) Create app
			createBody := map[string]any{
				"displayName":    displayName,
				"signInAudience": "AzureADMyOrg",
				"web":            map[string]any{"redirectUris": []string{}},
			}
			var app entraApplication
			Expect(entraGraphPOST(ctx, token, "https://graph.microsoft.com/v1.0/applications", createBody, &app, http.StatusCreated)).To(Succeed())
			Expect(app.ID).NotTo(BeEmpty())
			Expect(app.AppID).NotTo(BeEmpty())

			// 2) Add secret
			start := time.Now().UTC().Round(time.Second)
			end := start.Add(90 * 24 * time.Hour)
			var addReq entraAddPasswordRequest
			addReq.PasswordCredential.DisplayName = "e2e-secret"
			addReq.PasswordCredential.StartDateTime = start
			addReq.PasswordCredential.EndDateTime = end

			var addResp entraAddPasswordResponse
			addURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/applications/%s/addPassword", app.ID)
			Expect(entraGraphPOST(ctx, token, addURL, addReq, &addResp, http.StatusOK)).To(Succeed())
			Expect(addResp.SecretText).NotTo(BeEmpty())

			// 3) Persist JSON (uses shared entraSecretFile type from entra_shared.go)
			_ = os.MkdirAll(filepath.Dir(outFile), 0o755)
			f, err := os.Create(outFile)
			Expect(err).NotTo(HaveOccurred())
			defer f.Close()

			Expect(json.NewEncoder(f).Encode(entraSecretFiles{
				TenantID:        tenantID,
				AppObjectID:     app.ID,
				ClientID:        app.AppID,
				ClientSecret:    addResp.SecretText,
				DisplayName:     displayName,
				CreatedAt:       start,
				SecretExpiresAt: end,
			})).To(Succeed())

			By("saved Entra app credentials JSON at " + outFile)
		})
})
