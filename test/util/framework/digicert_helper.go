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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	"k8s.io/apimachinery/pkg/util/wait"
)

// DigiCertClient implements the CertCentral domain prevalidation API. Certificate
// issuance continues through Key Vault; this client only manages domain validation.
// See https://dev.digicert.com/certcentral-apis/services-api/domains/add-domain.html.
type DigiCertClient struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	pollInterval time.Duration
}

func NewDigiCertClient(apiKey string) (*DigiCertClient, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("DigiCert API key must not be empty")
	}
	return &DigiCertClient{
		apiKey: apiKey, baseURL: "https://www.digicert.com/services/v2",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			// Never forward the API key to a redirect target.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		pollInterval: 15 * time.Second,
	}, nil
}

type digiCertValidation struct {
	Type           string `json:"type"`
	Status         string `json:"status"`
	DCVStatus      string `json:"dcv_status"`
	ValidatedUntil string `json:"validated_until"`
}

// DigiCertDomain contains the token and OV validation state returned by CertCentral.
type DigiCertDomain struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Organization struct {
		ID int64 `json:"id"`
	} `json:"organization"`
	DCVMethod string `json:"dcv_method"`
	DCVToken  struct {
		Token          string `json:"token"`
		ExpirationDate string `json:"expiration_date"`
	} `json:"dcv_token"`
	DCVExpiration string               `json:"dcv_expiration_datetime"`
	Validations   []digiCertValidation `json:"validations"`
}

// OVValidated checks actual validation state and expiration, not the token's
// lifetime or the deprecated top-level domain status field.
func (d *DigiCertDomain) OVValidated() bool {
	for _, validation := range d.Validations {
		if validation.Type != "ov" || validation.Status != "active" || validation.DCVStatus != "complete" {
			continue
		}
		until, err := time.Parse(time.RFC3339, validation.ValidatedUntil)
		if err != nil || !until.After(time.Now()) {
			return false
		}
		if d.DCVExpiration != "" {
			dcvUntil, err := time.Parse(time.RFC3339, d.DCVExpiration)
			if err != nil || !dcvUntil.After(time.Now()) {
				return false
			}
		}
		return true
	}
	return false
}

// EnsureDomain finds an exact domain belonging to organizationID or adds it with
// DNS TXT validation. created is true only for an entry added by this call; callers
// should register cleanup immediately, before publishing DNS or waiting for DCV.
// Existing entries are never reassigned to another organization.
func (c *DigiCertClient) EnsureDomain(ctx context.Context, name string, organizationID int64) (domain *DigiCertDomain, created bool, err error) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" || strings.ContainsAny(name, "*/:") || organizationID <= 0 {
		return nil, false, fmt.Errorf("invalid DigiCert domain %q or organization ID %d", name, organizationID)
	}
	var otherOrganizationID int64
	for offset := 0; ; {
		query := url.Values{
			"filters[search]": {name}, "filters[search_criteria]": {"exact"},
			"limit": {"1000"}, "offset": {strconv.Itoa(offset)},
		}
		var page struct {
			Domains []DigiCertDomain `json:"domains"`
			Page    struct {
				Total int `json:"total"`
			} `json:"page"`
		}
		if err := c.request(ctx, http.MethodGet, "/domain?"+query.Encode(), nil, &page); err != nil {
			return nil, false, err
		}
		for _, existing := range page.Domains {
			if !strings.EqualFold(existing.Name, name) {
				continue
			}
			if existing.Organization.ID == organizationID {
				domain, err := c.getDomain(ctx, existing.ID)
				return domain, false, err
			}
			otherOrganizationID = existing.Organization.ID
		}
		offset += len(page.Domains)
		if len(page.Domains) == 0 || offset >= page.Page.Total {
			break
		}
	}
	if otherOrganizationID != 0 {
		return nil, false, fmt.Errorf("DigiCert domain %s belongs to organization %d, expected %d; refusing to change its organization", name, otherOrganizationID, organizationID)
	}
	parameters := map[string]any{
		"name": name, "organization": map[string]int64{"id": organizationID},
		"validations": []map[string]string{{"type": "ov"}}, "dcv_method": "dns-txt-token",
	}
	domain = &DigiCertDomain{}
	if err := c.request(ctx, http.MethodPost, "/domain", parameters, domain); err != nil {
		return nil, false, err
	}
	if domain.ID <= 0 {
		return nil, false, fmt.Errorf("DigiCert add domain %q returned no domain ID", name)
	}
	domain.Name, domain.Organization.ID = name, organizationID
	return domain, true, nil
}

