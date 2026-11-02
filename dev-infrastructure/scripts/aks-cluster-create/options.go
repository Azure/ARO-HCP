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
	"fmt"
	"strconv"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/internal/azsdk"
)

type clusterType string

const (
	clusterTypeMgmt clusterType = "mgmt"
	clusterTypeSvc  clusterType = "svc"
)

// rawPoolOptions holds one pool block's inputs sourced from environment
// variables, unvalidated.
type rawPoolOptions struct {
	name              string
	vmSize            string
	osDiskSizeGB      string
	minCount          string
	maxCount          string
	poolCount         string
	zones             string
	zoneRedundantMode string
	maxSurge          string
	maxUnavailable    string
}

// rawOptions holds all inputs sourced from environment variables, unvalidated.
type rawOptions struct {
	clusterType                 string
	systemPoolOnly              string
	locationAvailabilityZones   string
	istioVersions               string
	subscriptionID              string
	resourceGroup               string
	clusterName                 string
	region                      string
	regionAvailabilityZoneCount string

	system rawPoolOptions
	user   rawPoolOptions
	infra  rawPoolOptions

	nodeSubnetID         string
	podSubnetID          string
	networkDataplane     string
	networkPolicy        string
	outboundIPResourceID string

	managedIdentityID string
	etcdKMSKeyURI     string

	kubernetesVersion  string
	clusterTags        string
	owningTeamTagValue string

	metricLabelsAllowlist      string
	metricAnnotationsAllowlist string
}

// newRawOptionsFromEnv builds rawOptions from environment variables only. It
// does not call any external tools or APIs, which makes it safe to
// unit-test. Only textual defaults are applied here; required-field and
// shape validation happens in Validate.
func newRawOptionsFromEnv(env func(string) string) *rawOptions {
	o := &rawOptions{
		clusterType:                 env("CLUSTER_TYPE"),
		systemPoolOnly:              env("SYSTEM_POOL_ONLY"),
		locationAvailabilityZones:   env("LOCATION_AVAILABILITY_ZONES"),
		istioVersions:               env("ISTIO_VERSIONS"),
		subscriptionID:              env("SUBSCRIPTION_ID"),
		resourceGroup:               env("RESOURCE_GROUP"),
		clusterName:                 env("CLUSTER_NAME"),
		region:                      env("REGION"),
		regionAvailabilityZoneCount: env("REGION_AVAILABILITY_ZONE_COUNT"),

		system: rawPoolOptions{
			name:              env("SYSTEM_POOL_NAME"),
			vmSize:            env("SYSTEM_POOL_VM_SIZE"),
			osDiskSizeGB:      env("SYSTEM_POOL_OS_DISK_SIZE_GB"),
			minCount:          env("SYSTEM_POOL_MIN_COUNT"),
			maxCount:          env("SYSTEM_POOL_MAX_COUNT"),
			zones:             env("SYSTEM_POOL_ZONES"),
			zoneRedundantMode: env("SYSTEM_POOL_ZONE_REDUNDANT_MODE"),
		},
		user: rawPoolOptions{
			name:              env("USER_POOL_NAME"),
			vmSize:            env("USER_POOL_VM_SIZE"),
			osDiskSizeGB:      env("USER_POOL_OS_DISK_SIZE_GB"),
			minCount:          env("USER_POOL_MIN_COUNT"),
			maxCount:          env("USER_POOL_MAX_COUNT"),
			poolCount:         env("USER_POOL_COUNT"),
			zones:             env("USER_POOL_ZONES"),
			zoneRedundantMode: env("USER_POOL_ZONE_REDUNDANT_MODE"),
			maxSurge:          env("USER_POOL_MAX_SURGE"),
			maxUnavailable:    env("USER_POOL_MAX_UNAVAILABLE"),
		},
		infra: rawPoolOptions{
			name:              env("INFRA_POOL_NAME"),
			vmSize:            env("INFRA_POOL_VM_SIZE"),
			osDiskSizeGB:      env("INFRA_POOL_OS_DISK_SIZE_GB"),
			minCount:          env("INFRA_POOL_MIN_COUNT"),
			maxCount:          env("INFRA_POOL_MAX_COUNT"),
			poolCount:         env("INFRA_POOL_COUNT"),
			zones:             env("INFRA_POOL_ZONES"),
			zoneRedundantMode: env("INFRA_POOL_ZONE_REDUNDANT_MODE"),
			maxSurge:          env("INFRA_POOL_MAX_SURGE"),
			maxUnavailable:    env("INFRA_POOL_MAX_UNAVAILABLE"),
		},

		nodeSubnetID:         env("NODE_SUBNET_ID"),
		podSubnetID:          env("POD_SUBNET_ID"),
		networkDataplane:     env("NETWORK_DATAPLANE"),
		networkPolicy:        env("NETWORK_POLICY"),
		outboundIPResourceID: env("OUTBOUND_IP_RESOURCE_ID"),

		managedIdentityID: env("MANAGED_IDENTITY_ID"),
		etcdKMSKeyURI:     env("ETCD_KMS_KEY_URI"),

		kubernetesVersion:  env("KUBERNETES_VERSION"),
		clusterTags:        env("CLUSTER_TAGS"),
		owningTeamTagValue: env("OWNING_TEAM_TAG_VALUE"),

		metricLabelsAllowlist:      env("METRIC_LABELS_ALLOWLIST"),
		metricAnnotationsAllowlist: env("METRIC_ANNOTATIONS_ALLOWLIST"),
	}
	return o
}

