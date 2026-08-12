// Copyright 2025 Microsoft Corporation
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
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"

	"github.com/blang/semver/v4"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

type RBACScope string

const (
	RBACScopeResourceGroup RBACScope = "resourceGroup"
	RBACScopeResource      RBACScope = "resource"

	// Default OpenShift channel group and version for the E2E tests
	DefaultOCPChannelGroup         = "candidate"
	DefaultOCPVersionId            = "4.21"
	DefaultOCPNodePoolChannelGroup = "candidate"

	DefaultPodCIDR      = "10.128.0.0/14"
	DefaultServiceCIDR  = "172.30.0.0/16"
	DefaultK8sServiceIP = "172.30.0.1"
)

type NetworkConfig struct {
	NetworkType string
	PodCIDR     string
	ServiceCIDR string
	MachineCIDR string
	HostPrefix  int32
}

var (
	defaultCPVersion     string
	defaultCPVersionErr  error
	defaultCPVersionOnce sync.Once
)

// resolveDefaultControlPlaneVersion may return a bare major.minor (stable channel, which the RP
// resolves) or a concrete install version. Its environment-variable handling mirrors the main
// branch:
//   - ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION, when set, is the version. When
//     ARO_HCP_OPENSHIFT_LATEST_Z_STREAM is truthy its major.minor is resolved to the latest z-stream
//     in the active channel group; otherwise it is used verbatim.
//   - Otherwise the version is ARO_HCP_OPENSHIFT_VERSION_ID (or the DefaultOCPVersionId fallback),
//     resolved to a concrete install version for every channel group except stable (which the RP
//     resolves from a bare major.minor).
func resolveDefaultControlPlaneVersion() (string, error) {
	defaultCPVersionOnce.Do(func() {
		controlPlaneVersion := os.Getenv("ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION")
		if len(controlPlaneVersion) == 0 {
			controlPlaneVersion = os.Getenv("ARO_HCP_OPENSHIFT_VERSION_ID")
		}
		if len(controlPlaneVersion) == 0 {
			controlPlaneVersion = DefaultOCPVersionId
		}

		wantsLatestZStreamOfMinor, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("ARO_HCP_OPENSHIFT_LATEST_Z_STREAM")))
		channelGroup := DefaultOpenshiftChannelGroup()
		zStreamOffset := clusterversion.GetZStreamOffset(channelGroup)
		if wantsLatestZStreamOfMinor {
			zStreamOffset = 0
		}

		resultingControlPlaneMinorVersion := ""
		parsedControlPlaneVersion, err := semver.Parse(controlPlaneVersion)
		switch {
		case err == nil && wantsLatestZStreamOfMinor:
			// if someone specified the exact control_plane version but actually wants the latest z-stream.  They are logically bugged.
			defaultCPVersionErr = fmt.Errorf("cannot specify an exact version and the latest z-stream: %q", controlPlaneVersion)
			return

		case err == nil && !wantsLatestZStreamOfMinor:
			// if someone specified the exact control_plane version
			defaultCPVersion = parsedControlPlaneVersion.String()
			return

		case err != nil:
			// this means it's probably a minor version specified, so use that
			parsedControlPlaneVersion, err = semver.ParseTolerant(controlPlaneVersion)
			if err != nil {
				defaultCPVersionErr = fmt.Errorf("parse ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION %q: %w", controlPlaneVersion, err)
				return
			}
			resultingControlPlaneMinorVersion = fmt.Sprintf("%d.%d", parsedControlPlaneVersion.Major, parsedControlPlaneVersion.Minor)
		}

		if channelGroup == "nightly" {
			defaultCPVersion, defaultCPVersionErr = GetLatestNightlyInstallVersion(context.Background(), channelGroup, resultingControlPlaneMinorVersion)
			return
		}

		release, err := controlplaneversion.SelectControlPlaneVersion(context.Background(), http.DefaultTransport.RoundTrip, nil, fmt.Sprintf("%s-%s", channelGroup, resultingControlPlaneMinorVersion), zStreamOffset)
		if err != nil {
			defaultCPVersionErr = fmt.Errorf("failed getting controlPlaneVersion: %w", err)
			return
		}
		if release == nil {
			defaultCPVersionErr = fmt.Errorf("no release found for controlPlaneVersion: %w", err)
			return
		}

		defaultCPVersion = resultingControlPlaneMinorVersion
	})
	return defaultCPVersion, defaultCPVersionErr
}

