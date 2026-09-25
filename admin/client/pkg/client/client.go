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

package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"

	"github.com/go-logr/logr"
)

// VersionPinResponse is the response from PinClusterVersion.
type VersionPinResponse struct {
	ExactVersion      *string `json:"exactVersion,omitempty"`
	UntilExactVersion *string `json:"untilExactVersion,omitempty"`
}

type Client interface {
	HelloWorld(ctx context.Context) error
	PinClusterVersion(ctx context.Context, clusterResourceID string, exactVersion, untilExactVersion *string) (*VersionPinResponse, error)
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type client struct {
	token      string
	endpoint   string
	hostHeader string
	client     httpClient
}

var _ Client = (*client)(nil)

func NewClient(endpoint string, hostHeader string, token string, insecureSkipVerify bool, debug bool) Client {
	var roundTripper httpClient = &http.Client{}

	if insecureSkipVerify {
		roundTripper = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
					ServerName:         hostHeader,
				},
			},
		}
	}

	if debug {
		roundTripper = &debuggingRoundTripper{
			token:    token,
			delegate: roundTripper,
		}
	}

	return &client{
		token:      token,
		endpoint:   endpoint,
		hostHeader: hostHeader,
		client:     roundTripper,
	}
}

type debuggingRoundTripper struct {
	token    string
	delegate httpClient
}

func (d *debuggingRoundTripper) Do(request *http.Request) (*http.Response, error) {
	raw, err := httputil.DumpRequest(request, true)
	if err != nil {
		return nil, fmt.Errorf("failed to dump request: %w", err)
	}
	raw = bytes.ReplaceAll(raw, []byte(d.token), []byte("REDACTED"))
	fmt.Println(string(raw))

	resp, err := d.delegate.Do(request)
	if err != nil {
		return resp, err
	}

	raw, err = httputil.DumpResponse(resp, true)
	if err != nil {
		return resp, fmt.Errorf("failed to dump response: %w", err)
	}
	fmt.Println(string(raw))
	return resp, nil
}

var _ httpClient = (*debuggingRoundTripper)(nil)

func (c *client) newGetRequest(ctx context.Context, resource string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s%s", c.endpoint, resource), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Host = c.hostHeader
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	return req, nil
}

func (c *client) newPostRequest(ctx context.Context, resource string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s%s", c.endpoint, resource), body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Host = c.hostHeader
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

func (c *client) HelloWorld(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	req, err := c.newGetRequest(ctx, "/admin/helloworld")
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request %s: %w", req.URL.String(), err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error(err, "Failed to close body.")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get hello world: %d", resp.StatusCode)
	}
	return nil
}

// PinClusterVersion pins or clears the version pin on a single cluster.
// clusterResourceID is the full ARM resource ID of the HCP cluster.
// Pass nil for exactVersion to clear the pin.
// Pass nil for untilExactVersion to pin without an auto-release threshold.
func (c *client) PinClusterVersion(ctx context.Context, clusterResourceID string, exactVersion, untilExactVersion *string) (*VersionPinResponse, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	reqBody := struct {
		ExactVersion      *string `json:"exactVersion,omitempty"`
		UntilExactVersion *string `json:"untilExactVersion,omitempty"`
	}{
		ExactVersion:      exactVersion,
		UntilExactVersion: untilExactVersion,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	resource := fmt.Sprintf("/admin/v1/hcp%s/versionpin", clusterResourceID)
	req, err := c.newPostRequest(ctx, resource, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request %s: %w", req.URL.String(), err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error(err, "Failed to close body.")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("failed to pin cluster version: %d: %s", resp.StatusCode, string(body))
	}

	var pinResp VersionPinResponse
	if err := json.NewDecoder(resp.Body).Decode(&pinResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &pinResp, nil
}
