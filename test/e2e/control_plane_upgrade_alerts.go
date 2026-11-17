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
				regionAlertName     = "HCPUpgradeVersionNewAlerts"
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
			ruleGroupName := "upgrade-alert-e2e-" + runID
			body := upgradeAlertInputRules(workspaceID, resourceID, tc.Location(), runID)
			deployCtx, cancelDeploy := context.WithTimeout(ctx, 5*time.Minute)
			defer cancelDeploy()
			// Register before PUT: an interrupted request may still create the resource.
			DeferCleanup(func(cleanupCtx context.Context) {
				cleanupCtx, cancel := context.WithTimeout(cleanupCtx, 5*time.Minute)
				defer cancel()
				Expect(workspace.DeleteRuleGroup(cleanupCtx, ruleGroupName)).To(Succeed(), "failed to delete test rule group %s", ruleGroupName)
			})
			Expect(workspace.CreateOrUpdateRuleGroup(deployCtx, ruleGroupName, body)).To(Succeed(), "failed to deploy input rule group %s", ruleGroupName)

			By("creating customer infrastructure")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx, rg, clusterParams, map[string]interface{}{}, TestArtifactsFS, framework.RBACScopeResourceGroup)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer infrastructure")

			By(fmt.Sprintf("creating the HCP at %s on the %s channel", installVersion, channelGroup))
			err = tc.CreateHCPClusterFromParam20240610(ctx, GinkgoLogr, *rg.Name, clusterParams, framework.ClusterCreationTimeout)
			Expect(err).NotTo(HaveOccurred(), "failed to create test HCP")
			hcpClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()

			By("verifying only the pre-upgrade alert is firing and no HCPUpgradeNewAlert fires for this HCP")
			_, err = workspace.WaitForAlerts(ctx, []framework.ExpectedAlert{
				{Name: preUpgradeAlert, Labels: map[string]string{"alertname": preUpgradeAlert, "resource_id": resourceID}, Firing: true},
				{Name: duringUpgradeAlert, Labels: map[string]string{"alertname": duringUpgradeAlert, "resource_id": resourceID}, Firing: false},
				{Name: postUpgradeAlert, Labels: map[string]string{"alertname": postUpgradeAlert, "test_run": runID}, Firing: false},
				{Name: upgradeAlertName, Labels: map[string]string{"alertname": upgradeAlertName, "resource_id": resourceID}, Firing: false},
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
			// Allow the remaining upgrade time plus the observation time: the upgrade or its metrics can start late.
			// The post-upgrade alert must not fire yet; it needs recorded completion, so this proves the
			// during-upgrade alert started before the upgrade completed.
			duringUpgradeTimeout := time.Until(upgradeStarted.Add(framework.HCPClusterVersionUpgradeTimeout + observationTimeout))
			_, err = workspace.WaitForAlerts(ctx, []framework.ExpectedAlert{
				{Name: preUpgradeAlert, Labels: map[string]string{"alertname": preUpgradeAlert, "resource_id": resourceID}, Firing: true},
				{Name: duringUpgradeAlert, Labels: map[string]string{"alertname": duringUpgradeAlert, "resource_id": resourceID}, Firing: true},
				{Name: postUpgradeAlert, Labels: map[string]string{"alertname": postUpgradeAlert, "test_run": runID}, Firing: false},
			}, duringUpgradeTimeout, GinkgoLogr)
			Expect(err).NotTo(HaveOccurred(), "expected the during-upgrade and pre-upgrade alerts firing after the upgrade starts")

			By("waiting for the upgrade API operation to finish")
			err = <-upgradeDone
			Expect(err).NotTo(HaveOccurred(), "upgrade API operation failed")

			By("waiting for upgrade completion to trigger the post-upgrade alert and HCPUpgradeNewAlert for exactly the new alerts")
			// The control plane can still be upgrading after the API operation finishes, so allow
			// the remaining upgrade time plus the observation time.
			postUpgradeTimeout := time.Until(upgradeStarted.Add(framework.HCPClusterVersionUpgradeTimeout + observationTimeout))
			firing, err := workspace.WaitForAlerts(ctx, []framework.ExpectedAlert{
				{Name: preUpgradeAlert, Labels: map[string]string{"alertname": preUpgradeAlert, "resource_id": resourceID}, Firing: true},
				{Name: duringUpgradeAlert, Labels: map[string]string{"alertname": duringUpgradeAlert, "resource_id": resourceID}, Firing: true},
				{Name: postUpgradeAlert, Labels: map[string]string{"alertname": postUpgradeAlert, "test_run": runID}, Firing: true},
				{Name: upgradeAlertName + " for " + duringUpgradeAlert, Labels: map[string]string{"alertname": upgradeAlertName, "resource_id": resourceID, "new_alertname": duringUpgradeAlert}, Firing: true},
				// The post-upgrade input carries no resource_id, so this also proves the namespace-annotation ownership path.
				{Name: upgradeAlertName + " for " + postUpgradeAlert, Labels: map[string]string{"alertname": upgradeAlertName, "resource_id": resourceID, "new_alertname": postUpgradeAlert}, Firing: true},
				{Name: upgradeAlertName + " for " + preUpgradeAlert, Labels: map[string]string{"alertname": upgradeAlertName, "resource_id": resourceID, "new_alertname": preUpgradeAlert}, Firing: false},
			}, postUpgradeTimeout, GinkgoLogr)
			Expect(err).NotTo(HaveOccurred(), "expected HCPUpgradeNewAlert for the during-upgrade and post-upgrade alerts only")

			// The RP selects the patch release; HCPUpgradeNewAlert carries the exact version from the HostedCluster history.
			upgradedVersion := firing[upgradeAlertName+" for "+duringUpgradeAlert][0]["version"]
			Expect(upgradedVersion).To(HavePrefix(upgradeVersion+"."), "HCPUpgradeNewAlert did not report an exact %s version", upgradeVersion)
			// The region alert counts per management cluster, so the cluster must survive from the triggering alert.
			managementCluster := firing[upgradeAlertName+" for "+duringUpgradeAlert][0]["cluster"]
			Expect(managementCluster).NotTo(BeEmpty(), "HCPUpgradeNewAlert did not carry the management cluster of its triggering alert")

			By(fmt.Sprintf("waiting for the region alert for %s in %s on %s", upgradedVersion, tc.Location(), managementCluster))
			// The region alert is not tied to this HCP: another HCP upgraded to the same version
			// on this management cluster could also make it fire.
			_, err = workspace.WaitForAlerts(ctx, []framework.ExpectedAlert{
				{Name: regionAlertName, Labels: map[string]string{"alertname": regionAlertName, "region": tc.Location(), "version": upgradedVersion, "cluster": managementCluster}, Firing: true},
			}, observationTimeout, GinkgoLogr)
			Expect(err).NotTo(HaveOccurred(), "expected %s firing for %s in %s on %s", regionAlertName, upgradedVersion, tc.Location(), managementCluster)
		})
})

