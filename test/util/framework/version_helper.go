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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/cincinnati"
)

var (
	ErrNightlyReleaseStreamNotFound = errors.New("nightly release stream not found")
	ErrNoAcceptedNightlyTags        = errors.New("no accepted nightly tags found")
	ErrNoParseableNightlyTags       = errors.New("no parseable nightly tags found")
)

const (
	versionFetchMaxRetries     = 3
	versionFetchRetryBaseDelay = 1 * time.Second
)

func retryOnTransientError[T any](ctx context.Context, f func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := range versionFetchMaxRetries + 1 {
		if attempt > 0 {
			backoff := versionFetchRetryBaseDelay * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(backoff):
			}
		}
		result, err := f()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isRetryableVersionError(err) {
			return zero, err
		}
	}
	return zero, fmt.Errorf("after %d attempts: %w", versionFetchMaxRetries+1, lastErr)
}

// IsVersionNotFoundError returns true if the error indicates a version was not found,
// whether from Cincinnati or the nightly release stream API.
func IsVersionNotFoundError(err error) bool {
	return cincinnati.IsCincinnatiVersionNotFoundError(err) ||
		errors.Is(err, ErrNightlyReleaseStreamNotFound) ||
		errors.Is(err, ErrNoAcceptedNightlyTags) ||
		errors.Is(err, ErrNoParseableNightlyTags)
}

func isRetryableVersionError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrNightlyReleaseStreamNotFound) ||
		errors.Is(err, ErrNoAcceptedNightlyTags) ||
		errors.Is(err, ErrNoParseableNightlyTags) {
		return false
	}
	if cincinnati.IsCincinnatiVersionNotFoundError(err) {
		return false
	}
	return true
}

// GetLatestNightlyInstallVersion returns the latest accepted nightly tag for the given minor version
// (for example "4.19" -> "4.19.0-0.nightly-multi-YYYY-MM-DD-HHMMSS"). It supports only the "nightly"
// channel group — other channel groups install with the bare major.minor line and let the RP resolve
// it — and returns an error if called for any other channel group. Transient HTTP/DNS errors are
// retried with exponential backoff.
func GetLatestNightlyInstallVersion(ctx context.Context, channelGroup string, version string) (string, error) {
	if channelGroup != "nightly" {
		return "", fmt.Errorf("GetLatestNightlyInstallVersion supports only the nightly channel group, got %q", channelGroup)
	}
	return retryOnTransientError(ctx, func() (string, error) {
		return getLatestInstallVersionForNightlyChannel(ctx, version)
	})
}

// getLatestInstallVersionForNightlyChannel returns the latest accepted nightly tag for the given minor version
// (for example "4.19" -> "4.19.0-0.nightly-multi-YYYY-MM-DD-HHMMSS").
func getLatestInstallVersionForNightlyChannel(ctx context.Context, version string) (string, error) {
	releaseStream := fmt.Sprintf("%s.0-0.nightly-multi", version)
	releaseTagsURL := fmt.Sprintf("https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/tags?phase=Accepted", url.PathEscape(releaseStream))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseTagsURL, nil)
	if err != nil {
		return "", fmt.Errorf("create nightly tags request for %s: %w", releaseStream, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("query nightly tags for %s: %w", releaseStream, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("%w for %s: %s", ErrNightlyReleaseStreamNotFound, releaseStream, strings.TrimSpace(string(body)))
		}
		return "", fmt.Errorf("query nightly tags for %s returned %s: %s", releaseStream, resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode nightly tags response for %s: %w", releaseStream, err)
	}
	if len(payload.Tags) == 0 {
		return "", fmt.Errorf("%w for %s", ErrNoAcceptedNightlyTags, releaseStream)
	}

	var (
		latestTagName string
		latestVersion semver.Version
		foundValid    bool
	)
	for _, tag := range payload.Tags {
		candidateVersion, err := semver.ParseTolerant(tag.Name)
		if err != nil {
			// Ignore tags that cannot be parsed as a semantic version.
			continue
		}
		if !foundValid || candidateVersion.GT(latestVersion) {
			latestTagName = tag.Name
			latestVersion = candidateVersion
			foundValid = true
		}
	}
	if !foundValid {
		return "", fmt.Errorf("%w for %s", ErrNoParseableNightlyTags, releaseStream)
	}

	return latestTagName, nil
}

// PickLatestOpenshiftVersionId returns whichever of defaultVersion or
// minimalVersion is more recent. If defaultVersion already satisfies the
// minimum it is returned unchanged. If it does not:
//   - For nightly builds, an error is returned (wrapped in ErrNoAcceptedNightlyTags
//     so callers can use IsVersionNotFoundError to skip the test). Nightly versions
//     cannot be bumped to a different minor version — there is no release stream to
//     fetch from for an arbitrary newer minor.
//   - For all other channel groups, minimalVersion is returned as the fallback.
//
// Nightly versions (e.g. "4.19.0-0.nightly-multi-2026-09-01-142156") are compared
// by Major.Minor only, not by full semver, because the pre-release suffix would
// otherwise rank them below the bare release (4.19.0) and produce false negatives.
func PickLatestOpenshiftVersionId(defaultVersion, minimalVersion string) (string, error) {
	defaultSemver, err := semver.ParseTolerant(defaultVersion)
	if err != nil {
		return "", fmt.Errorf("failed to parse default version %q: %w", defaultVersion, err)
	}
	minimalSemver, err := semver.ParseTolerant(minimalVersion)
	if err != nil {
		return "", fmt.Errorf("failed to parse minimal version %q: %w", minimalVersion, err)
	}

	if strings.Contains(defaultVersion, "nightly") {
		// Compare Major.Minor only: a 4.19 nightly satisfies a minimum of "4.19"
		// even though semver pre-release ordering would rank it below 4.19.0.
		defaultMajorMinorOK := defaultSemver.Major > minimalSemver.Major ||
			(defaultSemver.Major == minimalSemver.Major && defaultSemver.Minor >= minimalSemver.Minor)
		if defaultMajorMinorOK {
			return defaultVersion, nil
		}
		return "", fmt.Errorf("%w: nightly build %s does not satisfy minimum %s",
			ErrNoAcceptedNightlyTags, defaultVersion, minimalVersion)
	}

	if defaultSemver.GTE(minimalSemver) {
		return defaultVersion, nil
	}
	return minimalVersion, nil
}
