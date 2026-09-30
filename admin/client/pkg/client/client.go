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
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httputil"
)

// Client currently exposes no operations; it is retained for future admin endpoints.
type Client interface{}

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
