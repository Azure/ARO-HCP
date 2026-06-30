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
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

type recoveryTestEnv struct {
	KubeClient       kubernetes.Interface
	Suffix           string
	ClusterName      string
	ResourceGroup    string
	HCPResourceID    string
	HTTPClient       *http.Client
	AdminAPIAddress  string
	OpenShiftVersion string
	ChannelGroup     string
}

type recoveryScenario struct {
	preBackup   func(ctx context.Context, env *recoveryTestEnv)
	postRestore func(ctx context.Context, env *recoveryTestEnv)
}

var _ = Describe("HCP Recovery", func() {
	DescribeTable("should recover an HCP cluster",
		func(ctx context.Context, version, channelGroup string, scenario recoveryScenario) {
			suffix := rand.String(6)
			clusterName := "recovery-" + suffix

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By(fmt.Sprintf("resolving latest %s %s version", version, channelGroup))
			openShiftVersion, err := framework.GetLatestInstallVersion(ctx, channelGroup, version)
			if err != nil {
				if errors.Is(err, framework.ErrNightlyReleaseStreamNotFound) ||
					errors.Is(err, framework.ErrNoAcceptedNightlyTags) ||
					errors.Is(err, framework.ErrVersionNotFound) {
					Skip(fmt.Sprintf("No install version found for %s in %s channel: %s", version, channelGroup, err.Error()))
				} else {
					Fail(fmt.Sprintf("failed to get latest install version for %s %s: %s", version, channelGroup, err.Error()))
				}
			}

			By("creating resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "hcp-recovery", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = clusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name+"-"+suffix, "-managed", 64)
			clusterParams.OpenshiftVersionId = openShiftVersion
			clusterParams.ChannelGroup = channelGroup

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating HCP cluster with version %q on %s channel", clusterParams.OpenshiftVersionId, channelGroup))
			err = tc.CreateHCPClusterFromParam20240610(
				ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster is viable")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			By("creating a NodePool")
			nodePoolParams := framework.NewDefaultNodePoolParams20240610()
			nodePoolParams.NodePoolName = "np-1"
			nodePoolParams.ClusterName = clusterName
			nodePoolParams.Replicas = int32(2)
			nodePoolParams.OpenshiftVersionId = openShiftVersion
			nodePoolParams.ChannelGroup = channelGroup
			err = tc.CreateNodePoolFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams.ManagedResourceGroupName,
				clusterName,
				nodePoolParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())

			hcpResourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/%s",
				api.Must(tc.SubscriptionID(ctx)), *resourceGroup.Name, clusterName)

			httpClient, adminAPIAddress, err := tc.NewAdminAPIHTTPClient(ctx)
			Expect(err).NotTo(HaveOccurred())

			env := &recoveryTestEnv{
				KubeClient:       kubeClient,
				Suffix:           suffix,
				ClusterName:      clusterName,
				ResourceGroup:    *resourceGroup.Name,
				HCPResourceID:    hcpResourceID,
				HTTPClient:       httpClient,
				AdminAPIAddress:  adminAPIAddress,
				OpenShiftVersion: openShiftVersion,
				ChannelGroup:     channelGroup,
			}

			By("running pre-backup scenario setup")
			scenario.preBackup(ctx, env)

			By("taking a backup using the admin API on-demand backup endpoint")
			createdBackup, err := createBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID)
			Expect(err).NotTo(HaveOccurred())
			Expect(createdBackup.Name).NotTo(BeEmpty())

			By(fmt.Sprintf("waiting for backup %s to complete", createdBackup.Name))
			Eventually(func() (string, error) {
				resp, err := getBackupViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, createdBackup.Name)
				if err != nil {
					return "", err
				}
				return resp.Backup.Phase, nil
			}, backupTimeout, 15*time.Second).Should(Equal("Completed"))

			By(fmt.Sprintf("creating a restore from backup %s", createdBackup.Name))
			restoreResp, err := createRestoreViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID, createdBackup.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(restoreResp.RecoveryState).To(Equal("Pending"))

			By("waiting for the restore to complete")
			var previousState string
			Eventually(func() (string, error) {
				resp, err := getRestoreStatusViaAdminAPI(ctx, httpClient, adminAPIAddress, hcpResourceID)
				if err != nil {
					return "", err
				}
				if resp.RecoveryState != previousState {
					GinkgoWriter.Printf("restore state changed: %s -> %s (phase: %s, lastCondition: %s)\n",
						previousState, resp.RecoveryState, resp.Phase, resp.LastCondition)
					previousState = resp.RecoveryState
				}
				if resp.RecoveryState == "Failed" {
					return "", fmt.Errorf("restore failed: lastCondition=%s, phase=%s", resp.LastCondition, resp.Phase)
				}
				return resp.RecoveryState, nil
			}, 60*time.Minute, 30*time.Second).Should(Equal("Completed"))

			By("getting fresh admin credentials after restore")
			adminRESTConfig, err = tc.GetAdminRESTConfigForHCPCluster20240610(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				clusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			kubeClient, err = kubernetes.NewForConfig(adminRESTConfig)
			Expect(err).NotTo(HaveOccurred())
			env.KubeClient = kubeClient

			By("running post-restore scenario verification")
			scenario.postRestore(ctx, env)
		},

		Entry("with a ConfigMap on 4.22 nightly",
			labels.RequireNothing,
			labels.Critical,
			labels.Positive,
			labels.AroRpApiCompatible,
			labels.DevelopmentOnly,
			"4.22", "nightly",
			recoveryScenario{
				preBackup: func(ctx context.Context, env *recoveryTestEnv) {
					By("deploying a test ConfigMap")
					testConfigMap := &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "recovery-test-cm",
							Namespace: "default",
						},
						Data: map[string]string{
							"test-key": "test-value-" + env.Suffix,
						},
					}
					_, err := env.KubeClient.CoreV1().ConfigMaps("default").Create(ctx, testConfigMap, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())
				},
				postRestore: func(ctx context.Context, env *recoveryTestEnv) {
					By("validating the ConfigMap is present after restore")
					Eventually(func() error {
						cm, err := env.KubeClient.CoreV1().ConfigMaps("default").Get(ctx, "recovery-test-cm", metav1.GetOptions{})
						if err != nil {
							return fmt.Errorf("failed to get ConfigMap default/recovery-test-cm: %w", err)
						}
						expected := "test-value-" + env.Suffix
						if cm.Data["test-key"] != expected {
							return fmt.Errorf("ConfigMap data mismatch: expected %q, got %q", expected, cm.Data["test-key"])
						}
						return nil
					}, 5*time.Minute, 15*time.Second).Should(Succeed())
				},
			},
		),
	)
})

type restoreResponse struct {
	ResourceID    string `json:"resourceID"`
	RecoveryState string `json:"recoveryState"`
	BackupID      string `json:"backupID,omitempty"`
	StartedAt     string `json:"startedAt,omitempty"`
	CompletedAt   string `json:"completedAt,omitempty"`
	Phase         string `json:"phase,omitempty"`
	LastCondition string `json:"lastCondition,omitempty"`
}

func createRestoreViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID, backupName string) (restoreResponse, error) {
	url := fmt.Sprintf("%s/admin/v1/hcp%s/restore", adminAPIAddress, resourceID)
	body, err := json.Marshal(struct {
		BackupID string `json:"backupID"`
	}{BackupID: backupName})
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return restoreResponse{}, fmt.Errorf("create restore returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result restoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return restoreResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}
	return result, nil
}

func getRestoreStatusViaAdminAPI(ctx context.Context, httpClient *http.Client, adminAPIAddress, resourceID string) (restoreResponse, error) {
	url := fmt.Sprintf("%s/admin/v1/hcp%s/restore", adminAPIAddress, resourceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return restoreResponse{}, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return restoreResponse{}, fmt.Errorf("get restore status returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result restoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return restoreResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}
	return result, nil
}
