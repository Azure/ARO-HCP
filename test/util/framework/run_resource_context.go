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

package framework

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/timing"
)

type RunResourceContext struct {
	resources         *perItOrDescribeTestContext
	manifest          SharedClusterManifest
	manifestPath      string
	artifacts         embed.FS
	logger            logr.Logger
	creationAttempted bool
}

func NewRunResourceContext(runID, manifestPath string, artifacts embed.FS, logger logr.Logger) (*RunResourceContext, error) {
	if runID == "" || !filepath.IsAbs(manifestPath) {
		return nil, fmt.Errorf("run resource context requires a run ID and absolute manifest path")
	}
	for _, template := range []string{"customer-infra", "managed-identities"} {
		if _, err := artifacts.ReadFile("test-artifacts/generated-test-artifacts/modules/" + template + ".json"); err != nil {
			return nil, fmt.Errorf("read fixture template %s (build with make -C test): %w", template, err)
		}
	}
	invocation, err := newInvocationContext()
	if err != nil {
		return nil, err
	}
	invocation.pooledIdentities = false
	resourceGroup := "shared-ro-" + rand.String(12)
	return &RunResourceContext{
		resources: &perItOrDescribeTestContext{
			perBinaryInvocationTestContext: invocation,
			timingMetadata:                 timing.SpecTimingMetadata{Deployments: make(map[string]map[string][]timing.Operation)},
		},
		manifest: SharedClusterManifest{
			Version: SharedFixtureManifestVersion, RunID: runID, OwnerPID: os.Getpid(), ExpiresAt: time.Now().Add(SharedFixtureLifetime),
			CustomerResourceGroup: resourceGroup, ManagedResourceGroup: resourceGroup + "-managed",
			ClusterName: "shared-read-only", NodePoolName: "shared-ro-np", Location: invocation.Location(),
			Environment: os.Getenv("AROHCP_ENV"), FrontendAddress: os.Getenv("FRONTEND_ADDRESS"),
			ResourceManagerEndpoint: os.Getenv("RESOURCE_MANAGER_ENDPOINT"),
		},
		manifestPath: manifestPath, artifacts: artifacts, logger: logger,
	}, nil
}

func (run *RunResourceContext) Provision(ctx context.Context) (SharedClusterManifest, error) {
	subscriptionID, err := run.resources.SubscriptionID(ctx)
	if err != nil {
		return run.manifest, err
	}
	run.manifest.SubscriptionID = subscriptionID
	run.manifest.ClusterResourceID = fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s", subscriptionID, run.manifest.CustomerResourceGroup, run.manifest.ClusterName)
	run.manifest.NodePoolResourceID = run.manifest.ClusterResourceID + "/nodePools/" + run.manifest.NodePoolName
	if err := WriteSharedClusterManifest(run.manifestPath, run.manifest); err != nil {
		return run.manifest, err
	}
	if sharedDir := SharedDir(); sharedDir != "" {
		if err := os.WriteFile(filepath.Join(sharedDir, "tracked-resource-group_"+run.manifest.CustomerResourceGroup), []byte(run.manifest.RunID), 0600); err != nil {
			return run.manifest, fmt.Errorf("track fixture RG for orphan cleanup: %w", err)
		}
	}
	factory, err := run.resources.GetARMResourcesClientFactory(ctx)
	if err != nil {
		return run.manifest, err
	}
	run.creationAttempted = true
	resourceGroup, err := CreateResourceGroup(ctx, factory.NewResourceGroupsClient(), run.manifest.CustomerResourceGroup, run.manifest.Location, SharedFixtureLifetime, 20*time.Minute,
		map[string]*string{SharedFixtureRunIDTag: to.Ptr(run.manifest.RunID)})
	if err != nil {
		return run.manifest, err
	}
	params := NewDefaultClusterParams20240610()
	params.ClusterName = run.manifest.ClusterName
	params.ManagedResourceGroupName = run.manifest.ManagedResourceGroup
	params, err = run.resources.CreateClusterCustomerResources20240610(ctx, resourceGroup, params, map[string]interface{}{}, run.artifacts, RBACScopeResourceGroup)
	if err != nil {
		return run.manifest, err
	}
	if err := run.resources.CreateHCPClusterFromParam20240610(ctx, run.logger, run.manifest.CustomerResourceGroup, params, ClusterCreationTimeout); err != nil {
		return run.manifest, err
	}
	hcpFactory, err := run.resources.Get20240610ClientFactory(ctx)
	if err != nil {
		return run.manifest, err
	}
	cluster, err := hcpFactory.NewHcpOpenShiftClustersClient().Get(ctx, run.manifest.CustomerResourceGroup, run.manifest.ClusterName, nil)
	if err != nil {
		return run.manifest, err
	}
	if cluster.ID == nil || !strings.EqualFold(*cluster.ID, run.manifest.ClusterResourceID) || cluster.Properties == nil || cluster.Properties.ProvisioningState == nil || *cluster.Properties.ProvisioningState != hcpsdk20240610preview.ProvisioningStateSucceeded {
		return run.manifest, fmt.Errorf("shared cluster GET did not confirm the expected identity and Succeeded provisioning state")
	}
	if _, err := factory.NewResourceGroupsClient().Get(ctx, run.manifest.ManagedResourceGroup, nil); err != nil {
		return run.manifest, fmt.Errorf("shared cluster managed resource group is not present: %w", err)
	}
	nodePoolParams := NewDefaultNodePoolParams20240610()
	nodePoolParams.NodePoolName = run.manifest.NodePoolName
	nodePoolParams.Replicas = 1
	nodePoolParams.VMSize, err = run.resources.SelectVMSize(ctx, DefaultWorkerVMSizeSelector())
	if err != nil {
		return run.manifest, fmt.Errorf("select shared node pool VM size: %w", err)
	}
	if err := provisionSharedNodePool20240610(ctx, hcpFactory.NewNodePoolsClient(), run.manifest, nodePoolParams); err != nil {
		return run.manifest, err
	}
	run.manifest.Ready = true
	return run.manifest, nil
}

