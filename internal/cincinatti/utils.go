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

package cincinatti

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

// GetCincinnatiURI returns the appropriate Cincinnati graph URI based on the channel group.
// For nightly channels, it returns the CI nightly graph.
// For all other channels (stable, fast, candidate, eus), it returns the production graph.
func GetCincinnatiURI(channelGroup string) (*url.URL, error) {
	if channelGroup == "nightly" {
		return url.Parse("https://multi.ocp.releases.ci.openshift.org/graph")
	}
	return url.Parse("https://api.openshift.com/api/upgrades_info/graph")
}

// ParseCincinnatiChannel parses a Cincinnati channel name (e.g., "stable-4.20") into its
// constituent parts: channel group (e.g., "stable") and minor version (e.g., "4.20").
// Returns an error if the channel format is invalid.
func ParseCincinnatiChannel(channel string) (string, string, error) {
	parts := strings.Split(channel, "-")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid cincinnati channel format: %s (expected format: 'channelGroup-minorVersion')", channel)
	}
	return parts[0], parts[1], nil
}

// ExcludeConditionalUpdatesWithAzureOrHyperShiftRisks filters out conditional updates that mention
// Azure, HyperShift, or Hosted Control Plane (HCP) in their risk messages. Since ARO-HCP runs on
// Azure with HyperShift and uses Hosted Control Planes, these platform-specific risks apply to
// ARO-HCP clusters and should be excluded.
//
// NOTE: This is a temporary workaround. Once ARO-21547 is implemented, proper PromQL condition
// evaluation will be performed, and this keyword-based risk filtering can be removed.
// See: https://issues.redhat.com/browse/ARO-21547
func ExcludeConditionalUpdatesWithAzureOrHyperShiftRisks(conditionalUpdates []configv1.ConditionalUpdate) []configv1.ConditionalUpdate {
	filtered := make([]configv1.ConditionalUpdate, 0, len(conditionalUpdates))
	for _, condUpdate := range conditionalUpdates {
		if !hasAroHcpPlatformRisk(condUpdate.Risks) {
			filtered = append(filtered, condUpdate)
		}
	}
	return filtered
}

// hasAroHcpPlatformRisk checks if any risk message contains ARO-HCP platform-specific keywords.
func hasAroHcpPlatformRisk(risks []configv1.ConditionalUpdateRisk) bool {
	platformKeywords := []string{"azure", "hypershift", "hcp", "hosted control plane"}

	for _, risk := range risks {
		message := strings.ToLower(risk.Message)
		for _, keyword := range platformKeywords {
			if strings.Contains(message, keyword) {
				return true
			}
		}
	}
	return false
}

// IsCincinnatiVersionNotFoundError checks if an error from Cincinnati is specifically a "VersionNotFound" error.
// This error indicates that the queried version does not exist in the Cincinnati update graph.
func IsCincinnatiVersionNotFoundError(err error) bool {
	var cincinnatiErr *cincinnati.Error
	return errors.As(err, &cincinnatiErr) && cincinnatiErr.Reason == "VersionNotFound"
}
