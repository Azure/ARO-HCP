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
	"slices"
	"strconv"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/internal/azsdk"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// rawPoolOptions holds one pool block's inputs sourced from environment
// variables, unvalidated.
type rawPoolOptions struct {
	name         string
	vmSize       string
	osDiskSizeGB string
	minCount     string
	maxCount     string
	poolCount    string
	zones        string
}

// commonRawOptions holds shared inputs sourced from environment variables, unvalidated.
type commonRawOptions struct {
	subscriptionID string
	resourceGroup  string
	clusterName    string
	region         string

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

func commonOptionsFromEnv(env func(string) string) commonRawOptions {
	return commonRawOptions{
		subscriptionID: env("SUBSCRIPTION_ID"),
		resourceGroup:  env("RESOURCE_GROUP"),
		clusterName:    env("CLUSTER_NAME"),
		region:         env("REGION"),

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
}

func poolOptionsFromEnv(env func(string) string, prefix string) rawPoolOptions {
	return rawPoolOptions{
		name:         env(prefix + "_NAME"),
		vmSize:       env(prefix + "_VM_SIZE"),
		osDiskSizeGB: env(prefix + "_OS_DISK_SIZE_GB"),
		minCount:     env(prefix + "_MIN_COUNT"),
		maxCount:     env(prefix + "_MAX_COUNT"),
		poolCount:    env(prefix + "_COUNT"),
		zones:        env(prefix + "_ZONES"),
	}
}

// validatedPoolConfig contains validated pool inputs.
type validatedPoolConfig struct {
	// Pool name, used as the prefix for per-zone pools.
	name string
	// Azure VM size for each node.
	vmSize string
	// OS disk size in GB per node.
	osDiskSizeGB int32
	// Autoscaler minimum node count per pool.
	minCount int32
	// Autoscaler maximum node count per pool.
	maxCount int32
	// Number of per-zone pools; ignored for regional pools.
	poolCount int
	// Configured zones, used when no existing pools match the role.
	explicitZones []string

	// Role, AKS pool mode, taints, and Swift networking policy.
	class compute.PoolClass
	// One pool spanning its zones or one pool per zone.
	mode compute.PoolMode
	// Maximum pods per node.
	maxPods int32
	// AKS node pool upgrade policy.
	upgradeSettings armcontainerservice.AgentPoolUpgradeSettings
}

// poolConfig contains validated pool inputs and resolved availability zones.
type poolConfig struct {
	// Validated inputs retained during completion.
	validatedPoolConfig
	// Resolved zones: existing pools, explicit config, then SKU discovery.
	zones []string
}

// validatedOptions contains shared deployment inputs and pool configuration.
type validatedOptions struct {
	subscriptionID string
	resourceGroup  string
	clusterName    string
	region         string

	system validatedPoolConfig
	user   validatedPoolConfig
	infra  validatedPoolConfig

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

	systemPoolOnly     bool
	serviceMeshProfile *armcontainerservice.ServiceMeshProfile
	ingressProfile     *armcontainerservice.ManagedClusterIngressProfile
}

// Validate checks required fields and parses shared inputs. It performs no side
// effects; Azure credentials and clients are built in Complete.
func (o *commonRawOptions) Validate() (*validatedOptions, error) {
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
	var missing []string
	for _, kv := range required {
		if len(kv.val) == 0 {
			missing = append(missing, kv.key)
		}
	}
	if len(missing) > 0 {
		return nil, utils.TrackError(fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", ")))
	}

	tags, err := parseTags(o.clusterTags)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("CLUSTER_TAGS: %w", err))
	}
	// Match Bicep's union: the alert-routing owner overrides the CSV tag.
	tags["owningTeam"] = o.owningTeamTagValue

	return &validatedOptions{
		subscriptionID: o.subscriptionID,
		resourceGroup:  o.resourceGroup,
		clusterName:    o.clusterName,
		region:         o.region,

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

// validatePool parses one pool block into a poolConfig. prefix is the block's
// env-var prefix (e.g. "USER_POOL"), used in lookups and error messages.
// Commands resolve zones separately.
func validatePool(prefix string, raw rawPoolOptions) (validatedPoolConfig, error) {
	if len(raw.name) == 0 {
		return validatedPoolConfig{}, utils.TrackError(fmt.Errorf("%s_NAME is required", prefix))
	}
	if len(raw.vmSize) == 0 {
		return validatedPoolConfig{}, utils.TrackError(fmt.Errorf("%s_VM_SIZE is required", prefix))
	}
	osDiskSizeGB, err := parseInt32(prefix+"_OS_DISK_SIZE_GB", raw.osDiskSizeGB)
	if err != nil {
		return validatedPoolConfig{}, utils.TrackError(err)
	}
	minCount, err := parseInt32(prefix+"_MIN_COUNT", raw.minCount)
	if err != nil {
		return validatedPoolConfig{}, utils.TrackError(err)
	}
	maxCount, err := parseInt32(prefix+"_MAX_COUNT", raw.maxCount)
	if err != nil {
		return validatedPoolConfig{}, utils.TrackError(err)
	}
	if osDiskSizeGB < 0 {
		return validatedPoolConfig{}, utils.TrackError(fmt.Errorf("%s_OS_DISK_SIZE_GB must not be negative", prefix))
	}
	if minCount < 0 {
		return validatedPoolConfig{}, utils.TrackError(fmt.Errorf("%s_MIN_COUNT must not be negative", prefix))
	}
	if maxCount < 0 {
		return validatedPoolConfig{}, utils.TrackError(fmt.Errorf("%s_MAX_COUNT must not be negative", prefix))
	}
	if minCount > maxCount {
		return validatedPoolConfig{}, utils.TrackError(fmt.Errorf("%s_MIN_COUNT (%d) must not exceed %s_MAX_COUNT (%d)", prefix, minCount, prefix, maxCount))
	}

	// Pool count is optional; default to one when unset.
	poolCount := 1
	if len(raw.poolCount) > 0 {
		poolCount, err = parseInt(prefix+"_COUNT", raw.poolCount)
		if err != nil {
			return validatedPoolConfig{}, utils.TrackError(err)
		}
		if poolCount < 1 {
			return validatedPoolConfig{}, utils.TrackError(fmt.Errorf("%s_COUNT must be at least 1", prefix))
		}
	}

	explicitZones, err := parseZoneList(prefix+"_ZONES", raw.zones)
	if err != nil {
		return validatedPoolConfig{}, utils.TrackError(err)
	}

	return validatedPoolConfig{
		name:          raw.name,
		vmSize:        raw.vmSize,
		osDiskSizeGB:  osDiskSizeGB,
		minCount:      minCount,
		maxCount:      maxCount,
		poolCount:     poolCount,
		explicitZones: explicitZones,
	}, nil
}

// completedOptions is validatedOptions plus the Azure clients needed to
// create the cluster and its pools.
type completedOptions struct {
	// Validated deployment inputs.
	*validatedOptions

	// System pool configuration with resolved zones.
	system poolConfig
	// Worker pool configuration with resolved zones.
	user poolConfig
	// Infrastructure pool configuration with resolved zones.
	infra poolConfig

	// Observed cluster; nil when creating a new cluster.
	cluster *armcontainerservice.ManagedCluster

	// Reads and updates AKS clusters.
	clustersClient *armcontainerservice.ManagedClustersClient
	// Submits and monitors ARM deployments.
	deploymentsClient *armdeployments.DeploymentsClient
	// Retrieves deployment operation diagnostics.
	deploymentOperationsClient *armdeployments.DeploymentOperationsClient
	// Regional VM SKU metadata used to build pools.
	skuCache *skucache.SKUCache
}

// Complete builds the Azure credential and clients used to create the cluster
// and its pools, observes the cluster, and resolves pool zones.
func (o *validatedOptions) Complete(ctx context.Context) (*completedOptions, error) {
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("azidentity: %w", err))
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
		return nil, utils.TrackError(fmt.Errorf("managed clusters client: %w", err))
	}
	deploymentsClient, err := armdeployments.NewDeploymentsClient(o.subscriptionID, cred, armClientOptions)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("deployments client: %w", err))
	}
	deploymentOperationsClient, err := armdeployments.NewDeploymentOperationsClient(o.subscriptionID, cred, armClientOptions)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("deployment operations client: %w", err))
	}

	if err := waitForDeployment(ctx, deploymentsClient, deploymentOperationsClient, o.resourceGroup, deploymentName); err != nil {
		return nil, utils.TrackError(err)
	}

	cluster, err := observeCluster(ctx, clustersClient, o.resourceGroup, o.clusterName)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	skuCache := skucache.NewSKUCache(o.region, cred, &clientOptions, nil)
	system, user, infra, err := completePoolConfigs(ctx, o, skuCache, cluster)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	completed := &completedOptions{
		validatedOptions: o,
		cluster:          cluster,
		system:           system,
		user:             user,
		infra:            infra,

		clustersClient:             clustersClient,
		deploymentsClient:          deploymentsClient,
		deploymentOperationsClient: deploymentOperationsClient,
		skuCache:                   skuCache,
	}
	return completed, nil
}

