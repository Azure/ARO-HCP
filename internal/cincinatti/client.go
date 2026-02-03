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
	"net/http"
	"net/url"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
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

// aroHCPRegistry implements ConditionRegistry with standard condition types registered.
// This prevents pruning of Always and PromQL conditions, ensuring ARO-HCP clusters
// have access to all conditional updates.
//
// TODO(https://issues.redhat.com/browse/ARO-21547): Implement actual PromQL condition evaluation for ARO-HCP clusters.
// Currently, all conditions are accepted without evaluation. Once ARO-21547 is complete,
// PromQL conditions should be evaluated against the actual cluster state to determine
// if conditional updates apply to ARO-HCP.
type aroHCPRegistry struct {
	registry clusterconditions.ConditionRegistry
}

// newAROHCPRegistry creates a condition registry that accepts all conditional updates.
// It registers the "Always" condition type and overrides PruneInvalid to prevent
// pruning of unrecognized condition types (like PromQL).
//
// TODO(https://issues.redhat.com/browse/ARO-21547): Wire up PromQL evaluation logic to check actual cluster conditions.
func newAROHCPRegistry() *aroHCPRegistry {
	registry := clusterconditions.NewConditionRegistry()

	// Register standard condition types so they don't get pruned
	registry.Register("Always", &always.Always{})

	return &aroHCPRegistry{registry: registry}
}

// PruneInvalid returns all rules unchanged - NO PRUNING.
// This ensures ARO-HCP clusters can access all conditional updates, including
// those with PromQL conditions that the default registry would prune.
//
// NOTE: This is a temporary approach. Once ARO-21547 is implemented, PromQL
// conditions should be properly evaluated instead of blindly accepting all updates.
func (r *aroHCPRegistry) PruneInvalid(ctx context.Context, rules []configv1.ClusterCondition) ([]configv1.ClusterCondition, error) {
	// Return ALL rules unchanged - NO PRUNING!
	// TODO(https://issues.redhat.com/browse/ARO-21547): Evaluate conditions and prune those that don't apply to ARO-HCP
	return rules, nil
}

// Match evaluates conditions using the underlying registry.
//
// TODO(https://issues.redhat.com/browse/ARO-21547): Enhance this to properly evaluate ARO-HCP specific conditions,
// especially PromQL queries that check for HyperShift platform and Azure infrastructure.
func (r *aroHCPRegistry) Match(ctx context.Context, conditions []configv1.ClusterCondition) (bool, error) {
	return r.registry.Match(ctx, conditions)
}

// Register registers a condition type with the underlying registry.
func (r *aroHCPRegistry) Register(conditionType string, condition clusterconditions.Condition) {
	r.registry.Register(conditionType, condition)
}

// client wraps the official OpenShift Cincinnati client to implement the Client interface.
type client struct {
	cvoClient cincinnati.Client
}

// GetUpdates delegates to the official CVO Cincinnati client.
func (c *client) GetUpdates(ctx context.Context, uri *url.URL, desiredArch, currentArch, channel string, version semver.Version) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate, error) {
	return c.cvoClient.GetUpdates(ctx, uri, desiredArch, currentArch, channel, version)
}

// NewCincinnatiClient creates a new ARO-HCP Cincinnati client that uses the official OpenShift
// Cincinnati client with a custom condition registry that does not prune conditional updates.
//
// This ensures ARO-HCP clusters have access to all conditional updates, including:
//   - HyperShift-related fixes (which apply to ARO-HCP)
//   - Updates with PromQL conditions that would otherwise be pruned
//   - Classic ARO updates (which won't match ARO-HCP's HyperShift architecture)
//
// The client uses http.DefaultTransport for HTTP communication.
//
// Parameters:
//   - clusterID: The cluster ID as set by the Cluster Service (CS). This is sent to Cincinnati
//     for tracking and will be used by PromQL condition evaluation once ARO-21547 is implemented.
//
// NOTE: Currently, all conditional updates are accepted without evaluating their conditions.
// This is intentional to ensure upgrade paths are available. See ARO-21547 for implementing
// proper condition evaluation: https://issues.redhat.com/browse/ARO-21547
//
// Returns:
//   - Client interface implementation wrapping the official CVO Cincinnati client
func NewCincinnatiClient(clusterID uuid.UUID) Client {
	// Create ARO-HCP registry that accepts all conditional updates (no pruning)
	registry := newAROHCPRegistry()
	userAgent := "ARO-HCP"
	transport := http.DefaultTransport.(*http.Transport)

	// Create official CVO Cincinnati client with our custom registry
	cvoClient := cincinnati.NewClient(clusterID, transport, userAgent, registry)

	// Return wrapper that implements our Client interface
	return &client{cvoClient: cvoClient}
}
