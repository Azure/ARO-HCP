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
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

// DNSResolutionTimeout is the default timeout for waiting for DNS to resolve.
const DNSResolutionTimeout = 10 * time.Minute

// HostnameFromURL extracts the hostname (without port or scheme) from a URL
// string. If the input has no scheme, it is treated as a bare host or host:port.
func HostnameFromURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL %q: %w", rawURL, err)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return "", fmt.Errorf("no hostname found in %q", rawURL)
	}
	return hostname, nil
}

// WaitForDNSResolution polls DNS for the given hostname until at least one
// address record is returned, or the timeout is reached. Each poll attempt
// uses a fresh net.Resolver with PreferGo: true to bypass negative DNS
// caching from prior NXDOMAIN responses.
func WaitForDNSResolution(ctx context.Context, hostname string, timeout time.Duration) error {
	var lastErrStr string
	startTime := time.Now()

	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		resolver := &net.Resolver{PreferGo: true}
		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		addrs, lookupErr := resolver.LookupHost(lookupCtx, hostname)
		if lookupErr != nil {
			errStr := formatDNSError(lookupErr, hostname)
			if errStr != lastErrStr {
				klog.Infof("DNS resolution pending for %s: %s (elapsed %s)", hostname, errStr, time.Since(startTime).Truncate(time.Second))
				lastErrStr = errStr
			}
			return false, nil
		}

		klog.Infof("DNS resolved %s -> %v (elapsed %s)", hostname, addrs, time.Since(startTime).Truncate(time.Second))
		return true, nil
	})

	if err != nil {
		return fmt.Errorf("DNS for %s did not resolve within %s (last error: %s): %w", hostname, timeout, lastErrStr, err)
	}
	return nil
}

func formatDNSError(err error, hostname string) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Sprintf("server=%s isNotFound=%v isTemporary=%v err=%s",
			dnsErr.Server, dnsErr.IsNotFound, dnsErr.IsTemporary, dnsErr.Err)
	}
	return err.Error()
}
