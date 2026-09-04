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

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// rawOptions holds all inputs sourced from environment variables, unvalidated.
type rawOptions struct {
	subscriptionID string
	resourceGroup  string
	clusterName    string
	region         string

	profile   string
	zones     string
	zoneCount string

	nodeSubnetID         string
	podSubnetID          string
	networkDataplane     string
	networkPolicy        string
	outboundIPResourceID string

	managedIdentityID string
	etcdKMSKeyURI     string

	kubernetesVersion string
	clusterTags       string

	metricLabelsAllowlist      string
	metricAnnotationsAllowlist string
}

// newRawOptionsFromEnv builds rawOptions from environment variables only. It
// does not call any external tools or APIs, which makes it safe to
// unit-test. Only textual defaults are applied here; required-field and
// shape validation happens in Validate.
func newRawOptionsFromEnv(env func(string) string) *rawOptions {
	o := &rawOptions{
		subscriptionID: env("SUBSCRIPTION_ID"),
		resourceGroup:  env("RESOURCE_GROUP"),
		clusterName:    env("CLUSTER_NAME"),
		region:         env("REGION"),

		profile:   env("PROFILE"),
		zones:     env("ZONES"),
		zoneCount: env("AZURE_REGION_AVAILABILITY_ZONE_COUNT"),

		nodeSubnetID:         env("NODE_SUBNET_ID"),
		podSubnetID:          env("POD_SUBNET_ID"),
		networkDataplane:     env("NETWORK_DATAPLANE"),
		networkPolicy:        env("NETWORK_POLICY"),
		outboundIPResourceID: env("OUTBOUND_IP_RESOURCE_ID"),

		managedIdentityID: env("MANAGED_IDENTITY_ID"),
		etcdKMSKeyURI:     env("ETCD_KMS_KEY_URI"),

		kubernetesVersion: env("KUBERNETES_VERSION"),
		clusterTags:       env("CLUSTER_TAGS"),

		metricLabelsAllowlist:      env("METRIC_LABELS_ALLOWLIST"),
		metricAnnotationsAllowlist: env("METRIC_ANNOTATIONS_ALLOWLIST"),
	}
	return o
}

// validatedOptions is rawOptions after required-field checks, CSV parsing,
// profile lookup, and credential resolution.
type validatedOptions struct {
	subscriptionID string
	resourceGroup  string
	clusterName    string
	region         string

	profile compute.Profile
	zones   []string

	nodeSubnetID         string
	podSubnetID          string
	networkDataplane     string
	networkPolicy        string
	outboundIPResourceID string

	managedIdentityID string
	etcdKMSKeyURI     string

	kubernetesVersion string
	clusterTags       map[string]string

	metricLabelsAllowlist      string
	metricAnnotationsAllowlist string
}

