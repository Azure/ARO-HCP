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

// Package cpoutofband identifies control-plane pin targets that are "out of
// band" with respect to Cluster Service (CS): versions CS does not recognize,
// which are applied to the HostedCluster directly (around CS) and served from an
// internally-known registry repository.
//
// When a cluster's resolved Spec.ControlPlaneVersion.DesiredVersion is an
// out-of-band control-plane version, the backend:
//   - sets the HostedCluster spec.controlPlaneRelease.image to the version's
//     digest-pinned release image reference (<name>@sha256:<digest>,
//     maintained in code in the allowlist below), a control-plane-only payload
//     override HyperShift does not version-check, while leaving
//     spec.release.image at the release image Cluster Service selected for the
//     version it was told (the channel's best exact version), so the data plane
//     follows the version the cluster would run without the pin, and
//   - must NOT send that version to Cluster Service (CS would reject it as
//     unknown); a recognized substitute version is sent instead — the channel's
//     best exact version, or, for a nightly cluster, the exact nightly build
//     set on the cluster (see rollout.RecognizedVersionForOutOfBandPin).
//
// Everything is keyed on IsOutOfBandControlPlaneVersion(resolvedDesiredVersion):
// when a pin auto-releases (DesiredVersion flips to the recognized fleet best),
// the predicate returns false and all paths revert to normal truthful Cluster
// Service behavior with no extra handling.
//
// The allowlist is static, code-defined configuration: the binary initializes
// it at startup from defaultOutOfBandControlPlaneReleaseImages below; there is
// no runtime (flag/config) override. Each allowlisted version is associated in
// code with the full digest-pinned reference of its release image, so the
// payload served for a pin is immutable.
package cpoutofband

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/blang/semver/v4"
)

// ocpReleaseImagesPrivRepository is the repository that serves out-of-band ocp
// control-plane release images.
const ocpReleaseImagesPrivRepository = "quay.io/openshift-release-dev/ocp-release-priv"

// defaultOutOfBandControlPlaneReleaseImages is the code-defined list of
// out-of-band control-plane versions recognized, associating each
// version (a canonical semver string, matched against
// semver.Version.String() output) with the full digest-pinned reference of
// its release image in the format: <registry_url>/<repository_path>@sha256:<digest>, which the
// backend writes to the HostedCluster spec.controlPlaneRelease.image while
// the version is pinned. Add entries here; there is no runtime configuration
// for this mapping. References are validated when the allowlist is built at
// process startup: an entry that is not digest-pinned, or that carries a tag,
// fails the binary immediately rather than producing an unusable image
// reference at reconcile time.
var defaultOutOfBandControlPlaneReleaseImages = map[string]string{
	"4.20.48-assembly.art2026112601": fmt.Sprintf("%s@sha256:fac7d45d108ab262c6154366958bfbce19fa649a9ba67135b2f3d9d468bc289b", ocpReleaseImagesPrivRepository),
	"4.21.44-assembly.art2026112601": fmt.Sprintf("%s@sha256:602d24fc2a8ef87352fc41451494154f9aa181b63d4021453e120f0c0115c788", ocpReleaseImagesPrivRepository),
	"4.22.24-assembly.art2026112601": fmt.Sprintf("%s@sha256:4dc7804bbe229fd61ba2ba9a48e5e357388dbd677b85cafd1b8c338ad7105e4c", ocpReleaseImagesPrivRepository),
	"5.0.3-assembly.art2026112601":   fmt.Sprintf("%s@sha256:dbc142f82958cf166067e380dad93d410313c54bb266cbdeeae03b1e6c4d9122", ocpReleaseImagesPrivRepository),
}

var (
	mu                                 sync.RWMutex
	outOfBandControlPlaneReleaseImages = newOCPControlPlaneReleaseImagesMap(defaultOutOfBandControlPlaneReleaseImages)
)

