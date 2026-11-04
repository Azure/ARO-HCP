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

package e2e

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/rand"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("Engineering", func() {
	It("should detect new alerts during and after a control plane upgrade",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.RequiresConfig,
		labels.MIContainers(1),
		labels.Slow,
		func(ctx context.Context) {
			const (
				customerClusterName = "upgrade-alert"
				upgradeAlertName    = "HCPUpgradeNewAlert"
				channelGroup        = "candidate"
				installVersion      = "4.21"
				upgradeVersion      = "4.22"
				observationTimeout  = 25 * time.Minute
			)
			tc := framework.NewTestContext()

			By("preparing test identities and the HCP monitoring workspace")
			if tc.UsePooledIdentities() {
				Expect(tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)).To(Succeed(), "failed to assign test identities")
			}
			workspace, err := tc.GetHCPMonitoringWorkspace(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get HCP monitoring workspace")

			By("creating a resource group")
			rg, err := tc.NewResourceGroup(ctx, "upgrade-alert", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resource group")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*rg.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName
			clusterParams.OpenshiftVersionId = installVersion
			clusterParams.ChannelGroup = channelGroup

			customerSubscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to resolve customer subscription ID")
			resourceID := strings.ToLower(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/%s", customerSubscriptionID, *rg.Name, clusterParams.ClusterName))

			By("deploying pre-upgrade, during-upgrade and post-upgrade alert rules")
			runID := rand.String(8)
			preUpgradeAlert := "TestAlertPreUpgrade_" + runID
			postUpgradeAlert := "TestAlertPostUpgrade_" + runID
			duringUpgradeAlert := "TestAlertDuringUpgrade_" + runID
			workspaceID := workspace.ResourceID()
			ruleNamePrefix := "upgrade-alert-e2e-" + runID
			ruleNames := map[string]string{
				preUpgradeAlert:    ruleNamePrefix + "-pre-upgrade",
				postUpgradeAlert:   ruleNamePrefix + "-post-upgrade",
				duringUpgradeAlert: ruleNamePrefix + "-during-upgrade",
			}
			body := upgradeAlertInputRules(workspaceID, resourceID, tc.Location(), runID)
			deployCtx, cancelDeploy := context.WithTimeout(ctx, 5*time.Minute)
			defer cancelDeploy()
			// Separate groups let us remove each alert without reloading the others.
			for _, rule := range body.Properties.(map[string]any)["rules"].([]map[string]any) {
				ruleName := ruleNames[rule["alert"].(string)]
				// Register before PUT: an interrupted request may still create the resource.
				DeferCleanup(func(cleanupCtx context.Context) {
					cleanupCtx, cancel := context.WithTimeout(cleanupCtx, 5*time.Minute)
					defer cancel()
					Expect(workspace.DeleteRuleGroup(cleanupCtx, ruleName)).To(Succeed(), "failed to delete test rule group %s", ruleName)
				})
				groupBody := body
				properties := maps.Clone(body.Properties.(map[string]any))
				properties["rules"] = []map[string]any{rule}
				groupBody.Properties = properties
				Expect(workspace.CreateOrUpdateRuleGroup(deployCtx, ruleName, groupBody)).To(Succeed(), "failed to deploy input rule group %s", ruleName)
			}

			By("creating customer infrastructure")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx, rg, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer infrastructure")

			By(fmt.Sprintf("creating the HCP at %s on the %s channel", installVersion, channelGroup))
			err = tc.CreateHCPClusterFromParam20240610(ctx, GinkgoLogr, *rg.Name, clusterParams, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create test HCP")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("verifying only the pre-upgrade alert is firing and the HCPUpgradeNewAlert is inactive")
			err = workspace.WaitForAlerts(ctx, resourceID, map[string]bool{
				preUpgradeAlert: true, duringUpgradeAlert: false, postUpgradeAlert: false, upgradeAlertName: false,
			}, observationTimeout, GinkgoLogr)
			Expect(err).NotTo(HaveOccurred(), "expected only the pre-upgrade alert firing before upgrade")

			By(fmt.Sprintf("starting the control plane upgrade to %s", upgradeVersion))
			upgradeCtx, cancelUpgrade := context.WithTimeout(ctx, framework.HCPClusterVersionUpgradeTimeout)
			defer cancelUpgrade()
			versionLine, tags := framework.ControlPlaneExactVersionPatchTags(upgradeVersion)
			update := hcpsdk20240610preview.HcpOpenShiftClusterUpdate{
				Tags: framework.TagsForPatch(clusterParams.Tags, tags),
				Properties: &hcpsdk20240610preview.HcpOpenShiftClusterPropertiesUpdate{
					Version: &hcpsdk20240610preview.VersionProfile{
						ID:           to.Ptr(versionLine),
						ChannelGroup: to.Ptr(clusterParams.ChannelGroup),
					},
				},
			}
			update.Tags[metadataapi.TagClusterMaxUpdateDuration] = to.Ptr((framework.HCPClusterVersionUpgradeTimeout - time.Minute).String())
			upgradeStarted := time.Now()
			upgrade, err := hcpClient.BeginUpdate(upgradeCtx, *rg.Name, clusterParams.ClusterName, update, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to start control plane upgrade to %s", upgradeVersion)
			// The API operation can finish before the control plane finishes upgrading.
			upgradeDone := make(chan error, 1)
			go func() {
				_, err := upgrade.PollUntilDone(upgradeCtx, nil)
				GinkgoLogr.Info("Upgrade API operation finished; control plane completion is not yet verified", "target_version", upgradeVersion, "api_operation_duration", time.Since(upgradeStarted).String(), "error", err)
				upgradeDone <- err
			}()

			By("waiting for the during-upgrade alert while the pre-upgrade alert remains firing")
			err = workspace.WaitForAlerts(upgradeCtx, resourceID, map[string]bool{
				preUpgradeAlert: true, duringUpgradeAlert: true,
			}, observationTimeout, GinkgoLogr)
			Expect(err).NotTo(HaveOccurred(), "expected the during-upgrade and pre-upgrade alerts firing after the upgrade starts")

			By("waiting for the upgrade API operation to finish")
			err = <-upgradeDone
			Expect(err).NotTo(HaveOccurred(), "upgrade API operation failed")

			By("waiting for upgrade completion to trigger the post-upgrade alert and for all actual alerts and the production HCPUpgradeNewAlert to fire")
			err = workspace.WaitForAlerts(ctx, resourceID, map[string]bool{
				preUpgradeAlert: true, duringUpgradeAlert: true, postUpgradeAlert: true, upgradeAlertName: true,
			}, observationTimeout, GinkgoLogr)
			Expect(err).NotTo(HaveOccurred(), "expected all source alerts and the production HCPUpgradeNewAlert firing after upgrade")

			// Resolve the post-upgrade alert first; the during-upgrade alert must keep the HCPUpgradeNewAlert firing.
			for _, name := range []string{postUpgradeAlert, duringUpgradeAlert} {
				expectUpgradeAlertFiring := name == postUpgradeAlert
				By(fmt.Sprintf("removing %s and verifying HCPUpgradeNewAlert firing=%t", name, expectUpgradeAlertFiring))
				deleteCtx, cancelDelete := context.WithTimeout(ctx, 5*time.Minute)
				defer cancelDelete()
				resolutionStarted := time.Now()
				err = workspace.DeleteRuleGroup(deleteCtx, ruleNames[name])
				cancelDelete()
				Expect(err).NotTo(HaveOccurred(), "failed to remove synthetic alert %s", name)
				GinkgoLogr.Info("Synthetic alert rule group deleted", "alertname", name, "duration", time.Since(resolutionStarted).String())

				err = workspace.WaitForAlerts(ctx, resourceID, map[string]bool{
					preUpgradeAlert: true, postUpgradeAlert: false,
					duringUpgradeAlert: expectUpgradeAlertFiring, upgradeAlertName: expectUpgradeAlertFiring,
				}, observationTimeout, GinkgoLogr)
				Expect(err).NotTo(HaveOccurred(), "incorrect alert states after removing %s", name)
				GinkgoLogr.Info("Verified alerts after rule deletion", "removed_alert", name, "hcp_upgrade_new_alert_firing", expectUpgradeAlertFiring, "total_resolution_wait", time.Since(resolutionStarted).String())
			}
		})
})

// upgradeAlertInputRules creates inputs for a fresh HCP followed by one upgrade.
// Count distinct attempts, ignoring collector labels.
// The during-upgrade alert remains active after completion; the post-upgrade alert requires both attempts completed.
func upgradeAlertInputRules(workspaceID, resourceID, location, runID string) armresources.GenericResource {
	started := fmt.Sprintf("count by (namespace) (group by (namespace, attempt_started_at) (hostedClusterAPI_control_plane_version_history_started_timestamp_seconds{resource_id=%q}))", resourceID)
	completed := fmt.Sprintf("count by (namespace) (group by (namespace, attempt_started_at) (hostedClusterAPI_control_plane_version_history_completed_timestamp_seconds{resource_id=%q}))", resourceID)
	inputs := []struct{ name, expression string }{
		// History supplies the actual HostedCluster namespace as soon as it is collected.
		{"TestAlertPreUpgrade", fmt.Sprintf("group by (namespace) (hostedClusterAPI_control_plane_version_history_started_timestamp_seconds{resource_id=%q})", resourceID)},
		{"TestAlertPostUpgrade", completed + " >= 2"},
		{"TestAlertDuringUpgrade", started + " >= 2 and (" + completed + " >= 1)"},
	}
	rules := make([]map[string]any, 0, 3)
	for _, input := range inputs {
		rules = append(rules, map[string]any{
			"alert":      input.name + "_" + runID,
			"expression": input.expression,
			"enabled":    true, "severity": 4, "for": "PT1M",
			"labels":               map[string]string{"resource_id": resourceID, "region": location, "test_run": runID},
			"annotations":          map[string]string{"summary": "Synthetic upgrade HCPUpgradeNewAlert E2E input; no workload fault"},
			"resolveConfiguration": map[string]any{"autoResolved": true, "timeToResolve": "PT1M"},
		})
	}
	return armresources.GenericResource{Location: to.Ptr(location), Properties: map[string]any{
		"enabled": true, "scopes": []string{workspaceID}, "interval": "PT1M", "rules": rules,
	}}
}
