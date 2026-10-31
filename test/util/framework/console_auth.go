// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func CheckConsoleLoginRedirect(ctx context.Context, consoleLoginURL, expectedAuthorizationEndpoint string) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // development cluster console routes use self-signed ingress certificates
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, consoleLoginURL, nil)
	if err != nil {
		return fmt.Errorf("failed to build console login readiness request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to request console login endpoint: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusFound {
		return fmt.Errorf("console login endpoint returned HTTP %d; expected HTTP %d redirect to %q", resp.StatusCode, http.StatusFound, expectedAuthorizationEndpoint)
	}
	location, err := resp.Location()
	if err != nil {
		return fmt.Errorf("console login redirect did not contain a valid Location header: %w", err)
	}
	expectedURL, err := url.Parse(expectedAuthorizationEndpoint)
	if err != nil {
		return fmt.Errorf("failed to parse expected authorization endpoint: %w", err)
	}
	if !strings.EqualFold(location.Scheme, expectedURL.Scheme) ||
		!strings.EqualFold(location.Host, expectedURL.Host) ||
		location.Path != expectedURL.Path {
		return fmt.Errorf("console login endpoint redirected to %q; expected authorization endpoint %q", location.String(), expectedAuthorizationEndpoint)
	}
	return nil
}
