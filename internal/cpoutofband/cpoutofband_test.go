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

package cpoutofband

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDigest is a valid 64-character hex digest for tests.
const testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// testImage is a digest-pinned release image reference for tests.
const testImage = ocpReleaseImagesPrivRepository + "@sha256:" + testDigest

func TestIsOutOfBandControlPlaneVersion(t *testing.T) {
	ConfigureControlPlaneVersions(map[string]string{"4.19.99": testImage, "4.20.99": testImage})
	t.Cleanup(func() { ConfigureControlPlaneVersions(nil) })

	for _, tc := range []struct {
		name    string
		version string
		want    bool
	}{
		{name: "configured out-of-band version", version: "4.19.99", want: true},
		{name: "another configured out-of-band version", version: "4.20.99", want: true},
		{name: "same patch but different minor is not matched (exact match)", version: "4.21.99", want: false},
		{name: "ordinary recognized version", version: "4.19.15", want: false},
		{name: "empty string", version: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsOutOfBandControlPlaneVersion(tc.version))
		})
	}
}

func TestConfigureControlPlaneVersionsReplaces(t *testing.T) {
	t.Cleanup(func() { ConfigureControlPlaneVersions(nil) })

	ConfigureControlPlaneVersions(map[string]string{"4.19.99": testImage, "4.20.99": testImage})
	assert.True(t, IsOutOfBandControlPlaneVersion("4.19.99"))
	assert.True(t, IsOutOfBandControlPlaneVersion("4.20.99"))
	assert.False(t, IsOutOfBandControlPlaneVersion(""), "the empty string is never an allowlisted version")

	// Reconfiguring fully replaces the previous set.
	ConfigureControlPlaneVersions(map[string]string{"4.21.99": testImage})
	assert.True(t, IsOutOfBandControlPlaneVersion("4.21.99"))
	assert.False(t, IsOutOfBandControlPlaneVersion("4.19.99"), "previous allowlist must be replaced, not merged")

	// Empty configuration disables the feature.
	ConfigureControlPlaneVersions(nil)
	assert.False(t, IsOutOfBandControlPlaneVersion("4.21.99"))
}

func TestOutOfBandControlPlaneReleaseImage(t *testing.T) {
	t.Run("returns the associated digest-pinned reference", func(t *testing.T) {
		ConfigureControlPlaneVersions(map[string]string{"4.19.99": testImage})
		t.Cleanup(func() { ConfigureControlPlaneVersions(nil) })

		image, ok := OutOfBandControlPlaneReleaseImage("4.19.99")
		assert.True(t, ok, "configured version must be found")
		assert.Equal(t, testImage, image)
	})

	t.Run("version outside the allowlist returns ok=false and no image", func(t *testing.T) {
		ConfigureControlPlaneVersions(map[string]string{"4.19.99": testImage})
		t.Cleanup(func() { ConfigureControlPlaneVersions(nil) })

		image, ok := OutOfBandControlPlaneReleaseImage("4.19.15")
		assert.False(t, ok, "unconfigured version must not be found")
		assert.Empty(t, image, "no image reference is associated with an unconfigured version")
	})
}

func TestNonSemverVersionPanicsAtConfigurationTime(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
	}{
		{name: "empty version key", version: ""},
		{name: "bare minor without patch", version: "4.19"},
		{name: "v-prefixed version", version: "v4.19.99"},
		{name: "not a version at all", version: "nightly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Keys are matched against semver.Version.String() output, so a
			// non-canonical key could never match; it must fail at startup (or
			// here, at configuration time) instead of silently dead-listing the
			// entry.
			require.Panics(t, func() { ConfigureControlPlaneVersions(map[string]string{tc.version: testImage}) })
		})
	}
	t.Cleanup(func() { ConfigureControlPlaneVersions(nil) })
}

func TestIsDigestPinnedImageReferenceAcceptsRegistryPort(t *testing.T) {
	// A colon before the last slash is a registry port, not a tag; it must
	// not be rejected.
	assert.True(t, isDigestPinnedImageReference("localhost:5000/ocp-release-priv@sha256:"+testDigest))
}

func TestNonDigestPinnedImagePanicsAtConfigurationTime(t *testing.T) {
	for _, tc := range []struct {
		name  string
		image string
	}{
		{name: "missing image", image: ""},
		{name: "tag-only reference", image: ocpReleaseImagesPrivRepository + ":4.19.99"},
		{name: "tag alongside the digest", image: ocpReleaseImagesPrivRepository + ":4.19.99@sha256:" + testDigest},
		{name: "digest-only reference without a name", image: "@sha256:" + testDigest},
		{name: "malformed digest", image: ocpReleaseImagesPrivRepository + "@sha256:abcdef"},
		{name: "non-hex digest", image: ocpReleaseImagesPrivRepository + "@sha256:zz23456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The production default map is validated the same way at process
			// startup; a non-digest-pinned entry must fail there (or here, at
			// configuration time), never at reconcile time.
			require.Panics(t, func() { ConfigureControlPlaneVersions(map[string]string{"4.19.99": tc.image}) })
		})
	}
	t.Cleanup(func() { ConfigureControlPlaneVersions(nil) })
}