// newOCPControlPlaneReleaseImagesMap copies a version-to-image allowlist. It panics on a key
// that is not a canonical semver version, the empty string included, or on an image reference that is not digest-pinned
// (<registry_url>/<repository_path>@sha256:<digest>. The version-to-release-image
// association is code-defined static data, so an invalid entry is a programming
// error that must surface at startup, never at reconcile time.
func newOCPControlPlaneReleaseImagesMap(versions map[string]string) map[string]string {
	next := make(map[string]string, len(versions))
	for v, image := range versions {
		if parsed, err := semver.Parse(v); err != nil || parsed.String() != v {
			panic(fmt.Sprintf("cpoutofband: out-of-band control-plane version %q is not a canonical semver version", v))
		}
		if !isDigestPinnedImageReference(image) {
			panic(fmt.Sprintf("cpoutofband: out-of-band control-plane version %q has no digest-pinned release image format (got %q)", v, image))
		}
		next[v] = image
	}
	return next
}

// ConfigureControlPlaneVersions replaces the allowlist of out-of-band
// control-plane versions and their release-image references. It is intended for tests.
// non test code uses the code-defined defaultOutOfBandControlPlaneReleaseImages. It panics on a key that is not a
// canonical semver version or on a reference that is not digest-pinned, like
// the startup-time validation of defaultOutOfBandControlPlaneReleaseImages.
func ConfigureControlPlaneVersions(versions map[string]string) {
	mu.Lock()
	defer mu.Unlock()
	outOfBandControlPlaneReleaseImages = newOCPControlPlaneReleaseImagesMap(versions)
}

// IsOutOfBandControlPlaneVersion reports whether version (a canonical semver
// string, e.g. the result of semver.Version.String()) is in the configured
// out-of-band control-plane allowlist.
func IsOutOfBandControlPlaneVersion(version string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := outOfBandControlPlaneReleaseImages[version]
	return ok
}

// OutOfBandControlPlaneReleaseImage returns the HostedCluster
// spec.controlPlaneRelease.image reference for an out-of-band control-plane
// version: the full digest-pinned release image reference associated with the
// version in the allowlist, and whether the version is in the allowlist at
// all. A caller that gets ok=false must treat the version as an ordinary
// (in-band) one: no release image is associated with it, so there is nothing
// to pin. Checking and fetching happen under a single lock, so the answer is
// internally consistent even while the allowlist is being replaced (only the
// test hook replaces it; in production it is static after startup), and it
// doubles as an atomic gate: callers need not, and should not, call
// IsOutOfBandControlPlaneVersion first.
func OutOfBandControlPlaneReleaseImage(version string) (string, bool) {
	mu.RLock()
	defer mu.RUnlock()
	image, ok := outOfBandControlPlaneReleaseImages[version]
	if !ok {
		return "", false
	}
	return image, true
}

// isDigestPinnedImageReference reports whether image is a container image
// reference pinned by sha256 digest: <name>@sha256:<64-character hex digest>,
// with the digest as the reference's tail. A tag is rejected even though the
// reference grammar allows <name>:<tag>@sha256:<digest>: at pull time the
// digest is authoritative and the tag is ignored, so a tag would only be able
// to silently drift from the digest and misrepresent the payload the
// reference serves. The only colon tolerated in the name is a registry port
// (<host>:<port>/<repository>), which precedes the last slash.
func isDigestPinnedImageReference(image string) bool {
	at := strings.LastIndex(image, "@sha256:")
	if at <= 0 {
		return false
	}
	name := image[:at]
	if strings.Contains(name[strings.LastIndex(name, "/")+1:], ":") {
		return false
	}
	return isValidSHA256Digest(image[at+len("@sha256:"):])
}

// isValidSHA256Digest reports whether digest is a 64-character hex string.
func isValidSHA256Digest(digest string) bool {
	if len(digest) != hex.EncodedLen(sha256.Size) {
		return false
	}
	for _, c := range digest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
