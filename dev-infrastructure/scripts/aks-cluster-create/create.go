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
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/agentpoolspec"
	"github.com/Azure/ARO-HCP/fleet/pkg/azure/quota"
	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// provisioningTagKey marks a ManagedCluster as mid-provisioning by this tool.
// Set in the initial create/update, cleared once the cluster and all its pools
// exist. A pipeline retry that finds the tag still set resumes from wherever
// it left off; a retry that finds the tag already gone treats the run as a
// no-op success.
const (
	provisioningTagKey   = "aro-hcp-provisioning"
	provisioningTagValue = "true"
)

// run creates the AKS cluster (with its first system pool inline) and then
// the remaining system, infra, and worker pools, in that order. Every step is
// safe to resend, so a pipeline retry after a partial failure just continues.
func run(ctx context.Context, o *completedOptions, logger logr.Logger) error {
	existing, err := getCluster(ctx, o.clustersClient, o.resourceGroup, o.clusterName)
	if err != nil {
		return fmt.Errorf("getting cluster: %w", err)
	}

	systemPools, infraPools, workerPools, failures, err := computeDesiredPools(ctx, logger, o)
	if err != nil {
		return fmt.Errorf("computing desired pools: %w", err)
	}
	logger.Info("computed desired pools", "systemPools", len(systemPools), "infraPools", len(infraPools), "workerPools", len(workerPools))
	if len(failures) > 0 {
		logger.Info("some tiers could not be fully allocated", "allocationFailures", failures)
	}

	// A cluster that exists without the provisioning tag has already been fully
	// provisioned by a prior run, so there is nothing left to apply. Still log
	// the desired plan and how it differs from the live cluster, then stop
	// without mutating anything.
	if existing != nil && !hasProvisioningTag(existing.Tags) {
		var currentPools []*armcontainerservice.ManagedClusterAgentPoolProfile
		if existing.Properties != nil {
			currentPools = existing.Properties.AgentPoolProfiles
		}
		logDesiredPlan(logger, systemPools, infraPools, workerPools, currentPools)
		logger.Info("cluster already fully provisioned, plan-only run, no changes applied")
		return nil
	}

	if compute.RequiredTierFailed(failures) {
		return fmt.Errorf("required tier allocation failed: %s", compute.BestFailureReason(failures))
	}
	if len(systemPools) == 0 {
		return fmt.Errorf("no system pool could be allocated: %s", compute.BestFailureReason(failures))
	}

	networkConfig := compute.NetworkConfig{
		VnetSubnetID: o.nodeSubnetID,
		PodSubnetID:  o.podSubnetID,
	}

	bootstrap := systemPools[0]
	remaining := make([]compute.Pool, 0, len(systemPools)-1+len(infraPools)+len(workerPools))
	remaining = append(remaining, systemPools[1:]...)
	remaining = append(remaining, infraPools...)
	remaining = append(remaining, workerPools...)

	logger.Info("creating/updating AKS cluster", "bootstrapPool", bootstrap.Name, "bootstrapVMSize", bootstrap.Spec.Size, "remainingPools", len(remaining))
	managedCluster := buildManagedCluster(o.validatedOptions, bootstrap, networkConfig)
	clusterPoller, err := o.clustersClient.BeginCreateOrUpdate(ctx, o.resourceGroup, o.clusterName, managedCluster, nil)
	if err != nil {
		return fmt.Errorf("creating cluster: %w", err)
	}
	if _, err := clusterPoller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("waiting for cluster creation: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, pool := range remaining {
		g.Go(func() error {
			defer utilruntime.HandleCrash()
			return ensurePool(gctx, o.poolsClient, o.resourceGroup, o.clusterName, pool, networkConfig, logger)
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	if err := removeProvisioningTag(ctx, o.clustersClient, o.resourceGroup, o.clusterName, desiredTags(o.validatedOptions)); err != nil {
		return fmt.Errorf("removing provisioning tag: %w", err)
	}

	logger.Info("cluster and node pools ready")
	return nil
}

// getCluster returns the cluster, or nil if it does not exist.
func getCluster(ctx context.Context, client *armcontainerservice.ManagedClustersClient, resourceGroup, clusterName string) (*armcontainerservice.ManagedCluster, error) {
	resp, err := client.Get(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &resp.ManagedCluster, nil
}

func hasProvisioningTag(tags map[string]*string) bool {
	v, ok := tags[provisioningTagKey]
	return ok && v != nil && *v == provisioningTagValue
}

// desiredTags is the tag set the cluster should carry once provisioning is
// done: a fresh copy of the operator-provided cluster tags, safe for the
// caller to mutate (e.g. by adding the provisioning tag).
func desiredTags(o *validatedOptions) map[string]string {
	tags := make(map[string]string, len(o.clusterTags))
	maps.Copy(tags, o.clusterTags)
	return tags
}

// computeDesiredPools mirrors the nodepool controller's SyncOnce: it calls
// compute.ResolveDesiredPools over the full profile (system, infra, and
// worker tiers together), then splits the result by role.
func computeDesiredPools(ctx context.Context, logger logr.Logger, o *completedOptions) (system, infra, worker []compute.Pool, failures []compute.AllocationFailure, err error) {
	ctx = utils.ContextWithLogger(ctx, logger)
	resolved, err := compute.ResolveDesiredPools(ctx, o.skuCache, o.subscriptionID, o.profile, o.zones,
		func(families sets.Set[compute.VMFamily]) (map[compute.VMFamily]compute.QuotaUsage, error) {
			return quota.FetchUsage(ctx, o.usageClient, o.region, families)
		})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	system, infra, worker = splitPoolsByRole(resolved.Pools)
	return system, infra, worker, resolved.Failures, nil
}

// splitPoolsByRole groups pools by role, preserving relative order within
// each group. AKS requires at least one System-mode pool in the initial
// ManagedCluster payload; the caller embeds one system pool inline and
// creates the rest (remaining system pools, infra, worker) afterward.
func splitPoolsByRole(pools []compute.Pool) (system, infra, worker []compute.Pool) {
	for _, pool := range pools {
		switch pool.Role {
		case compute.PoolRoleSystem:
			system = append(system, pool)
		case compute.PoolRoleInfra:
			infra = append(infra, pool)
		default:
			worker = append(worker, pool)
		}
	}
	return system, infra, worker
}

// logDesiredPlan logs the desired pool set and how it differs from the
// cluster's current agent pools, without applying anything. Used when the
// provisioning tag is already gone: the tool still surfaces what it would do.
func logDesiredPlan(logger logr.Logger, system, infra, worker []compute.Pool, current []*armcontainerservice.ManagedClusterAgentPoolProfile) {
	desired := make([]compute.Pool, 0, len(system)+len(infra)+len(worker))
	desired = append(desired, system...)
	desired = append(desired, infra...)
	desired = append(desired, worker...)

	currentByName := make(map[string]*armcontainerservice.ManagedClusterAgentPoolProfile, len(current))
	for _, pool := range current {
		if pool != nil && pool.Name != nil {
			currentByName[*pool.Name] = pool
		}
	}

	desiredNames := sets.New[string]()
	var toCreate, toChange []string
	for _, pool := range desired {
		desiredNames.Insert(pool.Name)
		cur, ok := currentByName[pool.Name]
		if !ok {
			toCreate = append(toCreate, fmt.Sprintf("%s (vmSize=%s, maxCount=%d, zones=%s)", pool.Name, pool.Spec.Size, pool.MaxCount, pool.ZoneString()))
			continue
		}
		if changes := poolChanges(pool, cur); len(changes) > 0 {
			toChange = append(toChange, fmt.Sprintf("%s: %s", pool.Name, strings.Join(changes, ", ")))
		}
	}

	var toDelete []string
	for _, pool := range current {
		if pool != nil && pool.Name != nil && !desiredNames.Has(*pool.Name) {
			toDelete = append(toDelete, *pool.Name)
		}
	}

	logger.Info("desired plan (no changes applied)",
		"desiredPools", desired,
		"toCreate", toCreate,
		"toChange", toChange,
		"toDelete", toDelete,
	)
}

// poolChanges reports how a desired pool differs from its live counterpart,
// comparing the fields this tool would act on.
func poolChanges(desired compute.Pool, current *armcontainerservice.ManagedClusterAgentPoolProfile) []string {
	var changes []string
	if size := ptr.Deref(current.VMSize, ""); size != desired.Spec.Size {
		changes = append(changes, fmt.Sprintf("vmSize %s->%s", size, desired.Spec.Size))
	}
	if maxCount := ptr.Deref(current.MaxCount, 0); maxCount != desired.MaxCount {
		changes = append(changes, fmt.Sprintf("maxCount %d->%d", maxCount, desired.MaxCount))
	}
	return changes
}

const poolRetries = 3

// ensurePool creates pool if it does not already exist. If a prior interrupted
// run left the pool in Failed state, it re-issues CreateOrUpdate to recover it.
// Pools that already exist and are not Failed are left as-is. Transient
// failures are retried up to poolRetries times.
func ensurePool(ctx context.Context, client *armcontainerservice.AgentPoolsClient, resourceGroup, clusterName string, pool compute.Pool, networkConfig compute.NetworkConfig, logger logr.Logger) error {
	var lastErr error
	for attempt := range poolRetries {
		lastErr = tryEnsurePool(ctx, client, resourceGroup, clusterName, pool, networkConfig, logger)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return lastErr
		}
		logger.Info("pool creation failed, retrying", "pool", pool.Name, "role", pool.Role, "vmSize", pool.Spec.Size, "zones", pool.ZoneString(), "attempt", attempt+1, "maxAttempts", poolRetries, "error", lastErr)
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return lastErr
		case <-timer.C:
		}
	}
	return lastErr
}

func tryEnsurePool(ctx context.Context, client *armcontainerservice.AgentPoolsClient, resourceGroup, clusterName string, pool compute.Pool, networkConfig compute.NetworkConfig, logger logr.Logger) error {
	existing, err := client.Get(ctx, resourceGroup, clusterName, pool.Name, nil)
	if err == nil {
		state := ""
		if existing.Properties != nil && existing.Properties.ProvisioningState != nil {
			state = *existing.Properties.ProvisioningState
		}
		if state != "Failed" {
			logger.Info("pool already exists, skipping", "pool", pool.Name, "role", pool.Role, "vmSize", pool.Spec.Size, "zones", pool.ZoneString(), "provisioningState", state)
			return nil
		}
		logger.Info("pool in Failed state, re-creating", "pool", pool.Name, "role", pool.Role, "vmSize", pool.Spec.Size, "zones", pool.ZoneString())
	} else {
		var respErr *azcore.ResponseError
		if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
			return fmt.Errorf("checking pool %s: %w", pool.Name, err)
		}
		logger.Info("creating pool", "pool", pool.Name, "role", pool.Role, "vmSize", pool.Spec.Size, "zones", pool.ZoneString())
	}

	agentPool := armcontainerservice.AgentPool{
		Properties: agentpoolspec.Build(pool, networkConfig),
	}
	poller, err := client.BeginCreateOrUpdate(ctx, resourceGroup, clusterName, pool.Name, agentPool, nil)
	if err != nil {
		return fmt.Errorf("creating pool %s: %w", pool.Name, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return fmt.Errorf("waiting for pool %s: %w", pool.Name, err)
	}
	return nil
}

// armTags converts a plain tag map into the ARM pointer-map shape.
func armTags(tags map[string]string) map[string]*string {
	result := make(map[string]*string, len(tags))
	for k, v := range tags {
		result[k] = ptr.To(v)
	}
	return result
}

// removeProvisioningTag marks the cluster as fully provisioned. ARM's tags
// update replaces the whole map, so the full desired set is sent rather than
// just clearing one key.
func removeProvisioningTag(ctx context.Context, client *armcontainerservice.ManagedClustersClient, resourceGroup, clusterName string, tags map[string]string) error {
	poller, err := client.BeginUpdateTags(ctx, resourceGroup, clusterName, armcontainerservice.TagsObject{Tags: armTags(tags)}, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// toClusterAgentPoolProfile adapts pool properties built by agentpoolspec.Build
// into the shape needed for ManagedCluster.Properties.AgentPoolProfiles. The SDK
// generates two nearly identical types — ManagedClusterAgentPoolProfileProperties
// (for the AgentPools API) and ManagedClusterAgentPoolProfile (for inline cluster
// pools) — so fields must be copied manually. When agentpoolspec.Build starts
// setting a new field, this function must be updated in lockstep.
// TestToClusterAgentPoolProfileFieldCoverage catches drift.
func toClusterAgentPoolProfile(name string, props *armcontainerservice.ManagedClusterAgentPoolProfileProperties) *armcontainerservice.ManagedClusterAgentPoolProfile {
	return &armcontainerservice.ManagedClusterAgentPoolProfile{
		Name:                   ptr.To(name),
		VMSize:                 props.VMSize,
		AvailabilityZones:      props.AvailabilityZones,
		OSDiskSizeGB:           props.OSDiskSizeGB,
		OSDiskType:             props.OSDiskType,
		EnableAutoScaling:      props.EnableAutoScaling,
		MinCount:               props.MinCount,
		MaxCount:               props.MaxCount,
		Mode:                   props.Mode,
		Type:                   props.Type,
		OSSKU:                  props.OSSKU,
		OSType:                 props.OSType,
		KubeletDiskType:        props.KubeletDiskType,
		EnableEncryptionAtHost: props.EnableEncryptionAtHost,
		EnableFIPS:             props.EnableFIPS,
		EnableNodePublicIP:     props.EnableNodePublicIP,
		MaxPods:                props.MaxPods,
		SecurityProfile:        props.SecurityProfile,
		UpgradeSettings:        props.UpgradeSettings,
		NodeLabels:             props.NodeLabels,
		NodeTaints:             props.NodeTaints,
		Tags:                   props.Tags,
		VnetSubnetID:           props.VnetSubnetID,
		PodSubnetID:            props.PodSubnetID,
	}
}

// buildManagedCluster builds the ManagedCluster payload, translating
// dev-infrastructure/modules/aks-cluster-base.bicep (lines 267-463; istio and
// ingress are omitted since management clusters never set deployIstio).
func buildManagedCluster(o *validatedOptions, bootstrap compute.Pool, networkConfig compute.NetworkConfig) armcontainerservice.ManagedCluster {
	tags := desiredTags(o)
	tags[provisioningTagKey] = provisioningTagValue

	var advancedNetworking *armcontainerservice.AdvancedNetworking
	if armcontainerservice.NetworkDataplane(o.networkDataplane) == armcontainerservice.NetworkDataplaneCilium {
		advancedNetworking = &armcontainerservice.AdvancedNetworking{
			Enabled:       ptr.To(true),
			Observability: &armcontainerservice.AdvancedNetworkingObservability{Enabled: ptr.To(true)},
		}
	}

	bootstrapProps := agentpoolspec.Build(bootstrap, networkConfig)

	return armcontainerservice.ManagedCluster{
		Location: ptr.To(o.region),
		SKU: &armcontainerservice.ManagedClusterSKU{
			Name: ptr.To(armcontainerservice.ManagedClusterSKUNameBase),
			Tier: ptr.To(armcontainerservice.ManagedClusterSKUTierStandard),
		},
		Tags: armTags(tags),
		Identity: &armcontainerservice.ManagedClusterIdentity{
			Type: ptr.To(armcontainerservice.ResourceIdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armcontainerservice.ManagedServiceIdentityUserAssignedIdentitiesValue{
				o.managedIdentityID: {},
			},
		},
		Properties: &armcontainerservice.ManagedClusterProperties{
			AADProfile: &armcontainerservice.ManagedClusterAADProfile{
				Managed:         ptr.To(true),
				EnableAzureRBAC: ptr.To(true),
			},
			AddonProfiles: map[string]*armcontainerservice.ManagedClusterAddonProfile{
				"azureKeyvaultSecretsProvider": {
					Enabled: ptr.To(true),
					Config: map[string]*string{
						"enableSecretRotation": ptr.To("true"),
						"rotationPollInterval": ptr.To("1h"),
					},
				},
				"omsagent": {
					Enabled: ptr.To(false),
				},
			},
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				toClusterAgentPoolProfile(bootstrap.Name, bootstrapProps),
			},
			AutoScalerProfile: &armcontainerservice.ManagedClusterPropertiesAutoScalerProfile{
				BalanceSimilarNodeGroups:          ptr.To("true"),
				DaemonsetEvictionForOccupiedNodes: ptr.To(true),
				ScanInterval:                      ptr.To("10s"),
				ScaleDownDelayAfterAdd:            ptr.To("30m"),
				ScaleDownDelayAfterDelete:         ptr.To("60s"),
				ScaleDownDelayAfterFailure:        ptr.To("3m"),
				ScaleDownUnneededTime:             ptr.To("30m"),
				ScaleDownUnreadyTime:              ptr.To("20m"),
				ScaleDownUtilizationThreshold:     ptr.To("0.3"),
				SkipNodesWithLocalStorage:         ptr.To("false"),
				MaxGracefulTerminationSec:         ptr.To("600"),
				MaxNodeProvisionTime:              ptr.To("15m"),
			},
			AutoUpgradeProfile: &armcontainerservice.ManagedClusterAutoUpgradeProfile{
				NodeOSUpgradeChannel: ptr.To(armcontainerservice.NodeOSUpgradeChannelNodeImage),
				UpgradeChannel:       ptr.To(armcontainerservice.UpgradeChannelPatch),
			},
			AzureMonitorProfile: &armcontainerservice.ManagedClusterAzureMonitorProfile{
				Metrics: &armcontainerservice.ManagedClusterAzureMonitorProfileMetrics{
					Enabled: ptr.To(true),
					KubeStateMetrics: &armcontainerservice.ManagedClusterAzureMonitorProfileKubeStateMetrics{
						MetricLabelsAllowlist:      ptr.To(o.metricLabelsAllowlist),
						MetricAnnotationsAllowList: ptr.To(o.metricAnnotationsAllowlist),
					},
				},
			},
			DisableLocalAccounts: ptr.To(true),
			DNSPrefix:            ptr.To(o.clusterName),
			EnableRBAC:           ptr.To(true),
			KubernetesVersion:    ptr.To(o.kubernetesVersion),
			MetricsProfile: &armcontainerservice.ManagedClusterMetricsProfile{
				CostAnalysis: &armcontainerservice.ManagedClusterCostAnalysis{
					Enabled: ptr.To(false),
				},
			},
			NetworkProfile: &armcontainerservice.NetworkProfile{
				AdvancedNetworking: advancedNetworking,
				IPFamilies:         []*armcontainerservice.IPFamily{ptr.To(armcontainerservice.IPFamilyIPv4)},
				LoadBalancerSKU:    ptr.To(armcontainerservice.LoadBalancerSKUStandard),
				LoadBalancerProfile: &armcontainerservice.ManagedClusterLoadBalancerProfile{
					OutboundIPs: &armcontainerservice.ManagedClusterLoadBalancerProfileOutboundIPs{
						PublicIPs: []*armcontainerservice.ResourceReference{
							{ID: ptr.To(o.outboundIPResourceID)},
						},
					},
				},
				NetworkDataplane: ptr.To(armcontainerservice.NetworkDataplane(o.networkDataplane)),
				NetworkPolicy:    ptr.To(armcontainerservice.NetworkPolicy(o.networkPolicy)),
				NetworkPlugin:    ptr.To(armcontainerservice.NetworkPluginAzure),
				ServiceCidr:      ptr.To("10.130.0.0/16"),
				ServiceCidrs:     []*string{ptr.To("10.130.0.0/16")},
				DNSServiceIP:     ptr.To("10.130.0.10"),
			},
			NodeResourceGroup: ptr.To(o.resourceGroup + "-aks1"),
			OidcIssuerProfile: &armcontainerservice.ManagedClusterOIDCIssuerProfile{
				Enabled: ptr.To(true),
			},
			SecurityProfile: &armcontainerservice.ManagedClusterSecurityProfile{
				AzureKeyVaultKms: &armcontainerservice.AzureKeyVaultKms{
					Enabled:               ptr.To(true),
					KeyID:                 ptr.To(o.etcdKMSKeyURI),
					KeyVaultNetworkAccess: ptr.To(armcontainerservice.KeyVaultNetworkAccessTypesPublic),
				},
				ImageCleaner: &armcontainerservice.ManagedClusterSecurityProfileImageCleaner{
					Enabled:       ptr.To(true),
					IntervalHours: ptr.To[int32](24),
				},
				WorkloadIdentity: &armcontainerservice.ManagedClusterSecurityProfileWorkloadIdentity{
					Enabled: ptr.To(true),
				},
			},
			ServicePrincipalProfile: &armcontainerservice.ManagedClusterServicePrincipalProfile{
				ClientID: ptr.To("msi"),
			},
			StorageProfile: &armcontainerservice.ManagedClusterStorageProfile{
				DiskCSIDriver:      &armcontainerservice.ManagedClusterStorageProfileDiskCSIDriver{Enabled: ptr.To(true)},
				FileCSIDriver:      &armcontainerservice.ManagedClusterStorageProfileFileCSIDriver{Enabled: ptr.To(true)},
				SnapshotController: &armcontainerservice.ManagedClusterStorageProfileSnapshotController{Enabled: ptr.To(true)},
			},
			SupportPlan: ptr.To(armcontainerservice.KubernetesSupportPlanKubernetesOfficial),
		},
	}
}
