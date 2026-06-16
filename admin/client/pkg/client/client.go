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
	"fmt"
	"net/http"
	"net/http/httputil"

	"github.com/go-logr/logr"
)

type Client interface {
	HelloWorld(ctx context.Context) error
	// Close releases idle connections held by the underlying HTTP transport.
	Close()
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type client struct {
	token     string
	endpoint  string
	hostHeader string
	client    httpClient
	transport *http.Transport // non-nil only when a custom transport was created
}

var _ Client = (*client)(nil)

func NewClient(endpoint string, hostHeader string, token string, insecureSkipVerify bool, debug bool) Client {
	var roundTripper httpClient = &http.Client{}
	var transport *http.Transport

	if insecureSkipVerify {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // intentional for admin CLI usage with self-signed certs
				ServerName:         hostHeader,
			},
		}
		roundTripper = &http.Client{Transport: transport}
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
		transport:  transport,
	}
}

// Close releases idle connections held by the underlying HTTP transport.
// This should be called when the client is no longer needed.
func (c *client) Close() {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
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