func DefaultOpenshiftControlPlaneVersionId() string {
	version, err := resolveDefaultControlPlaneVersion()
	if err != nil {
		if errors.Is(err, ErrNightlyReleaseStreamNotFound) || errors.Is(err, ErrNoAcceptedNightlyTags) || errors.Is(err, ErrNoParseableNightlyTags) {
			Skip(fmt.Sprintf("No install version found for %s in %s channel (%s)", DefaultOCPVersionId, DefaultOpenshiftChannelGroup(), err.Error()))
		} else {
			Fail(fmt.Sprintf("failed to get latest install version for %s channel: %s", DefaultOpenshiftChannelGroup(), err.Error()))
		}
	}
	return version
}

func DefaultOpenshiftChannelGroup() string {
	channelGroup := os.Getenv("ARO_HCP_OPENSHIFT_CHANNEL_GROUP")
	if len(channelGroup) == 0 {
		channelGroup = DefaultOCPChannelGroup
	}
	return channelGroup
}

func DefaultOpenshiftNodePoolVersionId() string {
	if version := os.Getenv("ARO_HCP_OPENSHIFT_NODEPOOL_VERSION"); len(version) != 0 {
		return version
	}

	channelGroup := DefaultOpenshiftNodePoolChannelGroup()
	cpChannelGroup := DefaultOpenshiftChannelGroup()
	cpVersion := DefaultOpenshiftControlPlaneVersionId()

	// A node pool's version.id must be a concrete Major.Minor.Patch. Unlike the control plane, the
	// RP does not resolve a bare major.minor for node pools. When the control plane already resolved
	// to a concrete version and the node pool shares its channel group, reuse it verbatim so the
	// node pool version equals (and never exceeds) the control plane.
	if channelGroup == cpChannelGroup {
		if _, err := semver.Parse(cpVersion); err == nil {
			return cpVersion
		}
	}

	// Otherwise resolve a concrete z-stream from the node pool's channel group. When the channel
	// groups match, resolve the control plane's major.minor line (it is a bare major.minor here)
	// with the same selector the RP uses for the control plane, so the node pool lands on the same
	// z-stream.
	minor := DefaultOCPVersionId
	if channelGroup == cpChannelGroup {
		if parsed, err := semver.ParseTolerant(cpVersion); err == nil {
			minor = fmt.Sprintf("%d.%d", parsed.Major, parsed.Minor)
		} else {
			minor = cpVersion
		}
	}

	var version string
	if channelGroup == "nightly" {
		// Nightly is not served by the update service graph API; use the release-stream API.
		resolved, err := GetLatestNightlyInstallVersion(context.Background(), channelGroup, minor)
		if err != nil {
			if errors.Is(err, ErrNightlyReleaseStreamNotFound) || errors.Is(err, ErrNoAcceptedNightlyTags) || errors.Is(err, ErrNoParseableNightlyTags) {
				Skip(fmt.Sprintf("No install version found for %s in %s channel (%s)", minor, channelGroup, err.Error()))
			} else {
				Fail(fmt.Sprintf("failed to get latest install version for %s channel: %s", channelGroup, err.Error()))
			}
		}
		return resolved
	}

	// Every other channel group selects the tip of the channel via the OpenShift update
	// service — the same selector the control plane version controller uses.
	release, err := controlplaneversion.SelectControlPlaneVersion(context.Background(), http.DefaultTransport.RoundTrip, nil, fmt.Sprintf("%s-%s", channelGroup, minor), clusterversion.GetZStreamOffset(channelGroup))
	if err != nil {
		Fail(fmt.Sprintf("failed to select node pool install version for %s-%s channel: %s", channelGroup, minor, err.Error()))
	}
	if release == nil {
		Fail(fmt.Sprintf("no node pool release resolved for channel %s-%s", channelGroup, minor))
	}
	version = release.Version

	return version
}

func DefaultOpenshiftNodePoolChannelGroup() string {
	channelGroup := os.Getenv("ARO_HCP_OPENSHIFT_NODEPOOL_CHANNEL_GROUP")
	if len(channelGroup) == 0 {
		channelGroup = DefaultOCPNodePoolChannelGroup
	}
	return channelGroup
}

// applyCPOImageOverride sets the CPO image override tag when the
// CPO_IMAGE_OVERRIDE environment variable is present. This is set by
// the aro-hcp-hypershift-images-push CI step to override the control
// plane operator image with one built from a HyperShift PR.
func applyCPOImageOverride(tags map[string]*string) {
	if cpoImage := os.Getenv("CPO_IMAGE_OVERRIDE"); cpoImage != "" {
		tags[metadataapi.TagClusterCPOImageOverride] = to.Ptr(cpoImage)
	}
}

// NodePoolAutoScalingParams contains min/max node counts for nodepool autoscaling
type NodePoolAutoScalingParams struct {
	Min int32
	Max int32
}
