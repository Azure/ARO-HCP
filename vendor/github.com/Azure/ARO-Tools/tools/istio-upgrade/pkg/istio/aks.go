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

package istio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

type AKSClientConfig struct {
	ARMPollTimeout       time.Duration
	ProvisioningTimeout  time.Duration
	ProvisioningInterval time.Duration
}

func DefaultAKSClientConfig() AKSClientConfig {
	return AKSClientConfig{
		ARMPollTimeout:       15 * time.Minute,
		ProvisioningTimeout:  5 * time.Minute,
		ProvisioningInterval: 15 * time.Second,
	}
}

var _ AKSClusterClient = (*AKSClient)(nil)

type AKSClient struct {
	client *armcontainerservice.ManagedClustersClient
	log    logr.Logger
	config AKSClientConfig
}

type ClusterInfo struct {
	Name              string
	KubernetesVersion string
	ProvisioningState string
}

type MeshProfile struct {
	Revisions []string
}

type MeshUpgradeInfo struct {
	AvailableUpgrades []string
	UpgradeInProgress bool
}

type AKSClusterClient interface {
	GetClusterState(ctx context.Context, resourceGroup, clusterName string) (*ClusterInfo, *MeshProfile, error)
	GetMeshUpgradeTargets(ctx context.Context, resourceGroup, clusterName string) (*MeshUpgradeInfo, error)
	EnableMesh(ctx context.Context, resourceGroup, clusterName, revision string) error
	StartCanaryUpgrade(ctx context.Context, resourceGroup, clusterName, newRevision string) error
	CompleteCanaryUpgrade(ctx context.Context, resourceGroup, clusterName, keepRevision string) error
}

func NewAKSClient(subscriptionID string, logger logr.Logger, config AKSClientConfig) (AKSClusterClient, error) {
	cred, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{RequireAzureTokenCredentials: true})
	if err != nil {
		return nil, fmt.Errorf("failed to create credential: %w", err)
	}
	client, err := armcontainerservice.NewManagedClustersClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create AKS client: %w", err)
	}
	return &AKSClient{client: client, log: logger, config: config}, nil
}

func (c *AKSClient) GetClusterState(ctx context.Context, resourceGroup, clusterName string) (*ClusterInfo, *MeshProfile, error) {
	resp, err := c.client.Get(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cluster %s: %w", clusterName, err)
	}

	info := &ClusterInfo{Name: clusterName}
	if resp.Properties != nil {
		info.KubernetesVersion = ptr.Deref(resp.Properties.KubernetesVersion, "")
		info.ProvisioningState = ptr.Deref(resp.Properties.ProvisioningState, "")
	}

	profile := &MeshProfile{}
	if resp.Properties != nil && resp.Properties.ServiceMeshProfile != nil &&
		resp.Properties.ServiceMeshProfile.Istio != nil {
		for _, rev := range resp.Properties.ServiceMeshProfile.Istio.Revisions {
			if rev != nil {
				profile.Revisions = append(profile.Revisions, *rev)
			}
		}
	}

	return info, profile, nil
}

func (c *AKSClient) GetMeshUpgradeTargets(ctx context.Context, resourceGroup, clusterName string) (*MeshUpgradeInfo, error) {
	pager := c.client.NewListMeshUpgradeProfilesPager(resourceGroup, clusterName, nil)
	info := &MeshUpgradeInfo{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == 409 &&
				strings.Contains(respErr.ErrorCode, "ServiceMeshUpgradeInProgress") {
				c.log.Info("Upgrade already in progress (409)", "cluster", clusterName)
				info.UpgradeInProgress = true
				return info, nil
			}
			return nil, fmt.Errorf("failed to list mesh upgrade profiles: %w", err)
		}
		for _, profile := range page.Value {
			if profile.Properties == nil {
				continue
			}
			for _, upgrade := range profile.Properties.Upgrades {
				if upgrade != nil {
					info.AvailableUpgrades = append(info.AvailableUpgrades, *upgrade)
				}
			}
		}
	}
	return info, nil
}

func (c *AKSClient) modifyClusterProperties(ctx context.Context, resourceGroup, clusterName, operation string, modify func(resp *armcontainerservice.ManagedClustersClientGetResponse) error) error {
	start := time.Now()
	c.log.Info("Starting ARM operation", "operation", operation, "cluster", clusterName)

	resp, err := c.client.Get(ctx, resourceGroup, clusterName, nil)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	if err := modify(&resp); err != nil {
		return err
	}

	poller, err := c.client.BeginCreateOrUpdate(ctx, resourceGroup, clusterName, resp.ManagedCluster, nil)
	if err != nil {
		return fmt.Errorf("%s: ARM request failed: %w", operation, err)
	}
	pollCtx, cancel := context.WithTimeout(ctx, c.config.ARMPollTimeout)
	defer cancel()
	if _, err = poller.PollUntilDone(pollCtx, nil); err != nil {
		return fmt.Errorf("%s: ARM operation timed out or failed: %w", operation, err)
	}

	if _, err := c.waitForProvisioning(ctx, resourceGroup, clusterName, c.config.ProvisioningTimeout); err != nil {
		return fmt.Errorf("%s: cluster not healthy after operation: %w", operation, err)
	}

	c.log.Info("ARM operation complete", "operation", operation, "elapsed", time.Since(start).Round(time.Second))
	return nil
}