// poolConfig is a rawPoolOptions after required-field checks, integer parsing,
// and CSV zone parsing.
type poolConfig struct {
	name           string
	vmSize         string
	osDiskSizeGB   int32
	minCount       int32
	maxCount       int32
	poolCount      int
	zones          []string
	maxSurge       string
	maxUnavailable string
}

// validatedOptions is rawOptions after required-field checks, integer parsing,
// and CSV zone parsing.
type validatedOptions struct {
	clusterType    clusterType
	systemPoolOnly bool
	istioVersions  []string
	subscriptionID string
	resourceGroup  string
	clusterName    string
	region         string

	system poolConfig
	user   poolConfig
	infra  poolConfig

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

// Validate checks required fields, parses integer and CSV-shaped values, and
// builds the per-pool configuration. It performs no side effects; Azure
// credentials and clients are built in Complete.
func (o *rawOptions) Validate() (*validatedOptions, error) {
	kind := clusterType(o.clusterType)
	if kind != clusterTypeMgmt && kind != clusterTypeSvc {
		return nil, fmt.Errorf("CLUSTER_TYPE must be mgmt or svc, got %q", o.clusterType)
	}
	systemPoolOnly := false
	if o.systemPoolOnly != "" {
		var err error
		systemPoolOnly, err = strconv.ParseBool(o.systemPoolOnly)
		if err != nil {
			return nil, fmt.Errorf("SYSTEM_POOL_ONLY: %w", err)
		}
	}
	if kind == clusterTypeMgmt && systemPoolOnly {
		return nil, fmt.Errorf("SYSTEM_POOL_ONLY is not supported for mgmt")
	}
	istioVersions := parseCSVList(o.istioVersions)
	if kind == clusterTypeSvc && len(istioVersions) == 0 {
		return nil, fmt.Errorf("ISTIO_VERSIONS is required for svc bootstrap")
	}
	required := []struct{ key, val string }{
		{"SUBSCRIPTION_ID", o.subscriptionID},
		{"RESOURCE_GROUP", o.resourceGroup},
		{"CLUSTER_NAME", o.clusterName},
		{"REGION", o.region},
		{"NODE_SUBNET_ID", o.nodeSubnetID},
		{"POD_SUBNET_ID", o.podSubnetID},
		{"NETWORK_DATAPLANE", o.networkDataplane},
		{"NETWORK_POLICY", o.networkPolicy},
		{"OUTBOUND_IP_RESOURCE_ID", o.outboundIPResourceID},
		{"MANAGED_IDENTITY_ID", o.managedIdentityID},
		{"ETCD_KMS_KEY_URI", o.etcdKMSKeyURI},
		{"KUBERNETES_VERSION", o.kubernetesVersion},
	}
	if kind == clusterTypeMgmt {
		required = append(required, struct{ key, val string }{"REGION_AVAILABILITY_ZONE_COUNT", o.regionAvailabilityZoneCount})
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

	var regionAvailabilityZoneCount int
	if kind == clusterTypeMgmt {
		var err error
		regionAvailabilityZoneCount, err = parseInt("REGION_AVAILABILITY_ZONE_COUNT", o.regionAvailabilityZoneCount)
		if err != nil {
			return nil, err
		}
		if regionAvailabilityZoneCount < compute.RequiredAvailabilityZones {
			return nil, fmt.Errorf("REGION_AVAILABILITY_ZONE_COUNT must be at least %d, got %d", compute.RequiredAvailabilityZones, regionAvailabilityZoneCount)
		}
	}
	resolveZones := func(prefix string, raw rawPoolOptions) ([]string, error) {
		if kind == clusterTypeSvc {
			return resolveSvcZones(prefix, raw, o.locationAvailabilityZones)
		}
		zones, err := compute.ResolveZones(raw.zones, regionAvailabilityZoneCount)
		if err != nil {
			return nil, fmt.Errorf("%s_ZONES: %w", prefix, err)
		}
		return zones, nil
	}
	system, err := validatePool("SYSTEM_POOL", o.system, resolveZones)
	if err != nil {
		return nil, err
	}
	var user, infra poolConfig
	if !systemPoolOnly {
		user, err = validatePool("USER_POOL", o.user, resolveZones)
		if err != nil {
			return nil, err
		}
		infra, err = validatePool("INFRA_POOL", o.infra, resolveZones)
		if err != nil {
			return nil, err
		}
	}

	tags, err := parseTags(o.clusterTags)
	if err != nil {
		return nil, fmt.Errorf("CLUSTER_TAGS: %w", err)
	}
	// Match Bicep's union: the alert-routing owner overrides the CSV tag.
	tags["owningTeam"] = o.owningTeamTagValue

	return &validatedOptions{
		clusterType:    kind,
		systemPoolOnly: systemPoolOnly,
		istioVersions:  istioVersions,
		subscriptionID: o.subscriptionID,
		resourceGroup:  o.resourceGroup,
		clusterName:    o.clusterName,
		region:         o.region,

		system: system,
		user:   user,
		infra:  infra,

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

// resolveSvcZones mirrors svc-cluster.bicep's explicit CSV / regional fallback,
// then the base/pool modules' redundancy switch. No exact-three-zone policy
// applies to svc, including regions with no availability zones.
func resolveSvcZones(prefix string, raw rawPoolOptions, regionalCSV string) ([]string, error) {
	switch raw.zoneRedundantMode {
	case "Auto", "Enabled", "Disabled":
	default:
		return nil, fmt.Errorf("%s_ZONE_REDUNDANT_MODE must be Auto, Enabled, or Disabled", prefix)
	}
	zones := parseCSVList(raw.zones)
	if len(zones) == 0 {
		zones = parseCSVList(regionalCSV)
	}
	if raw.zoneRedundantMode == "Disabled" {
		return nil, nil
	}
	return zones, nil
}

// validatePool parses the common pool settings, with profile-specific placement.
func validatePool(prefix string, raw rawPoolOptions, resolveZones func(string, rawPoolOptions) ([]string, error)) (poolConfig, error) {
	if len(raw.name) == 0 {
		return poolConfig{}, fmt.Errorf("%s_NAME is required", prefix)
	}
	if len(raw.vmSize) == 0 {
		return poolConfig{}, fmt.Errorf("%s_VM_SIZE is required", prefix)
	}
	osDiskSizeGB, err := parseInt32(prefix+"_OS_DISK_SIZE_GB", raw.osDiskSizeGB)
	if err != nil {
		return poolConfig{}, err
	}
	minCount, err := parseInt32(prefix+"_MIN_COUNT", raw.minCount)
	if err != nil {
		return poolConfig{}, err
	}
	maxCount, err := parseInt32(prefix+"_MAX_COUNT", raw.maxCount)
	if err != nil {
		return poolConfig{}, err
	}
	if osDiskSizeGB < 0 {
		return poolConfig{}, fmt.Errorf("%s_OS_DISK_SIZE_GB must not be negative", prefix)
	}
	if minCount < 0 {
		return poolConfig{}, fmt.Errorf("%s_MIN_COUNT must not be negative", prefix)
	}
	if maxCount < 0 {
		return poolConfig{}, fmt.Errorf("%s_MAX_COUNT must not be negative", prefix)
	}
	if minCount > maxCount {
		return poolConfig{}, fmt.Errorf("%s_MIN_COUNT (%d) must not exceed %s_MAX_COUNT (%d)", prefix, minCount, prefix, maxCount)
	}

	// Pool count is optional; default to one when unset.
	poolCount := 1
	if len(raw.poolCount) > 0 {
		poolCount, err = parseInt(prefix+"_COUNT", raw.poolCount)
		if err != nil {
			return poolConfig{}, err
		}
		if poolCount < 1 {
			return poolConfig{}, fmt.Errorf("%s_COUNT must be at least 1", prefix)
		}
	}

	zones, err := resolveZones(prefix, raw)
	if err != nil {
		return poolConfig{}, err
	}

	return poolConfig{
		name:           raw.name,
		vmSize:         raw.vmSize,
		osDiskSizeGB:   osDiskSizeGB,
		minCount:       minCount,
		maxCount:       maxCount,
		poolCount:      poolCount,
		zones:          zones,
		maxSurge:       raw.maxSurge,
		maxUnavailable: raw.maxUnavailable,
	}, nil
}

// completedOptions is validatedOptions plus the Azure clients needed to
// create the cluster and its pools.
type completedOptions struct {
	*validatedOptions

	clustersClient             *armcontainerservice.ManagedClustersClient
	deploymentsClient          *armdeployments.DeploymentsClient
	deploymentOperationsClient *armdeployments.DeploymentOperationsClient
	skuCache                   *skucache.SKUCache
}

// Complete builds the Azure credential and clients used to create the cluster
// and its pools.
func (o *validatedOptions) Complete() (*completedOptions, error) {
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, fmt.Errorf("azidentity: %w", err)
	}

	clientOptions := policy.ClientOptions{
		// Honor server Retry-After even when it exceeds the SDK's default 60s
		// cap. The caller's context still bounds the wait and retry count is unchanged.
		Retry: policy.RetryOptions{MaxRetryDelay: -1},
		Telemetry: policy.TelemetryOptions{
			ApplicationID: string(azsdk.ComponentInfra),
		},
	}
	armClientOptions := &azcorearm.ClientOptions{ClientOptions: clientOptions}

	clustersClient, err := armcontainerservice.NewManagedClustersClient(o.subscriptionID, cred, armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("managed clusters client: %w", err)
	}
	deploymentsClient, err := armdeployments.NewDeploymentsClient(o.subscriptionID, cred, armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("deployments client: %w", err)
	}
	deploymentOperationsClient, err := armdeployments.NewDeploymentOperationsClient(o.subscriptionID, cred, armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("deployment operations client: %w", err)
	}

	return &completedOptions{
		validatedOptions: o,

		clustersClient:             clustersClient,
		deploymentsClient:          deploymentsClient,
		deploymentOperationsClient: deploymentOperationsClient,
		skuCache:                   skucache.NewSKUCache(o.region, cred, &clientOptions, nil),
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

// parseInt parses a required integer environment value.
func parseInt(key, raw string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a valid integer", key, raw)
	}
	return n, nil
}

// parseInt32 parses a required int32 environment value.
func parseInt32(key, raw string) (int32, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a valid integer", key, raw)
	}
	return int32(n), nil
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
