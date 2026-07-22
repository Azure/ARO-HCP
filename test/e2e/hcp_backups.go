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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type backupTestClusterConfig struct {
	resourceGroupPrefix string
	clusterName         string
	nsgName             string
	vnetName            string
	subnetName          string
}

type backupTestCluster struct {
	httpClient   *http.Client
	adminAPIAddr string
	resourceID   string
}

func createBackupTestCluster(ctx context.Context, cfg backupTestClusterConfig) backupTestCluster {
	tc := framework.NewTestContext()

	if tc.UsePooledIdentities() {
		err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
		Expect(err).NotTo(HaveOccurred(), "failed to assign identity containers")
	}

	By("creating a resource group")
	resourceGroup, err := tc.NewResourceGroup(ctx, cfg.resourceGroupPrefix, tc.Location())
	Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

	By("creating cluster parameters")
	clusterParams := framework.NewDefaultClusterParams20251223()
	clusterParams.ClusterName = cfg.clusterName
	managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
	clusterParams.ManagedResourceGroupName = managedResourceGroupName

	By("creating customer resources")
	clusterParams, err = tc.CreateClusterCustomerResources20251223(ctx,
		resourceGroup,
		clusterParams,
		map[string]interface{}{
			"customerNsgName":        cfg.nsgName,
			"customerVnetName":       cfg.vnetName,
			"customerVnetSubnetName": cfg.subnetName,
		},
		TestArtifactsFS,
		framework.RBACScopeResourceGroup,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create customer resources")

	By("creating the HCP cluster")
	err = tc.CreateHCPClusterFromParam20251223(
		ctx,
		GinkgoLogr,
		*resourceGroup.Name,
		clusterParams,
		nil,
		framework.ClusterCreationTimeout,
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster")

	hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
		api.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, cfg.clusterName)

	By("creating admin API HTTP client")
	httpClient, adminAPIAddress, err := tc.NewAdminAPIHTTPClient(ctx)
	Expect(err).NotTo(HaveOccurred(), "failed to create admin API HTTP client")

	return backupTestCluster{
		httpClient:   httpClient,
		adminAPIAddr: adminAPIAddress,
		resourceID:   hcpResourceID,
	}
}

var _ = Describe("Backups", func() {
	It("can be created on-demand for an HCP cluster",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.Slow,
		func(ctx context.Context) {
			cluster := createBackupTestCluster(ctx, backupTestClusterConfig{
				resourceGroupPrefix: "manual-bkp-e2e",
				clusterName:         "manual-bkp-cluster",
				nsgName:             "manual-bkp-nsg-name",
				vnetName:            "manual-bkp-vnet-name",
				subnetName:          "manual-bkp-vnet-subnet1",
			})

			By("creating a manual on-demand backup")
			createdBackup, err := createBackupViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to create on-demand backup")
			Expect(createdBackup.Name).NotTo(BeEmpty(), "on-demand backup name should not be empty")

			By(fmt.Sprintf("waiting for backup %s to complete", createdBackup.Name))
			var lastPhase string
			Eventually(func() (string, error) {
				resp, err := getBackupViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, createdBackup.Name)
				if err != nil {
					return "", err
				}
				if resp.Backup.Phase == "PartiallyFailed" || resp.Backup.Phase == "Failed" {
					return "", fmt.Errorf("backup %s reached terminal failure state: %s", createdBackup.Name, resp.Backup.Phase)
				}
				if resp.Backup.Phase != lastPhase {
					GinkgoLogr.Info("backup phase transition", "backup", createdBackup.Name, "phase", resp.Backup.Phase)
					lastPhase = resp.Backup.Phase
				}
				return resp.Backup.Phase, nil
			}, framework.BackupTimeout, framework.BackupWaitInterval).Should(Equal("Completed"), "on-demand backup should reach Completed phase")

			By("verifying completed backup details")
			getResp, err := getBackupViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, createdBackup.Name)
			Expect(err).NotTo(HaveOccurred(), "failed to get backup details")
			Expect(getResp.Backup.Name).To(Equal(createdBackup.Name), "backup name should match")
			Expect(getResp.Backup.Phase).To(Equal("Completed"), "backup phase should be Completed")
			Expect(getResp.Backup.StartTimestamp).NotTo(BeEmpty(), "backup start timestamp should be set")
			Expect(getResp.Backup.CompletionTimestamp).NotTo(BeEmpty(), "backup completion timestamp should be set")
		})

	It("schedule pause stops backup execution for an HCP cluster",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.Slow,
		func(ctx context.Context) {
			cluster := createBackupTestCluster(ctx, backupTestClusterConfig{
				resourceGroupPrefix: "pause-bkp-e2e",
				clusterName:         "pause-bkp-cluster",
				nsgName:             "pause-bkp-nsg-name",
				vnetName:            "pause-bkp-vnet-name",
				subnetName:          "pause-bkp-vnet-subnet1",
			})
			By("verifying backup schedules were created")
			Eventually(func() (bool, error) {
				resp, err := getBackupScheduleViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
				if err != nil {
					return false, err
				}
				if len(resp.Schedules) == 0 {
					return false, nil
				}
				return true, nil
			}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(),
				"schedules should have been created on the mgmt cluster")

			By("verifying testing cadence is present before timing-sensitive wait")
			schedResp, err := getBackupScheduleViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to get backup schedules for cadence check")
			if !hasTestingCadence(schedResp.Schedules) {
				Skip("no 5-minute backup schedule present; skipping timing-sensitive LastBackupTime assertion")
			}

			By("waiting for at least one scheduled backup to execute")
			Eventually(func() (bool, error) {
				resp, err := getBackupScheduleViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
				if err != nil {
					return false, err
				}
				if len(resp.Schedules) == 0 {
					return false, nil
				}
				for _, s := range resp.Schedules {
					if s.LastBackupTime != "" {
						return true, nil
					}
				}
				return false, nil
			}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(), "at least one schedule should have a LastBackupTime")

			By("pausing the backup schedule")
			patchResp, err := patchBackupScheduleViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, api.BackupScheduleStatePaused)
			Expect(err).NotTo(HaveOccurred(), "failed to pause backup schedule")
			Expect(patchResp.State).To(Equal(api.BackupScheduleStatePaused), "patch response state should be Paused")

			By("verifying backup schedule pause propagated to the mgmt cluster")
			var pausedBaselineTimes map[string]string
			Eventually(func() (bool, error) {
				resp, err := getBackupScheduleViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
				if err != nil {
					return false, err
				}
				if len(resp.Schedules) == 0 {
					return false, nil
				}
				for _, s := range resp.Schedules {
					if !s.Paused {
						return false, nil
					}
				}
				pausedBaselineTimes = collectLastBackupTimes(resp.Schedules)
				return true, nil
			}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(),
				"all velero schedules should have spec.paused=true on the mgmt cluster")

			By("verifying no new backups execute while paused")
			Consistently(func() (bool, error) {
				resp, err := getBackupScheduleViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
				if err != nil {
					return false, err
				}
				currentTimes := collectLastBackupTimes(resp.Schedules)
				for name, lastTime := range pausedBaselineTimes {
					if currentTime, ok := currentTimes[name]; ok && currentTime != lastTime {
						return false, fmt.Errorf("schedule %s LastBackupTime changed from %s to %s while paused", name, lastTime, currentTime)
					}
				}
				return true, nil
			}, framework.BackupWaitTimeout, framework.BackupWaitInterval).Should(BeTrue(), "no schedule should execute new backups while paused")
		})

})

func getBackupViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, backupName string) (api.GetBackupResponse, error) {
	return framework.DoAdminAPIRequest[api.GetBackupResponse](ctx, httpClient, http.MethodGet,
		fmt.Sprintf("%s/admin/v1/hcp%s/backups/%s", adminAPIAddress, resourceID, backupName), http.StatusOK, nil)
}

func createBackupViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string) (api.BackupResponse, error) {
	return framework.DoAdminAPIRequest[api.BackupResponse](ctx, httpClient, http.MethodPost,
		fmt.Sprintf("%s/admin/v1/hcp%s/backups", adminAPIAddress, resourceID), http.StatusAccepted, nil)
}

func getBackupScheduleViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string) (api.BackupScheduleResponse, error) {
	return framework.DoAdminAPIRequest[api.BackupScheduleResponse](ctx, httpClient, http.MethodGet,
		fmt.Sprintf("%s/admin/v1/hcp%s/backupschedules", adminAPIAddress, resourceID), http.StatusOK, nil)
}

func patchBackupScheduleViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string, state api.BackupScheduleState) (api.BackupSchedulePatchResponse, error) {
	bodyBytes, err := json.Marshal(api.BackupSchedulePatchRequest{State: state})
	if err != nil {
		return api.BackupSchedulePatchResponse{}, fmt.Errorf("failed to marshal patch request: %w", err)
	}
	return framework.DoAdminAPIRequest[api.BackupSchedulePatchResponse](ctx, httpClient, http.MethodPatch,
		fmt.Sprintf("%s/admin/v1/hcp%s/backupschedules", adminAPIAddress, resourceID), http.StatusOK,
		bytes.NewReader(bodyBytes))
}

func hasTestingCadence(schedules []api.BackupScheduleDetail) bool {
	for _, s := range schedules {
		if strings.Contains(s.Name, "5min") {
			return true
		}
	}
	return false
}

func collectLastBackupTimes(schedules []api.BackupScheduleDetail) map[string]string {
	times := make(map[string]string, len(schedules))
	for _, s := range schedules {
		times[s.Name] = s.LastBackupTime
	}
	return times
}
