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

package rollout

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// A response that sends headers but never finishes its body must be bounded too.
type stalledGraphBody struct {
	ctx context.Context
}

func TestCincinnatiSelectorProfiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		profile coreapi.VersionProfile
		want    *semver.Version
		wantErr string
	}{
		{"stable offset", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"}, v("4.21.3"), ""},
		{"fast tip", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "fast"}, v("4.21.5"), ""},
		{"candidate tip", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "candidate"}, v("4.21.5"), ""},
		{"nightly has no graph", coreapi.VersionProfile{ID: "4.21", ChannelGroup: "nightly"}, nil, ""},
		{"missing profile", coreapi.VersionProfile{}, nil, "unsupported channel group"},
		{"exact profile", coreapi.VersionProfile{ID: "4.21.3", ChannelGroup: "stable"}, nil, "canonical major.minor"},
		{"invalid version", coreapi.VersionProfile{ID: "invalid", ChannelGroup: "stable"}, nil, "invalid version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			selector := cincinnatiBestVersionSelector{roundTrip: func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, tc.profile.ChannelGroup+"-4.21", req.URL.Query().Get("channel"))
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"nodes":[{"version":"4.21.1","payload":"image:1"},{"version":"4.21.5","payload":"image:5"},{"version":"4.21.3","payload":"image:3"}]}`))}, nil
			}}
			got, err := selector.BestExactVersionForProfile(t.Context(), tc.profile)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, cmp.Diff(tc.want, got))
			if tc.want == nil {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func (b stalledGraphBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (stalledGraphBody) Close() error { return nil }

func TestCincinnatiSelectorRequestDeadline(t *testing.T) {
	for _, stage := range []string{"headers", "body"} {
		for _, parentTimeout := range []time.Duration{0, 5 * time.Second} {
			t.Run(stage+"/parent="+parentTimeout.String(), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx := t.Context()
					expectedTimeout := 30 * time.Second
					if parentTimeout != 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, parentTimeout)
						defer cancel()
						expectedTimeout = parentTimeout
					}
					start := time.Now()
					calls := 0
					selector := cincinnatiBestVersionSelector{
						roundTrip: func(req *http.Request) (*http.Response, error) {
							calls++
							deadline, ok := req.Context().Deadline()
							if !ok || !deadline.Equal(start.Add(expectedTimeout)) {
								t.Fatalf("request deadline = %v (present: %t), want %v", deadline, ok, start.Add(expectedTimeout))
							}
							if stage == "headers" {
								<-req.Context().Done()
								return nil, req.Context().Err()
							}
							return &http.Response{
								StatusCode: http.StatusOK,
								Body:       stalledGraphBody{ctx: req.Context()},
							}, nil
						},
					}
					version, err := selector.BestExactVersionForProfile(ctx, coreapi.VersionProfile{ID: "4.21", ChannelGroup: "stable"})
					if !errors.Is(err, context.DeadlineExceeded) || version != nil {
						t.Fatalf("selection = %v, %v; want nil version and deadline exceeded", version, err)
					}
					if elapsed := time.Since(start); elapsed != expectedTimeout {
						t.Fatalf("selection took %v, want %v", elapsed, expectedTimeout)
					}
					if calls != 1 {
						t.Fatalf("made %d requests, want no retry after deadline", calls)
					}
				})
			})
		}
	}
}
