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

package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
)

// KustoCluster represents a discovered Kusto cluster from Azure Resource Graph.
type KustoCluster struct {
	Name     string
	Location string
	URI      string
	ID       string
	// Environment is the value of the cluster's scope tag (aroHCPEnvironment by
	// default), used to scope entity-group membership to a single environment. It
	// may be empty when the cluster has not yet been tagged (for example during
	// an in-progress tag rollout or while Resource Graph indexing lags behind
	// ARM).
	Environment string
}

const (
	defaultTagKey      = "aroHCPPurpose"
	defaultScopeTagKey = "aroHCPEnvironment"
)

// tagKeyPattern and tagValuePattern restrict caller-supplied tag selectors to a
// safe identifier charset before they are interpolated into the Resource Graph
// query. Parameterizing the tag keys/values is what lets other consumers (for
// example ARO Classic) reuse this tool, but it also means these values reach the
// query string, so validating them here closes that KQL-injection surface.
var (
	tagKeyPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	tagValuePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// KustoDiscoveryConfig selects which resource tags identify and scope the Kusto
// clusters to sync. It defaults to the ARO-HCP tags (aroHCPPurpose to mark a
// cluster, aroHCPEnvironment to scope it); other consumers can supply their own
// tag scheme so the tool stays product-agnostic.
type KustoDiscoveryConfig struct {
	// TagKey is the tag whose presence (or value, when TagValue is set) marks a
	// Kusto cluster as a sync target. Defaults to aroHCPPurpose. This mirrors the
	// TagKey/TagValue discovery convention already used by grafanactl and hcpctl.
	TagKey string
	// TagValue, when non-empty, requires the selection tag to equal this value
	// (case-insensitive). When empty, presence of the key is sufficient,
	// preserving the original cross-value behavior.
	TagValue string
	// ScopeTagKey is the tag projected for per-environment scoping. Defaults to
	// aroHCPEnvironment.
	ScopeTagKey string
}

// WithDefaults returns a copy with empty fields filled from the ARO-HCP defaults.
func (cfg KustoDiscoveryConfig) WithDefaults() KustoDiscoveryConfig {
	if cfg.TagKey == "" {
		cfg.TagKey = defaultTagKey
	}
	if cfg.ScopeTagKey == "" {
		cfg.ScopeTagKey = defaultScopeTagKey
	}
	return cfg
}

// Validate rejects tag selectors that fall outside the safe identifier charset,
// preventing KQL injection when they are interpolated into the discovery query.
func (cfg KustoDiscoveryConfig) Validate() error {
	if !tagKeyPattern.MatchString(cfg.TagKey) {
		return fmt.Errorf("invalid tag key %q; must match %s", cfg.TagKey, tagKeyPattern.String())
	}
	if !tagKeyPattern.MatchString(cfg.ScopeTagKey) {
		return fmt.Errorf("invalid scope tag key %q; must match %s", cfg.ScopeTagKey, tagKeyPattern.String())
	}
	if cfg.TagValue != "" && !tagValuePattern.MatchString(cfg.TagValue) {
		return fmt.Errorf("invalid tag value %q; must match %s", cfg.TagValue, tagValuePattern.String())
	}
	return nil
}

// ResourceGraphKustoDiscoveryClient discovers Kusto clusters using Azure Resource Graph.
type ResourceGraphKustoDiscoveryClient struct {
	client *armresourcegraph.Client
	cfg    KustoDiscoveryConfig
}

// NewResourceGraphKustoDiscoveryClient creates a new discovery client. The
// discovery config selects which resource tags identify and scope target
// clusters; empty fields fall back to the ARO-HCP defaults.
func NewResourceGraphKustoDiscoveryClient(cred azcore.TokenCredential, clientOptions *arm.ClientOptions, cfg KustoDiscoveryConfig) (*ResourceGraphKustoDiscoveryClient, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Kusto discovery config: %w", err)
	}

	client, err := armresourcegraph.NewClient(cred, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create Resource Graph client: %w", err)
	}

	return &ResourceGraphKustoDiscoveryClient{
		client: client,
		cfg:    cfg,
	}, nil
}

// kustoDiscoveryQuery builds the Azure Resource Graph query used to discover
// Kusto clusters. Every candidate must carry the configured selection tag (and
// match its value when TagValue is set) and be in a Succeeded
// provisioning state. The configured scope tag is projected so the caller can
// scope entity-group membership per environment and detect clusters that are not
// yet tagged. Environment scoping is performed client-side after discovery (see
// selectClustersForEnvironment in the entitygroups package), so no scope value
// is ever interpolated here; the tag selectors are validated before
// interpolation, so the query carries no KQL-injection surface.
func kustoDiscoveryQuery(cfg KustoDiscoveryConfig) (string, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	selectionFilter := fmt.Sprintf("isnotempty(tags['%s'])", cfg.TagKey)
	if cfg.TagValue != "" {
		selectionFilter = fmt.Sprintf("tags['%s'] =~ '%s'", cfg.TagKey, cfg.TagValue)
	}

	return fmt.Sprintf("resources | where type =~ 'microsoft.kusto/clusters' | where %s and properties.provisioningState == 'Succeeded' | project name, location, uri=tostring(properties.uri), id, environment=tostring(tags['%s'])", selectionFilter, cfg.ScopeTagKey), nil
}

// DiscoverKustoClusters returns every Kusto cluster that carries the configured
// selection tag, using Azure Resource Graph to query across all accessible
// subscriptions (the same discovery pattern as grafanactl). Each returned
// cluster includes its configured scope tag value, which may be empty. Callers
// scope the result to a single environment via selectClustersForEnvironment,
// which fails closed when a discovered cluster is missing a valid scope tag so
// membership is never rebuilt from a partial set.
func (c *ResourceGraphKustoDiscoveryClient) DiscoverKustoClusters(ctx context.Context) ([]KustoCluster, error) {
	query, err := kustoDiscoveryQuery(c.cfg)
	if err != nil {
		return nil, err
	}
	format := armresourcegraph.ResultFormatObjectArray

	var clusters []KustoCluster
	var skipToken *string
	for {
		result, err := c.client.Resources(ctx, armresourcegraph.QueryRequest{
			Query: &query,
			Options: &armresourcegraph.QueryRequestOptions{
				ResultFormat: &format,
				SkipToken:    skipToken,
			},
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to query Resource Graph: %w", err)
		}

		rows, ok := result.Data.([]any)
		if !ok {
			raw, _ := json.Marshal(result.Data)
			return nil, fmt.Errorf("unexpected Resource Graph result type: %T (raw: %s)", result.Data, string(raw))
		}

		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unexpected row type: %T", row)
			}

			cluster := KustoCluster{}
			if v, ok := m["name"].(string); ok {
				cluster.Name = v
			}
			if v, ok := m["location"].(string); ok {
				cluster.Location = v
			}
			if v, ok := m["uri"].(string); ok {
				cluster.URI = v
			}
			if v, ok := m["id"].(string); ok {
				cluster.ID = v
			}
			if v, ok := m["environment"].(string); ok {
				cluster.Environment = v
			}

			if cluster.Name == "" || cluster.Location == "" || cluster.URI == "" {
				return nil, fmt.Errorf("discovered cluster has missing fields: name=%q location=%q uri=%q id=%q", cluster.Name, cluster.Location, cluster.URI, cluster.ID)
			}

			clusters = append(clusters, cluster)
		}

		skipToken = result.SkipToken
		if skipToken == nil || *skipToken == "" {
			break
		}
	}

	return clusters, nil
}
