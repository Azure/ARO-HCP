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

package dnsreadback

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
)

// TXTResolver supplies the delegation and token lookups for a DNS precheck.
type TXTResolver interface {
	LookupNS(context.Context, string) ([]*net.NS, error)
	LookupTXT(context.Context, string) ([]string, error)
}

// VerifyTXT checks delegation and a validation token through the supplied
// resolver, without requiring direct access to external DNS servers. The CA's
// subsequent domain-validation check establishes public visibility of the token.
func VerifyTXT(ctx context.Context, resolver TXTResolver, zone string, nameservers []string, name, value string) error {
	fqdn := func(name string) string { return strings.ToLower(strings.TrimSuffix(name, ".")) + "." }
	records, err := resolver.LookupNS(ctx, fqdn(zone))
	if err != nil {
		return fmt.Errorf("NS lookup for %s through configured resolver: %w", zone, err)
	}
	observed := make([]string, 0, len(records))
	for _, record := range records {
		observed = append(observed, fqdn(record.Host))
	}
	expected := make([]string, 0, len(nameservers))
	for _, nameserver := range nameservers {
		expected = append(expected, fqdn(nameserver))
	}
	slices.Sort(observed)
	slices.Sort(expected)
	if !slices.Equal(observed, expected) {
		return fmt.Errorf("%w: NS lookup for %s through configured resolver: got %v, want %v", ErrInconsistent, zone, observed, expected)
	}
	values, err := resolver.LookupTXT(ctx, fqdn(name))
	if err != nil {
		return fmt.Errorf("TXT lookup for %s through configured resolver: %w", name, err)
	}
	if !slices.Contains(values, value) {
		return fmt.Errorf("%w: TXT lookup for %s through configured resolver: expected challenge value absent (%d records)", ErrInconsistent, name, len(values))
	}
	return nil
}