// upgradeAlertInputRules creates inputs for a fresh HCP followed by one upgrade.
// Count distinct attempts, ignoring collector labels.
// The during-upgrade alert remains active after completion; the post-upgrade alert requires both attempts completed.
// The post-upgrade alert omits resource_id and fires in the control-plane namespace (HostedCluster
// namespace + "-" + name), like real control-plane alerts that rely on the Namespace annotation for ownership.
func upgradeAlertInputRules(workspaceID, resourceID, location, runID string) armresources.GenericResource {
	started := fmt.Sprintf("count by (namespace, cluster) (group by (namespace, cluster, attempt_started_at) (hostedClusterAPI_control_plane_version_history_started_timestamp_seconds{resource_id=%q}))", resourceID)
	completed := fmt.Sprintf("count by (namespace, cluster) (group by (namespace, cluster, attempt_started_at) (hostedClusterAPI_control_plane_version_history_completed_timestamp_seconds{resource_id=%q}))", resourceID)
	completedInControlPlaneNamespace := fmt.Sprintf(`count by (namespace, cluster) (group by (namespace, cluster, attempt_started_at) (label_join(hostedClusterAPI_control_plane_version_history_completed_timestamp_seconds{resource_id=%q}, "namespace", "-", "namespace", "name")))`, resourceID)
	inputs := []struct {
		name, expression string
		withResourceID   bool
	}{
		// History supplies the actual HostedCluster namespace and management cluster as soon as it is collected.
		{"TestAlertPreUpgrade", fmt.Sprintf("group by (namespace, cluster) (hostedClusterAPI_control_plane_version_history_started_timestamp_seconds{resource_id=%q})", resourceID), true},
		{"TestAlertPostUpgrade", completedInControlPlaneNamespace + " >= 2", false},
		{"TestAlertDuringUpgrade", started + " >= 2 and (" + completed + " >= 1)", true},
	}
	rules := make([]map[string]any, 0, 3)
	for _, input := range inputs {
		labels := map[string]string{"region": location, "test_run": runID}
		if input.withResourceID {
			labels["resource_id"] = resourceID
		}
		rules = append(rules, map[string]any{
			"alert":      input.name + "_" + runID,
			"expression": input.expression,
			"enabled":    true, "severity": 4, "for": "PT1M",
			"labels":               labels,
			"annotations":          map[string]string{"summary": "Synthetic upgrade HCPUpgradeNewAlert E2E input; no workload fault"},
			"resolveConfiguration": map[string]any{"autoResolved": true, "timeToResolve": "PT1M"},
		})
	}
	return armresources.GenericResource{Location: to.Ptr(location), Properties: map[string]any{
		"enabled": true, "scopes": []string{workspaceID}, "interval": "PT1M", "rules": rules,
	}}
}
