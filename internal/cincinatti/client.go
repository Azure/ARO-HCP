// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package cincinatti

//go:generate $MOCKGEN -typed -source=client.go -destination=mock_client.go -package cincinatti Client

import (
	"context"
	"net/url"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/always"
)

// Client is an interface for fetching cluster updates from the Cincinnati service.
// It wraps the official OpenShift Cincinnati client to provide update graph information.
type Client interface {
	// GetUpdates retrieves available cluster updates from the Cincinnati service.
	// It returns the current release information, a list of unconditionally recommended
	// updates, a list of conditionally recommended updates, and any error encountered.
	//
	// Parameters:
	//   - ctx: Context for the HTTP request, allowing cancellation
	//   - uri: Base URI of the Cincinnati service
	//   - desiredArch: Target architecture for updates (e.g., "amd64", "multi")
	//   - currentArch: Current cluster architecture
	//   - channel: Update channel name (e.g., "stable-4.19", "stable-4.20")
	//   - version: Current semantic version of the cluster
	//
	// Returns:
	//   - Current release metadata from the update graph
	//   - Slice of unconditionally recommended update releases (nil if none)
	//   - Slice of conditionally recommended updates with associated risks (nil if none)
	//   - Error if the request fails or the response is invalid
	GetUpdates(ctx context.Context, uri *url.URL, desiredArch, currentArch, channel string, version semver.Version) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate, error)
}

func NewAlwaysConditionRegistry() clusterconditions.ConditionRegistry {
	conditionRegistry := clusterconditions.NewConditionRegistry()
	conditionRegistry.Register("Always", &always.Always{})

	return conditionRegistry
}
