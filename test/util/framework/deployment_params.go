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
	"os"
	"sync"

	. "github.com/onsi/ginkgo/v2"

	"github.com/blang/semver/v4"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	"github.com/Azure/ARO-HCP/internal/api"
)

type RBACScope string

const (
	RBACScopeResourceGroup RBACScope = "resourceGroup"
	RBACScopeResource      RBACScope = "resource"

	// Default OpenShift channel group and version for the E2E tests
	DefaultOCPChannelGroup         = "candidate"
	DefaultOCPVersionId            = "4.20"
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

func resolveDefaultControlPlaneVersion() (string, error) {
	defaultCPVersionOnce.Do(func() {
		version := os.Getenv("ARO_HCP_OPENSHIFT_CONTROLPLANE_VERSION")
		if len(version) == 0 {
			version = DefaultOCPVersionId
			if v := os.Getenv("ARO_HCP_OPENSHIFT_VERSION_ID"); v != "" {
				version = v
			}
			channelGroup := DefaultOpenshiftChannelGroup()
			if channelGroup != "stable" {
				resolved, err := GetLatestInstallVersion(context.Background(), channelGroup, version)
				if err != nil {
					defaultCPVersionErr = err
					return
				}
				version = resolved
			}
		}
		defaultCPVersion = version
	})
	return defaultCPVersion, defaultCPVersionErr
}

func DefaultOpenshiftControlPlaneVersionId() string {
	version, err := resolveDefaultControlPlaneVersion()
	if err != nil {
		if errors.Is(err, ErrNightlyReleaseStreamNotFound) || errors.Is(err, ErrNoAcceptedNightlyTags) || errors.Is(err, ErrVersionNotFound) {
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
	version := os.Getenv("ARO_HCP_OPENSHIFT_NODEPOOL_VERSION")
	if len(version) == 0 {
		channelGroup := DefaultOpenshiftNodePoolChannelGroup()
		cpChannelGroup := DefaultOpenshiftChannelGroup()
		cpVersion := DefaultOpenshiftControlPlaneVersionId()

		// CRITICAL: When channel groups match, ALWAYS use the control plane version
		// to prevent version mismatches due to Cincinnati timing differences.
		// This ensures node pool version never exceeds control plane version.
		if channelGroup == cpChannelGroup {
			return cpVersion
		}

		// Different channel groups: resolve node pool version from its own channel,
		// then validate it doesn't exceed control plane version
		var err error
		version, err = GetLatestInstallVersion(context.Background(), channelGroup, DefaultOCPVersionId)
		if err != nil {
			if errors.Is(err, ErrNightlyReleaseStreamNotFound) || errors.Is(err, ErrNoAcceptedNightlyTags) || errors.Is(err, ErrVersionNotFound) {
				Skip(fmt.Sprintf("No install version found for %s in %s channel (%s)", DefaultOCPVersionId, channelGroup, err.Error()))
			} else {
				Fail(fmt.Sprintf("failed to get latest install version for %s channel: %s", channelGroup, err.Error()))
			}
		}

		// Validate: node pool version must not exceed control plane version
		npSemver, npErr := semver.Parse(version)
		cpSemver, cpErr := semver.Parse(cpVersion)

		if npErr == nil && cpErr == nil {
			if npSemver.GT(cpSemver) {
				// Node pool version exceeds control plane version - clamp it
				fmt.Fprintf(os.Stderr, "WARNING: Node pool version %s (from %s channel) exceeds control plane version %s (from %s channel). Clamping to control plane version.\n",
					version, channelGroup, cpVersion, cpChannelGroup)
				version = cpVersion
			}
		} else {
			// Couldn't parse versions for comparison - log warning but continue
			fmt.Fprintf(os.Stderr, "WARNING: Could not compare versions (np=%s, cp=%s). Proceeding with node pool version from %s channel.\n",
				version, cpVersion, channelGroup)
		}
	}
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
		tags[api.TagClusterCPOImageOverride] = to.Ptr(cpoImage)
	}
}

// NodePoolAutoScalingParams contains min/max node counts for nodepool autoscaling
type NodePoolAutoScalingParams struct {
	Min int32
	Max int32
}