// SubmitDNSValidation starts or restarts OV validation for an existing domain.
// Call once per validation attempt, rather than once per status poll.
func (c *DigiCertClient) SubmitDNSValidation(ctx context.Context, domainID int64) (*DigiCertDomain, error) {
	var domain DigiCertDomain
	err := c.request(ctx, http.MethodPost, fmt.Sprintf("/domain/%d/validation", domainID), map[string]any{
		"validations": []map[string]string{{"type": "ov"}}, "dcv_method": "dns-txt-token",
	}, &domain)
	if err != nil {
		return nil, err
	}
	domain.ID = domainID
	return &domain, nil
}

func (c *DigiCertClient) getDomain(ctx context.Context, domainID int64) (*DigiCertDomain, error) {
	var domain DigiCertDomain
	err := c.request(ctx, http.MethodGet, fmt.Sprintf("/domain/%d?include_dcv=true&include_validation=true", domainID), nil, &domain)
	return &domain, err
}

// CheckAndWaitForDNSValidation triggers a DCV check once, then polls domain info.
// The caller must first publish the TXT and confirm public DNS propagation.
func (c *DigiCertClient) CheckAndWaitForDNSValidation(ctx context.Context, domainID int64, timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("DigiCert validation timeout must be > 0, got %s", timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.request(ctx, http.MethodPut, fmt.Sprintf("/domain/%d/dcv/validate-token", domainID), nil, nil); err != nil {
		return err
	}
	lastState := "validation not observed"
	err := wait.PollUntilContextCancel(ctx, c.pollInterval, true, func(ctx context.Context) (bool, error) {
		domain, err := c.getDomain(ctx, domainID)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			var apiErr *digiCertAPIError
			var networkErr net.Error
			retryable := (errors.As(err, &apiErr) && (apiErr.statusCode == http.StatusTooManyRequests || apiErr.statusCode >= 500)) || errors.As(err, &networkErr)
			if !retryable {
				return false, err
			}
			state := err.Error()
			if state != lastState {
				ginkgo.GinkgoLogr.Info("waiting for DigiCert domain validation", "domainID", domainID, "error", state)
				lastState = state
			}
			return false, nil
		}
		if domain.OVValidated() {
			return true, nil
		}
		state := fmt.Sprintf("domain=%s OV=%+v DCV expiration=%s", domain.Name, domain.Validations, domain.DCVExpiration)
		if state != lastState {
			ginkgo.GinkgoLogr.Info("waiting for DigiCert domain validation", "domainID", domainID, "want", "current, complete OV validation", "observed", state)
			lastState = state
		}
		for _, validation := range domain.Validations {
			if validation.Type == "ov" && (validation.DCVStatus == "failed" || validation.Status == "rejected") {
				return false, errors.New(state)
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("DigiCert domain %d: expected current, complete OV validation; last observed %s: %w", domainID, lastState, err)
	}
	return nil
}

// DeleteDomain removes only the domain ID the caller has identified as test-owned.
func (c *DigiCertClient) DeleteDomain(ctx context.Context, domainID int64) error {
	err := c.request(ctx, http.MethodDelete, fmt.Sprintf("/domain/%d", domainID), nil, nil)
	var apiErr *digiCertAPIError
	if errors.As(err, &apiErr) && apiErr.statusCode == http.StatusNotFound {
		return nil
	}
	return err
}

type digiCertAPIError struct {
	statusCode int
	details    string
}

func (e *digiCertAPIError) Error() string {
	return fmt.Sprintf("DigiCert HTTP %d: %s", e.statusCode, e.details)
}

func (c *DigiCertClient) request(ctx context.Context, method, path string, parameters, result any) error {
	var body io.Reader
	if parameters != nil {
		encoded, err := json.Marshal(parameters)
		if err != nil {
			return fmt.Errorf("encoding DigiCert request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("constructing DigiCert request: %w", err)
	}
	req.Header.Set("X-DC-DEVKEY", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DigiCert %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &digiCertAPIError{statusCode: resp.StatusCode, details: strings.ReplaceAll(string(data), c.apiKey, "[REDACTED]")}
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(result); err != nil {
			return fmt.Errorf("decoding DigiCert %s %s response: %w", method, path, err)
		}
	}
	return nil
}