// Validate checks required fields, parses CSV-shaped values, and resolves the
// nodepool profile by name. It performs no side effects; Azure credentials and
// clients are built in Complete.
func (o *rawOptions) Validate(_ context.Context) (*validatedOptions, error) {
	required := []struct{ key, val string }{
		{"SUBSCRIPTION_ID", o.subscriptionID},
		{"RESOURCE_GROUP", o.resourceGroup},
		{"CLUSTER_NAME", o.clusterName},
		{"REGION", o.region},
		{"PROFILE", o.profile},
		{"NODE_SUBNET_ID", o.nodeSubnetID},
		{"POD_SUBNET_ID", o.podSubnetID},
		{"NETWORK_DATAPLANE", o.networkDataplane},
		{"NETWORK_POLICY", o.networkPolicy},
		{"OUTBOUND_IP_RESOURCE_ID", o.outboundIPResourceID},
		{"MANAGED_IDENTITY_ID", o.managedIdentityID},
		{"ETCD_KMS_KEY_URI", o.etcdKMSKeyURI},
		{"KUBERNETES_VERSION", o.kubernetesVersion},
	}
	var missing []string
	for _, kv := range required {
		if len(kv.val) == 0 {
			missing = append(missing, kv.key)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	profile, ok := compute.LookupProfile(o.profile)
	if !ok {
		return nil, fmt.Errorf("PROFILE: unknown profile %q (valid: %s)", o.profile, strings.Join(compute.ValidProfileNames(), ", "))
	}

	zoneCount, err := strconv.Atoi(strings.TrimSpace(o.zoneCount))
	if err != nil {
		return nil, fmt.Errorf("AZURE_REGION_AVAILABILITY_ZONE_COUNT: %q is not a valid integer", o.zoneCount)
	}
	zones, err := compute.ResolveZones(o.zones, zoneCount)
	if err != nil {
		return nil, fmt.Errorf("ZONES: %w", err)
	}

	tags, err := parseTags(o.clusterTags)
	if err != nil {
		return nil, fmt.Errorf("CLUSTER_TAGS: %w", err)
	}

	return &validatedOptions{
		subscriptionID: o.subscriptionID,
		resourceGroup:  o.resourceGroup,
		clusterName:    o.clusterName,
		region:         o.region,

		profile: profile,
		zones:   zones,

		nodeSubnetID:         o.nodeSubnetID,
		podSubnetID:          o.podSubnetID,
		networkDataplane:     o.networkDataplane,
		networkPolicy:        o.networkPolicy,
		outboundIPResourceID: o.outboundIPResourceID,

		managedIdentityID: o.managedIdentityID,
		etcdKMSKeyURI:     o.etcdKMSKeyURI,

		kubernetesVersion: o.kubernetesVersion,
		clusterTags:       tags,

		metricLabelsAllowlist:      o.metricLabelsAllowlist,
		metricAnnotationsAllowlist: o.metricAnnotationsAllowlist,
	}, nil
}

// completedOptions is validatedOptions plus the Azure clients needed to
// create the cluster and its pools.
type completedOptions struct {
	*validatedOptions

	clustersClient *armcontainerservice.ManagedClustersClient
	poolsClient    *armcontainerservice.AgentPoolsClient
	usageClient    *armcompute.UsageClient
	skuCache       *skucache.SKUCache
}

// Complete builds the Azure credential and clients used to create the cluster
// and its pools.
func (o *validatedOptions) Complete(_ context.Context) (*completedOptions, error) {
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, fmt.Errorf("azidentity: %w", err)
	}

	policyClientOptions := &policy.ClientOptions{}
	armClientOptions := &azcorearm.ClientOptions{ClientOptions: *policyClientOptions}

	clustersClient, err := armcontainerservice.NewManagedClustersClient(o.subscriptionID, cred, armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("managed clusters client: %w", err)
	}
	poolsClient, err := armcontainerservice.NewAgentPoolsClient(o.subscriptionID, cred, armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("agent pools client: %w", err)
	}
	usageClient, err := armcompute.NewUsageClient(o.subscriptionID, cred, armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("usage client: %w", err)
	}

	return &completedOptions{
		validatedOptions: o,

		clustersClient: clustersClient,
		poolsClient:    poolsClient,
		usageClient:    usageClient,
		skuCache:       skucache.NewSKUCache(o.region, cred, policyClientOptions, nil),
	}, nil
}

// parseCSVList splits a comma-separated list, trimming whitespace and
// dropping empty entries. An empty or whitespace-only input yields nil.
func parseCSVList(raw string) []string {
	var out []string
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if len(entry) == 0 {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// parseTags parses a comma-separated "key=value" list, matching the CSV tag
// format used by aks-cluster-base.bicep's csvTagsToObject. An empty input
// yields an empty, non-nil map.
func parseTags(raw string) (map[string]string, error) {
	tags := make(map[string]string)
	for _, entry := range parseCSVList(raw) {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || len(key) == 0 {
			return nil, fmt.Errorf("malformed tag entry %q (expected key=value)", entry)
		}
		tags[key] = value
	}
	return tags, nil
}