func observeCluster(ctx context.Context, client *armcontainerservice.ManagedClustersClient, resourceGroup, clusterName string) (*armcontainerservice.ManagedCluster, error) {
	cluster, err := getCluster(ctx, client, resourceGroup, clusterName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("getting cluster: %w", err))
	}
	if cluster == nil {
		return nil, nil
	}
	return waitForManagedCluster(ctx, client, resourceGroup, clusterName, cluster)
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
		return 0, utils.TrackError(fmt.Errorf("%s: %q is not a valid integer", key, raw))
	}
	return n, nil
}

// parseInt32 parses a required int32 environment value.
func parseInt32(key, raw string) (int32, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
	if err != nil {
		return 0, utils.TrackError(fmt.Errorf("%s: %q is not a valid integer", key, raw))
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
			return nil, utils.TrackError(fmt.Errorf("malformed tag entry %q (expected key=value)", entry))
		}
		tags[key] = value
	}
	return tags, nil
}

// parseZoneList parses distinct positive zone numbers, preserving their order.
func parseZoneList(key, raw string) ([]string, error) {
	var zones []string
	for _, entry := range parseCSVList(raw) {
		zone, err := parseInt(key, entry)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		if zone < 1 {
			return nil, utils.TrackError(fmt.Errorf("%s: zone %q must be a positive integer", key, entry))
		}
		value := strconv.Itoa(zone)
		if slices.Contains(zones, value) {
			return nil, utils.TrackError(fmt.Errorf("%s: duplicate zone %d", key, zone))
		}
		zones = append(zones, value)
	}
	return zones, nil
}
