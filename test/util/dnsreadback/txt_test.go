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
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

type testTXTResolver struct {
	lookupNS  func(context.Context, string) ([]*net.NS, error)
	lookupTXT func(context.Context, string) ([]string, error)
}

func (resolver testTXTResolver) LookupNS(ctx context.Context, name string) ([]*net.NS, error) {
	return resolver.lookupNS(ctx, name)
}

func (resolver testTXTResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return resolver.lookupTXT(ctx, name)
}

func TestVerifyTXTUsesConfiguredResolver(t *testing.T) {
	// No network access is needed: all NS and TXT queries must use this resolver,
	// even when direct access to public recursive/authoritative DNS is blocked.
	expectedNS := []string{"NS2.EXAMPLE.COM", "ns1.example.com."}
	resolver := testTXTResolver{
		lookupNS: func(ctx context.Context, name string) ([]*net.NS, error) {
			require.Equal(t, "cluster.example.com.", name)
			require.Equal(t, t.Context(), ctx)
			return []*net.NS{{Host: "ns1.example.com."}, {Host: "ns2.example.com."}}, nil
		},
		lookupTXT: func(ctx context.Context, name string) ([]string, error) {
			require.Equal(t, "apps.cluster.example.com.", name)
			require.Equal(t, t.Context(), ctx)
			return []string{"unrelated-existing-value", "validation-token"}, nil
		},
	}
	err := VerifyTXT(t.Context(), resolver, "cluster.example.com", expectedNS, "apps.cluster.example.com", "validation-token")
	require.NoError(t, err)
	require.Equal(t, []string{"NS2.EXAMPLE.COM", "ns1.example.com."}, expectedNS, "checking delegation must not mutate expected nameservers")
}

func TestVerifyTXTRejectsUnpropagatedRecords(t *testing.T) {
	for _, scenario := range []string{"wrong delegation", "missing TXT", "missing token"} {
		t.Run(scenario, func(t *testing.T) {
			resolver := testTXTResolver{
				lookupNS: func(context.Context, string) ([]*net.NS, error) {
					if scenario == "wrong delegation" {
						return []*net.NS{{Host: "old.example.com."}}, nil
					}
					return []*net.NS{{Host: "ns.example.com."}}, nil
				},
				lookupTXT: func(context.Context, string) ([]string, error) {
					require.NotEqual(t, "wrong delegation", scenario, "must check delegation before TXT")
					if scenario == "missing TXT" {
						return nil, nil
					}
					return []string{"other-token"}, nil
				},
			}
			err := VerifyTXT(t.Context(), resolver, "example.com", []string{"ns.example.com"}, "apps.example.com", "validation-token")
			require.ErrorIs(t, err, ErrInconsistent)
		})
	}
}

func TestVerifyTXTPreservesResolverErrors(t *testing.T) {
	for _, lookup := range []string{"NS", "TXT"} {
		t.Run(lookup, func(t *testing.T) {
			want := &net.DNSError{Err: "configured resolver unavailable", IsTimeout: true}
			resolver := testTXTResolver{
				lookupNS: func(context.Context, string) ([]*net.NS, error) {
					if lookup == "NS" {
						return nil, want
					}
					return []*net.NS{{Host: "ns.example.com."}}, nil
				},
				lookupTXT: func(context.Context, string) ([]string, error) {
					return nil, want
				},
			}
			err := VerifyTXT(t.Context(), resolver, "example.com", []string{"ns.example.com"}, "apps.example.com", "validation-token")
			require.ErrorIs(t, err, want)
			require.ErrorContains(t, err, lookup+" lookup")
			require.ErrorContains(t, err, "configured resolver")
			require.False(t, errors.Is(err, ErrInconsistent), "transport failures must remain distinct from incorrect DNS records")
		})
	}
}
