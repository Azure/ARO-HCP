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
	"context"
	"crypto/tls"
	"net/http"
	"time"
)

// TestHTTPSConnectivity attempts an HTTPS connection to the given URL.
// Returns nil if the connection succeeds, or an error if it fails (DNS, timeout,
// connection refused, etc.). When insecureSkipVerify is true, TLS certificate
// validation is skipped — useful for testing network reachability without
// requiring valid certificates. Redirects are not followed.
func TestHTTPSConnectivity(ctx context.Context, url string, timeout time.Duration, insecureSkipVerify bool) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, "GET", url, nil)
	if err != nil {
		return err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // caller explicitly opted in
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
