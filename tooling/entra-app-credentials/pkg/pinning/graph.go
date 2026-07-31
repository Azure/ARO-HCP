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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	defaultGraphBaseURL = "https://graph.microsoft.com/v1.0"
	graphScope          = "https://graph.microsoft.com/.default"
	maxErrorBodyBytes   = 16 * 1024
)

var retryInterval = 2 * time.Second

type graphClient struct {
	credential azcore.TokenCredential
	httpClient *http.Client
	baseURL    string
}

type graphApplication struct {
	ID             string               `json:"id"`
	AppID          string               `json:"appId"`
	DisplayName    string               `json:"displayName"`
	KeyCredentials []graphKeyCredential `json:"keyCredentials"`
}

type graphKeyCredential struct {
	CustomKeyIdentifier string  `json:"customKeyIdentifier,omitempty"`
	DisplayName         *string `json:"displayName,omitempty"`
	Key                 []byte  `json:"key,omitempty"`
	Type                string  `json:"type,omitempty"`
	Usage               string  `json:"usage,omitempty"`
}

type graphApplicationCollection struct {
	Value []graphApplication `json:"value"`
}

type graphPatchRequest struct {
	KeyCredentials []graphKeyCredential `json:"keyCredentials"`
}

// NewGraphClient creates a Microsoft Graph application client.
func NewGraphClient(credential azcore.TokenCredential, httpClient *http.Client) ApplicationClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &graphClient{
		credential: credential,
		httpClient: httpClient,
		baseURL:    defaultGraphBaseURL,
	}
}

func (c *graphClient) FindApplication(ctx context.Context, displayName string) (*Application, error) {
	var application *Application
	err := retry(ctx, func() error {
		query := url.Values{}
		query.Set("$filter", fmt.Sprintf("displayName eq '%s'", strings.ReplaceAll(displayName, "'", "''")))
		query.Set("$select", "id,appId,displayName,keyCredentials")

		var response graphApplicationCollection
		if err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/applications?"+query.Encode(), nil, &response); err != nil {
			return err
		}
		switch len(response.Value) {
		case 0:
			return &transientError{err: fmt.Errorf("application %q is not visible in Microsoft Graph yet", displayName)}
		case 1:
			var err error
			application, err = convertApplication(response.Value[0])
			return err
		default:
			return fmt.Errorf("display name %q matched %d applications; expected exactly one", displayName, len(response.Value))
		}
	})
	return application, err
}

func (c *graphClient) ReplaceKeyCredentials(ctx context.Context, applicationID, certificateName string, certificate []byte) error {
	body := graphPatchRequest{
		KeyCredentials: []graphKeyCredential{{
			DisplayName: &certificateName,
			Key:         certificate,
			Type:        keyCredentialType,
			Usage:       keyCredentialUsage,
		}},
	}
	return retry(ctx, func() error {
		return c.doJSON(ctx, http.MethodPatch, c.baseURL+"/applications/"+url.PathEscape(applicationID), body, nil)
	})
}

func (c *graphClient) GetApplication(ctx context.Context, applicationID string) (*Application, error) {
	var application *Application
	err := retry(ctx, func() error {
		query := url.Values{}
		query.Set("$select", "id,appId,displayName,keyCredentials")
		var response graphApplication
		if err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/applications/"+url.PathEscape(applicationID)+"?"+query.Encode(), nil, &response); err != nil {
			return err
		}
		var err error
		application, err = convertApplication(response)
		return err
	})
	return application, err
}

func convertApplication(application graphApplication) (*Application, error) {
	credentials := make([]KeyCredential, 0, len(application.KeyCredentials))
	for _, credential := range application.KeyCredentials {
		customKeyIdentifier, err := decodeCustomKeyIdentifier(credential.CustomKeyIdentifier)
		if err != nil {
			return nil, fmt.Errorf("decode customKeyIdentifier for application %q: %w", application.DisplayName, err)
		}
		credentials = append(credentials, KeyCredential{
			CustomKeyIdentifier: customKeyIdentifier,
			Type:                credential.Type,
			Usage:               credential.Usage,
		})
	}
	return &Application{
		ID:             application.ID,
		AppID:          application.AppID,
		DisplayName:    application.DisplayName,
		KeyCredentials: credentials,
	}, nil
}

func decodeCustomKeyIdentifier(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	if decoded, err := hex.DecodeString(value); err == nil {
		return decoded, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("value is neither hexadecimal nor base64: %w", err)
	}
	return decoded, nil
}

func (c *graphClient) doJSON(ctx context.Context, method, endpoint string, requestBody, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graphScope}})
	if err != nil {
		return fmt.Errorf("get Microsoft Graph token: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token.Token)
	request.Header.Set("Accept", "application/json")
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return &transientError{err: fmt.Errorf("send Microsoft Graph request: %w", err)}
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		errorBody, readErr := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
		if readErr != nil {
			return fmt.Errorf("read Microsoft Graph error response: %w", readErr)
		}
		graphErr := &httpError{
			statusCode: response.StatusCode,
			status:     response.Status,
			body:       strings.TrimSpace(string(errorBody)),
			retryAfter: parseRetryAfter(response.Header.Get("Retry-After")),
		}
		if graphErr.transient() {
			return &transientError{err: graphErr, retryAfter: graphErr.retryAfter}
		}
		return graphErr
	}

	if responseBody == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(responseBody); err != nil {
		return fmt.Errorf("decode Microsoft Graph response: %w", err)
	}
	return nil
}

type httpError struct {
	statusCode int
	status     string
	body       string
	retryAfter time.Duration
}

func (e *httpError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("Microsoft Graph returned %s", e.status)
	}
	return fmt.Sprintf("Microsoft Graph returned %s: %s", e.status, e.body)
}

func (e *httpError) transient() bool {
	return e.statusCode == http.StatusNotFound ||
		e.statusCode == http.StatusTooManyRequests ||
		e.statusCode >= http.StatusInternalServerError
}

type transientError struct {
	err        error
	retryAfter time.Duration
}

func (e *transientError) Error() string {
	return e.err.Error()
}

func (e *transientError) Unwrap() error {
	return e.err
}

func retry(ctx context.Context, operation func() error) error {
	var lastErr error
	for {
		err := operation()
		if err == nil {
			return nil
		}
		var transient *transientError
		if !errors.As(err, &transient) {
			return err
		}
		lastErr = err
		delay := retryInterval
		if transient.retryAfter > delay {
			delay = transient.retryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("retry Microsoft Graph operation: %w (last error: %v)", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func parseRetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
