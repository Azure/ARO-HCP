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

package request

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Client struct {
	httpClient  *http.Client
	hostHeader  string
	bearerToken string
}

func NewClient(bearerToken string, hostHeader string, insecureSkipVerify bool) *Client {
	httpClient := &http.Client{}

	// Configure TLS if InsecureSkipVerify is enabled
	if insecureSkipVerify {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	return &Client{
		httpClient:  httpClient,
		bearerToken: bearerToken,
		hostHeader:  hostHeader,
	}
}

// SendRequest sends an HTTP request with custom headers and bearer token authentication
func (c *Client) SendRequest(ctx context.Context, url string, method string, body interface{}) ([]byte, error) {
	// Prepare request body if applicable
	var bodyReader io.Reader
	if method != http.MethodGet && body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set Host header if provided (for port-forward scenarios with VirtualServices)
	if c.hostHeader != "" {
		req.Host = c.hostHeader
	}

	// Set authorization header

	if c.bearerToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.bearerToken))
	}

	// Send the HTTP request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	responseBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check if request was successful
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorContent := string(responseBytes)
		if errorContent == "" {
			errorContent = "No content"
		}
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, errorContent)
	}

	return responseBytes, nil
}