func provisionSharedNodePool20240610(ctx context.Context, client *hcpsdk20240610preview.NodePoolsClient, manifest SharedClusterManifest, params NodePoolParams20240610) error {
	nodePool, err := CreateNodePoolAndWait20240610(ctx, client, manifest.CustomerResourceGroup, manifest.ClusterName, manifest.NodePoolName,
		BuildNodePoolFromParams20240610(params, manifest.Location), NodePoolCreationTimeout)
	if err != nil {
		return fmt.Errorf("provision shared node pool: %w", err)
	}
	if nodePool == nil || nodePool.ID == nil || !strings.EqualFold(*nodePool.ID, manifest.NodePoolResourceID) || nodePool.Properties == nil || nodePool.Properties.ProvisioningState == nil || *nodePool.Properties.ProvisioningState != hcpsdk20240610preview.ProvisioningStateSucceeded {
		return fmt.Errorf("shared node pool did not confirm the expected identity and Succeeded provisioning state")
	}
	return nil
}

func (run *RunResourceContext) Cleanup(ctx context.Context) error {
	if !run.creationAttempted {
		return nil
	}
	if run.resources.perBinaryInvocationTestContext.skipCleanup {
		run.logger.Info("fixture resources retained for debugging (ARO_E2E_SKIP_CLEANUP)",
			"runID", run.manifest.RunID,
			"resourceGroup", run.manifest.CustomerResourceGroup,
			"managedResourceGroup", run.manifest.ManagedResourceGroup)
		return nil
	}
	factory, err := run.resources.GetARMResourcesClientFactory(ctx)
	if err != nil {
		return err
	}
	group, err := factory.NewResourceGroupsClient().Get(ctx, run.manifest.CustomerResourceGroup, nil)
	if IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if group.Tags[SharedFixtureRunIDTag] == nil || *group.Tags[SharedFixtureRunIDTag] != run.manifest.RunID {
		return fmt.Errorf("refusing fixture cleanup: RG %q is not tagged for run %q", run.manifest.CustomerResourceGroup, run.manifest.RunID)
	}
	return run.resources.CleanupResourceGroups(ctx, CleanupResourceGroupsOptions{
		ResourceGroupNames: []string{run.manifest.CustomerResourceGroup}, Timeout: time.Hour,
		CleanupWorkflow: CleanupWorkflowStandard, Concurrency: 1,
	})
}
