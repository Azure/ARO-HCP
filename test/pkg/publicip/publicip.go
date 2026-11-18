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

// Package publicip validates IPv4 addresses and discovers the caller's public IPv4 address.
package publicip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const (
	requestTimeout   = 10 * time.Second
	maxResponseBytes = 256
)

// ParseIPv4 accepts exactly one IPv4 address without whitespace or a CIDR suffix.
// IPv4-mapped IPv6 addresses are not IPv4 addresses for this purpose.
func ParseIPv4(value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid IPv4 address %q: %w", value, err)
	}
	if !address.Is4() {
		return netip.Addr{}, fmt.Errorf("expected a single IPv4 address, got %q", value)
	}
	return address, nil
}

// DiscoverIPv4 queries HTTPS echo services sequentially using IPv4 connections.
// Each request has a finite timeout and respects cancellation of ctx.
func DiscoverIPv4(ctx context.Context) (netip.Addr, error) {
	client := newDiscoveryClient()
	defer client.CloseIdleConnections()
	return discoverIPv4(ctx, client)
}

func newDiscoveryClient() *http.Client {
	dialer := &net.Dialer{Timeout: requestTimeout, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp4", address)
			},
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: requestTimeout,
		},
		// Echo providers must answer directly; never follow a redirect, including
		// an HTTPS-to-HTTP downgrade, to discover an address.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func discoverIPv4(ctx context.Context, client *http.Client) (netip.Addr, error) {
	var failures []error
	for _, provider := range [...]string{"https://ifconfig.me", "https://icanhazip.com", "https://ifconfig.co"} {
		if err := ctx.Err(); err != nil {
			return netip.Addr{}, fmt.Errorf("discovering public IPv4 address: %w", err)
		}
		address, err := queryIPv4(ctx, client, provider)
		if err == nil {
			return address, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", provider, err))
	}
	return netip.Addr{}, fmt.Errorf("discovering public IPv4 address: %w", errors.Join(failures...))
}

func queryIPv4(ctx context.Context, client *http.Client, provider string) (netip.Addr, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider, nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating echo request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("requesting echo response: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading echo response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return netip.Addr{}, fmt.Errorf("echo response exceeds %d bytes", maxResponseBytes)
	}
	address, err := ParseIPv4(strings.TrimSpace(string(body)))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parsing echo response: %w", err)
	}
	return address, nil
}
