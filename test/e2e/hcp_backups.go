// Copyright 2025 Microsoft Corporation
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
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

type backupResponse struct {
	Name                string `json:"name"`
	StartTimestamp      string `json:"startTimestamp"`
	CompletionTimestamp string `json:"completionTimestamp"`
	Phase               string `json:"phase"`
}

type listBackupsResponse struct {
	ResourceID string           `json:"resourceID"`
	Backups    []backupResponse `json:"backups"`
}

type getBackupResponse struct {
	ResourceID string         `json:"resourceID"`
	Backup     backupResponse `json:"backup"`
}

type backupProfileResponse struct {
	ResourceID       string `json:"resourceID"`
	State            string `json:"state"`
	LastBackupTime   string `json:"lastBackupTime,omitempty"`
	LastBackupStatus string `json:"lastBackupStatus,omitempty"`
}

type backupProfilePatchRequest struct {
	State string `json:"state"`
}

const (
	backupTimeout             = 10 * time.Minute
	pauseVerificationDuration = 10 * time.Minute
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
		err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
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
		45*time.Minute,
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
	It("should be created by the schedule for an HCP cluster",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			cluster := createBackupTestCluster(ctx, backupTestClusterConfig{
				resourceGroupPrefix: "backup-e2e",
				clusterName:         "backup-hcp-cluster",
				nsgName:             "backup-nsg-name",
				vnetName:            "backup-vnet-name",
				subnetName:          "backup-vnet-subnet1",
			})

			By("waiting for at least one backup to appear")
			var backups []backupResponse
			Eventually(func() (int, error) {
				resp, err := listBackupsViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
				if err != nil {
					return 0, err
				}
				backups = resp.Backups
				return len(backups), nil
			}, 10*time.Minute, 30*time.Second).Should(BeNumerically(">", 0), "expected at least one backup to be created by the schedule")

			By("waiting for a backup to complete")
			var completedBackupName string
			Eventually(func() (string, error) {
				resp, err := listBackupsViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
				if err != nil {
					return "", err
				}
				for _, b := range resp.Backups {
					if b.Phase == "Completed" {
						completedBackupName = b.Name
						return b.Phase, nil
					}
					if b.Phase == "PartiallyFailed" || b.Phase == "Failed" {
						return "", fmt.Errorf("backup %s reached terminal failure state: %s", b.Name, b.Phase)
					}
				}
				phases := make([]string, len(resp.Backups))
				for i, b := range resp.Backups {
					phases[i] = fmt.Sprintf("%s=%s", b.Name, b.Phase)
				}
				return fmt.Sprintf("no completed backup yet: %v", phases), nil
			}, backupTimeout, 15*time.Second).Should(Equal("Completed"), "expected a backup to reach Completed phase")

			By(fmt.Sprintf("verifying backup %s via get endpoint", completedBackupName))
			getResp, err := getBackupViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, completedBackupName)
			Expect(err).NotTo(HaveOccurred(), "failed to get backup via admin API")
			Expect(getResp.Backup.Name).To(Equal(completedBackupName), "backup name should match")
			Expect(getResp.Backup.Phase).To(Equal("Completed"), "backup phase should be Completed")
			Expect(getResp.Backup.StartTimestamp).NotTo(BeEmpty(), "backup start timestamp should be set")
			Expect(getResp.Backup.CompletionTimestamp).NotTo(BeEmpty(), "backup completion timestamp should be set")
		})

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
			Eventually(func() (string, error) {
				resp, err := getBackupViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, createdBackup.Name)
				if err != nil {
					return "", err
				}
				if resp.Backup.Phase == "PartiallyFailed" || resp.Backup.Phase == "Failed" {
					return "", fmt.Errorf("backup %s reached terminal failure state: %s", createdBackup.Name, resp.Backup.Phase)
				}
				return resp.Backup.Phase, nil
			}, backupTimeout, 15*time.Second).Should(Equal("Completed"), "on-demand backup should reach Completed phase")

			By("verifying completed backup details")
			getResp, err := getBackupViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, createdBackup.Name)
			Expect(err).NotTo(HaveOccurred(), "failed to get backup details")
			Expect(getResp.Backup.Name).To(Equal(createdBackup.Name), "backup name should match")
			Expect(getResp.Backup.Phase).To(Equal("Completed"), "backup phase should be Completed")
			Expect(getResp.Backup.StartTimestamp).NotTo(BeEmpty(), "backup start timestamp should be set")
			Expect(getResp.Backup.CompletionTimestamp).NotTo(BeEmpty(), "backup completion timestamp should be set")
		})

	It("schedules can be paused and activated via the admin API",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.CoreInfraService,
		labels.DevelopmentOnly,
		labels.AroRpApiCompatible,
		labels.Slow,
		func(ctx context.Context) {
			cluster := createBackupTestCluster(ctx, backupTestClusterConfig{
				resourceGroupPrefix: "profile-e2e",
				clusterName:         "profile-hcp-cluster",
				nsgName:             "profile-nsg-name",
				vnetName:            "profile-vnet-name",
				subnetName:          "profile-vnet-subnet1",
			})

			By("verifying default backup profile state is Active")
			profile, err := getBackupProfileViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to get backup profile")
			Expect(profile.ResourceID).NotTo(BeEmpty(), "backup profile resource ID should not be empty")
			Expect(profile.State).To(Equal("Active"), "default backup profile state should be Active")

			By("pausing the backup schedule")
			profile, err = patchBackupProfileViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, "Paused")
			Expect(err).NotTo(HaveOccurred(), "failed to pause backup schedule")
			Expect(profile.State).To(Equal("Paused"), "backup profile state should be Paused after patching")

			By("verifying backup profile state persisted as Paused")
			profile, err = getBackupProfileViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to get backup profile after pausing")
			Expect(profile.State).To(Equal("Paused"), "backup profile state should persist as Paused")

			By("recording backup count at time of pause")
			pauseListResp, err := listBackupsViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to list backups after pausing")
			countAtPause := len(pauseListResp.Backups)
			GinkgoLogr.Info("backup count at time of pause", "count", countAtPause)

			By(fmt.Sprintf("verifying no new backups are created beyond race condition allowance (count at pause: %d, max allowed: %d)", countAtPause, countAtPause+1))
			Consistently(func() (int, error) {
				resp, listErr := listBackupsViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
				if listErr != nil {
					return 0, listErr
				}
				return len(resp.Backups), nil
			}, pauseVerificationDuration, 30*time.Second).Should(
				BeNumerically("<=", countAtPause+1),
				fmt.Sprintf("expected backup count to stay at or below %d while schedule is paused (allowing +1 for race condition), but more backups were created", countAtPause+1),
			)

			By("activating the backup schedule")
			profile, err = patchBackupProfileViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, "Active")
			Expect(err).NotTo(HaveOccurred(), "failed to activate backup schedule")
			Expect(profile.State).To(Equal("Active"), "backup profile state should be Active after patching")

			By("verifying backup profile state persisted as Active")
			profile, err = getBackupProfileViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID)
			Expect(err).NotTo(HaveOccurred(), "failed to get backup profile after activating")
			Expect(profile.State).To(Equal("Active"), "backup profile state should persist as Active")

			By("verifying invalid state returns 400")
			statusCode, _, err := patchBackupProfileRawViaAdminAPI(ctx, cluster.httpClient, cluster.adminAPIAddr, cluster.resourceID, "InvalidState")
			Expect(err).NotTo(HaveOccurred(), "failed to send patch request with invalid state")
			Expect(statusCode).To(Equal(http.StatusBadRequest), "invalid backup profile state should return 400")
		})
})

