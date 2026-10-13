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

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
)

var (
	ErrNightlyReleaseStreamNotFound = errors.New("nightly release stream not found")
	ErrNoAcceptedNightlyTags        = errors.New("no accepted nightly tags found")
	ErrNoParseableNightlyTags       = errors.New("no parseable nightly tags found")
	ErrNightlyVersionTooOld         = errors.New("nightly version is too old")
)

const (
	versionFetchMaxRetries     = 3
	versionFetchRetryBaseDelay = 1 * time.Second

	// nightlyImageDateLayout is the timestamp suffix on nightly tags, e.g.
	// 4.19.0-0.nightly-multi-2026-09-01-142156.
	nightlyImageDateLayout = "2006-01-02-150405"
	nightlyImageDateMarker = "nightly-multi-"
	// the service will reject nightly images older than this
	maxNightlyImageAge = 30 * 24 * time.Hour
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

// IsIncompatibleNightlyVersionError returns true if the error indicates that
// the nightly version should skip the test: either it does not satisfy a
// minimum version requirement, or the latest accepted nightly image is older
// than maxNightlyImageAge.
func IsIncompatibleNightlyVersionError(err error) bool {
	return errors.Is(err, ErrNightlyVersionTooOld)
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
		errors.Is(err, ErrNoParseableNightlyTags) ||
		errors.Is(err, ErrNightlyVersionTooOld) {
		return false
	}
	if cincinnati.IsCincinnatiVersionNotFoundError(err) {
		return false
	}
	return true
}

// SelectControlPlaneVersion wraps controlplaneversion.SelectControlPlaneVersion with the same
// transient-error retry policy as GetLatestNightlyInstallVersion, so tests survive brief DNS or
// network hiccups when the test pod resolves api.openshift.com. Non-transient errors (context
// cancellation/deadline, cincinnati "version not found") are returned immediately.
func SelectControlPlaneVersion(ctx context.Context, roundTripper controlplaneversion.RoundTrip, updateService *url.URL, channel string, offset uint) (*configv1.Release, error) {
	return retryOnTransientError(ctx, func() (*configv1.Release, error) {
		return controlplaneversion.SelectControlPlaneVersion(ctx, roundTripper, updateService, channel, offset)
	})
}

// GetLatestNightlyInstallVersion returns the latest accepted nightly tag for the given minor version
// (for example "4.19" -> "4.19.0-0.nightly-multi-YYYY-MM-DD-HHMMSS"). It supports only the "nightly"
// channel group — other channel groups install with the bare major.minor line and let the RP resolve
// it — and returns an error if called for any other channel group. Transient HTTP/DNS errors are
// retried with exponential backoff. If the latest accepted tag is older than maxNightlyImageAge,
// it returns ErrNightlyVersionTooOld so callers Skip rather than Fail.
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

	if err := checkNightlyImageAge(latestTagName, time.Now().UTC()); err != nil {
		return "", err
	}

	return latestTagName, nil
}

// parseNightlyImageTime extracts the build timestamp from a nightly version
// such as 4.19.0-0.nightly-multi-YYYY-MM-DD-HHMMSS. The timestamp is UTC.
func parseNightlyImageTime(version string) (time.Time, error) {
	i := strings.LastIndex(version, nightlyImageDateMarker)
	if i < 0 {
		return time.Time{}, fmt.Errorf("nightly version %q does not contain %q date suffix", version, nightlyImageDateMarker)
	}
	datePart := version[i+len(nightlyImageDateMarker):]
	builtAt, err := time.Parse(nightlyImageDateLayout, datePart)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse nightly date %q from version %q: %w", datePart, version, err)
	}
	return builtAt, nil
}

// checkNightlyImageAge returns ErrNightlyVersionTooOld when the nightly image
// encoded in version is older than maxNightlyImageAge. A missing or unparseable
// date is returned as a plain error so callers Fail rather than Skip.
func checkNightlyImageAge(version string, now time.Time) error {
	builtAt, err := parseNightlyImageTime(version)
	if err != nil {
		return err
	}
	age := now.Sub(builtAt)
	if age > maxNightlyImageAge {
		return fmt.Errorf("%w: latest nightly %s was built at %s (%s ago, limit %s)",
			ErrNightlyVersionTooOld, version, builtAt.Format(time.RFC3339), age.Round(time.Hour), maxNightlyImageAge)
	}
	return nil
}

// PickAtLeastOpenshiftVersionId selects latest version based on a predefined
// default and given minimal version constraint arguments.
//   - If defaultVersion already satisfies the minimalVersion, the default is
//     returned unchanged.
//   - If it doesn't, the minimal version is returned instead, with one
//     exception: For nightly builds, an error is returned instead (since
//     nightly versions cannot be bumped to a different minor version).
//
// Nightly versions (e.g. "4.19.0-0.nightly-multi-2026-09-01-142156") are compared
// by Major.Minor only, not by full semver, because the pre-release suffix would
// otherwise rank them below the bare release (4.19.0) and produce false negatives.
func PickAtLeastOpenshiftVersionId(defaultVersion, minimalVersion string) (string, error) {
	defaultSemver, err := semver.ParseTolerant(defaultVersion)
	if err != nil {
		return "", fmt.Errorf("failed to parse default version %q: %w", defaultVersion, err)
	}
	minimalSemver, err := semver.ParseTolerant(minimalVersion)
	if err != nil {
		return "", fmt.Errorf("failed to parse minimal version %q: %w", minimalVersion, err)
	}

	if len(defaultSemver.Pre) > 0 {
		// For nightly builds, ignore the pre-release suffix and compare Major.Minor.Patch
		// directly. Bare semver ordering would rank "4.19.0-0.nightly-multi-..." below
		// "4.19.0", producing false negatives for patch-zero minimums. Nightly builds are
		// always patch 0 (X.Y.0-0.nightly-multi-...), so a patch-qualified minimum such
		// as "4.22.1" correctly rejects a 4.22.0 nightly.
		defaultAboveMin := defaultSemver.Major > minimalSemver.Major ||
			(defaultSemver.Major == minimalSemver.Major && defaultSemver.Minor > minimalSemver.Minor) ||
			(defaultSemver.Major == minimalSemver.Major && defaultSemver.Minor == minimalSemver.Minor && defaultSemver.Patch >= minimalSemver.Patch)
		if defaultAboveMin {
			return defaultVersion, nil
		}
		return "", fmt.Errorf("%w: nightly build %s does not satisfy minimum %s",
			ErrNightlyVersionTooOld, defaultVersion, minimalVersion)
	}

	if defaultSemver.GTE(minimalSemver) {
		return defaultVersion, nil
	}
	return minimalVersion, nil
}