func (c *AKSClient) EnableMesh(ctx context.Context, resourceGroup, clusterName, revision string) error {
	return c.modifyClusterProperties(ctx, resourceGroup, clusterName, "enable-mesh", func(resp *armcontainerservice.ManagedClustersClientGetResponse) error {
		if resp.Properties == nil {
			return fmt.Errorf("cluster properties are nil")
		}
		resp.Properties.ServiceMeshProfile = &armcontainerservice.ServiceMeshProfile{
			Mode: ptr.To(armcontainerservice.ServiceMeshModeIstio),
			Istio: &armcontainerservice.IstioServiceMesh{
				Revisions: []*string{ptr.To(revision)},
				Components: &armcontainerservice.IstioComponents{
					IngressGateways: []*armcontainerservice.IstioIngressGateway{
						{Enabled: ptr.To(true), Mode: ptr.To(armcontainerservice.IstioIngressGatewayModeExternal)},
					},
				},
			},
		}
		return nil
	})
}

func (c *AKSClient) StartCanaryUpgrade(ctx context.Context, resourceGroup, clusterName, newRevision string) error {
	return c.modifyClusterProperties(ctx, resourceGroup, clusterName, "start-canary", func(resp *armcontainerservice.ManagedClustersClientGetResponse) error {
		if resp.Properties == nil || resp.Properties.ServiceMeshProfile == nil ||
			resp.Properties.ServiceMeshProfile.Istio == nil {
			return fmt.Errorf("cluster has no service mesh profile")
		}
		for _, rev := range resp.Properties.ServiceMeshProfile.Istio.Revisions {
			if rev != nil && *rev == newRevision {
				return fmt.Errorf("revision %s is already installed", newRevision)
			}
		}
		resp.Properties.ServiceMeshProfile.Istio.Revisions = append(
			resp.Properties.ServiceMeshProfile.Istio.Revisions, ptr.To(newRevision),
		)
		return nil
	})
}

func (c *AKSClient) CompleteCanaryUpgrade(ctx context.Context, resourceGroup, clusterName, keepRevision string) error {
	return c.modifyClusterProperties(ctx, resourceGroup, clusterName, "complete-canary", func(resp *armcontainerservice.ManagedClustersClientGetResponse) error {
		if resp.Properties == nil || resp.Properties.ServiceMeshProfile == nil ||
			resp.Properties.ServiceMeshProfile.Istio == nil {
			return fmt.Errorf("cluster has no service mesh profile")
		}
		resp.Properties.ServiceMeshProfile.Istio.Revisions = []*string{ptr.To(keepRevision)}
		return nil
	})
}

func (c *AKSClient) waitForProvisioning(ctx context.Context, resourceGroup, clusterName string, timeout time.Duration) (*ClusterInfo, error) {
	var lastState string
	var lastErr error
	var result *ClusterInfo

	err := wait.PollUntilContextTimeout(ctx, c.config.ProvisioningInterval, timeout, true, func(ctx context.Context) (bool, error) {
		info, _, err := c.GetClusterState(ctx, resourceGroup, clusterName)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && (respErr.StatusCode == 404 || respErr.StatusCode == 403) {
				return false, fmt.Errorf("permanent error polling cluster %s: %w", clusterName, err)
			}
			lastErr = err
			c.log.Error(err, "Transient error polling cluster", "cluster", clusterName)
			return false, nil
		}
		lastErr = nil
		if info.ProvisioningState != lastState {
			c.log.Info("Cluster provisioning state", "state", info.ProvisioningState)
			lastState = info.ProvisioningState
		}
		if info.ProvisioningState == "Succeeded" {
			result = info
			return true, nil
		}
		if info.ProvisioningState == "Failed" {
			return false, fmt.Errorf("cluster entered Failed state")
		}
		if info.ProvisioningState == "Canceled" {
			return false, fmt.Errorf("cluster provisioning was canceled")
		}
		return false, nil
	})
	if err != nil {
		if lastErr != nil {
			return nil, fmt.Errorf("timeout waiting for cluster to reach Succeeded (current: %s, lastErr: %v): %w", lastState, lastErr, err)
		}
		return nil, err
	}
	return result, nil
}