func doAdminAPIRequest[T any](ctx context.Context, httpClient *http.Client, method, url string, expectedStatus int, body io.Reader) (T, error) {
	var zero T

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return zero, fmt.Errorf("failed to create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expectedStatus {
		respBody, _ := io.ReadAll(resp.Body)
		return zero, fmt.Errorf("expected status %d, got %d: %s", expectedStatus, resp.StatusCode, string(respBody))
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return zero, fmt.Errorf("failed to decode response: %w", err)
	}
	return result, nil
}

func listBackupsViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string) (listBackupsResponse, error) {
	return doAdminAPIRequest[listBackupsResponse](ctx, httpClient, http.MethodGet,
		fmt.Sprintf("%s/admin/v1/hcp%s/backups", adminAPIAddress, resourceID), http.StatusOK, nil)
}

func getBackupViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, backupName string) (getBackupResponse, error) {
	return doAdminAPIRequest[getBackupResponse](ctx, httpClient, http.MethodGet,
		fmt.Sprintf("%s/admin/v1/hcp%s/backups/%s", adminAPIAddress, resourceID, backupName), http.StatusOK, nil)
}

func createBackupViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string) (backupResponse, error) {
	return doAdminAPIRequest[backupResponse](ctx, httpClient, http.MethodPost,
		fmt.Sprintf("%s/admin/v1/hcp%s/backups", adminAPIAddress, resourceID), http.StatusCreated, nil)
}

func getBackupProfileViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string) (backupProfileResponse, error) {
	return doAdminAPIRequest[backupProfileResponse](ctx, httpClient, http.MethodGet,
		fmt.Sprintf("%s/admin/v1/hcp%s/backupProfile", adminAPIAddress, resourceID), http.StatusOK, nil)
}

func patchBackupProfileViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, state string) (backupProfileResponse, error) {
	body, err := json.Marshal(backupProfilePatchRequest{State: state})
	if err != nil {
		return backupProfileResponse{}, fmt.Errorf("failed to marshal request body: %w", err)
	}
	return doAdminAPIRequest[backupProfileResponse](ctx, httpClient, http.MethodPatch,
		fmt.Sprintf("%s/admin/v1/hcp%s/backupProfile", adminAPIAddress, resourceID), http.StatusOK, bytes.NewReader(body))
}

func patchBackupProfileRawViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, state string) (int, string, error) {
	url := fmt.Sprintf("%s/admin/v1/hcp%s/backupProfile", adminAPIAddress, resourceID)

	body, err := json.Marshal(backupProfilePatchRequest{State: state})
	if err != nil {
		return 0, "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody), nil
}
