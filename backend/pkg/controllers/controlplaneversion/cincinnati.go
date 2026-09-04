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

package controlplaneversion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"syscall"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
)

// RoundTrip allows customizing the RoundTripper (e.g. to support
// testing or proxy/TLS configuration) without having to create a local
// struct supplying the RoundTripper interface.  The interface only
// requires a RoundTrip function, and it is easier to just pass the
// function around.  https://github.com/golang/go/issues/38479 is
// proposing the type become a stdlib declaration, but until then,
// declare it locally.
type RoundTrip func(*http.Request) (*http.Response, error)

// node represents a single cluster version in the update graph.
type node struct {
	// Version is the semantic version of this release.
	Version string `json:"version"`

	// Payload is the release image pullspec (payload) for this version.
	Payload string `json:"payload"`
}

// graph represents the update graph structure returned by the Cincinnati service.
// It defines all available cluster versions and the valid upgrade paths between them.
type graph struct {
	// Nodes contains all cluster version releases available in this channel.
	Nodes []node `json:"nodes"`
}

var defaultUpstreamUpdateService = "https://api.openshift.com/api/upgrades_info/graph"

// cincinnatiRetryBackoff configures the retry-with-backoff behavior of
// doWithRetry when fetching the Cincinnati update graph in production. It
// is a package-level var (rather than a hard-coded literal inside
// doWithRetry) purely for readability; tests exercise the retry/no-retry
// behavior by passing their own fast backoff directly to doWithRetry
// instead of overriding this var, to stay deterministic and avoid shared
// mutable state.
var cincinnatiRetryBackoff = wait.Backoff{
	Duration: 1 * time.Second,
	Factor:   2,
	Steps:    5,
	Cap:      30 * time.Second,
}

// roundTrip converts a RoundTrip function into a RoundTripper.
type roundTrip struct {
	roundTripper RoundTrip
}

// RoundTrip executes a single HTTP transaction, returning a Response
// for the provided Request.
func (rt *roundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return rt.roundTripper(request)
}

// cincinnati calls an update-service to retrieve a list of releases,
// and the update recommendation graph connecting them.
func cincinnati(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, userAgent string, channel string) ([]configv1.Release, *url.URL, error) {
	if updateService == nil {
		var err error
		updateService, err = url.Parse(defaultUpstreamUpdateService)
		if err != nil {
			return nil, nil, err
		}
	} else { // copy to avoid mutating function arguments
		u := *updateService
		updateService = &u
	}

	queryParams := updateService.Query()
	queryParams.Set("arch", "multi")
	queryParams.Set("channel", channel)
	updateService.RawQuery = queryParams.Encode()
	req, err := http.NewRequest("GET", updateService.String(), nil)
	if err != nil {
		return nil, updateService, err
	}
	req.Header.Set("Accept", "application/vnd.redhat.cincinnati.v1+json")
	if len(userAgent) > 0 {
		req.Header.Set("User-Agent", userAgent)
	}
	client := &http.Client{}
	if roundTripper != nil {
		client.Transport = &roundTrip{roundTripper: roundTripper}
	}

	body, err := doWithRetry(ctx, client, req, cincinnatiRetryBackoff)
	if err != nil {
		return nil, updateService, err
	}
	var graph graph
	err = json.Unmarshal(body, &graph)
	if err != nil {
		return nil, updateService, err
	}

	releases, err := nodesToReleases(graph.Nodes)
	return releases, updateService, err
}

// doWithRetry issues req via client, retrying transient failures (DNS lookup
// failures, connection timeouts, and 5xx responses) with exponential
// backoff. It returns the response body on a successful 200, or an error if
// every attempt failed. Non-transient failures (e.g. 4xx responses) are
// returned immediately without retrying.
func doWithRetry(ctx context.Context, client *http.Client, req *http.Request, backoff wait.Backoff) ([]byte, error) {
	var body []byte
	var lastErr error
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		resp, err := client.Do(req.Clone(ctx))
		if err != nil {
			if ctx.Err() != nil {
				// The context was cancelled or timed out; stop retrying and
				// surface that immediately instead of the generic transport
				// error.
				return false, ctx.Err()
			}
			if !isTransientTransportError(err) {
				// Misconfiguration-style errors (bad URL, unsupported
				// protocol scheme, TLS cert errors, etc.) will not clear up
				// on retry; fail fast instead of masking them.
				return false, err
			}
			// Treat known-transient transport errors (DNS lookup failures,
			// dial timeouts, connection resets, etc.) as retryable,
			// remembering the error in case every attempt fails.
			lastErr = err
			return false, nil
		}
		defer func() {
			// Drain the body before closing so the underlying connection
			// can be reused by later retry attempts.
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()

		if resp.StatusCode >= http.StatusInternalServerError {
			// Server-side errors are typically transient; retry.
			lastErr = fmt.Errorf("%s returned unexpected HTTP status %d", req.URL, resp.StatusCode)
			return false, nil
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Errorf("%s returned unexpected HTTP status %d", req.URL, resp.StatusCode)
		}

		body, err = io.ReadAll(resp.Body)
		if err != nil {
			lastErr = err
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// The caller's context was cancelled or timed out; surface that
			// directly rather than a generic retries-exhausted error.
			return nil, err
		}
		if wait.Interrupted(err) {
			if lastErr != nil {
				return nil, fmt.Errorf("%s did not respond successfully after retries: %w", req.URL, lastErr)
			}
			return nil, fmt.Errorf("%s did not respond successfully after retries", req.URL)
		}
		return nil, err
	}
	return body, nil
}

// isTransientTransportError reports whether err, returned from
// client.Do, is likely to clear up on its own (DNS resolution failures,
// dial timeouts, connection resets) as opposed to a persistent
// misconfiguration (unsupported URL scheme, TLS certificate errors,
// malformed requests) that will fail identically on every retry.
func isTransientTransportError(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// Permanent lookup failures (e.g. NXDOMAIN / "no such host") will
		// not clear up on retry; only timeouts and other temporary
		// resolver failures are worth retrying.
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return false
}

// nodesToReleases converts a slice of update-service nodes to a slice
// of Releases, sorted in descending SemVer order.
func nodesToReleases(nodes []node) ([]configv1.Release, error) {
	releases := make([]configv1.Release, 0, len(nodes))
	for _, node := range nodes {
		releases = append(releases, configv1.Release{
			Version: node.Version,
			Image:   node.Payload,
		})
	}

	slices.SortFunc(releases, func(a, b configv1.Release) int {
		vA := semver.MustParse(a.Version)
		vB := semver.MustParse(b.Version)
		return -vA.Compare(vB)
	})

	return releases, nil
}
