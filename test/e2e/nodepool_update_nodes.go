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
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to update nodepool replicas and autoscaling",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		func(ctx context.Context) {
			const (
				customerClusterName = "hcp-cluster"

				scaleDownNodePoolName = "np-scale-down"
				scaleUpNodePoolName   = "np-scale-up"
				autoscaleNodePoolName = "np-autoscale"

				deployedNodePoolsNumber = 3

				scaleDownNodePoolInitialReplicas = 2
				scaleDownNodePoolUpdatedReplicas = scaleDownNodePoolInitialReplicas - 1

				scaleUpNodePoolInitialReplicas = 1
				scaleUpNodePoolUpdatedReplicas = scaleUpNodePoolInitialReplicas + 1

				autoscaleNodePoolInitialReplicas = 1
				autoscaleNodePoolUpdatedReplicas = 0
				autoscaleNodePoolMinReplicas     = 1
				autoscaleNodePoolMaxReplicas     = 2

				clusterInitialNodeCount = scaleDownNodePoolInitialReplicas + scaleUpNodePoolInitialReplicas + autoscaleNodePoolInitialReplicas
				clusterFinalNodeCount   = scaleDownNodePoolUpdatedReplicas + scaleUpNodePoolUpdatedReplicas + autoscaleNodePoolMinReplicas
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, 60*time.Second)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "rg-update-nodes", tc.Location())
			Expect(err).NotTo(HaveOccurred())

			clusterParams := framework.NewDefaultClusterParams()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred())

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster(
				ctx,
				tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				10*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating %d node pools in parallel", deployedNodePoolsNumber))
			scaleDownParams := framework.NewDefaultNodePoolParams()
			scaleDownParams.NodePoolName = scaleDownNodePoolName
			scaleDownParams.Replicas = scaleDownNodePoolInitialReplicas

			scaleUpParams := framework.NewDefaultNodePoolParams()
			scaleUpParams.NodePoolName = scaleUpNodePoolName
			scaleUpParams.Replicas = scaleUpNodePoolInitialReplicas

			autoscaleParams := framework.NewDefaultNodePoolParams()
			autoscaleParams.NodePoolName = autoscaleNodePoolName
			autoscaleParams.Replicas = autoscaleNodePoolInitialReplicas

			allNodePoolParams := []framework.NodePoolParams{scaleDownParams, scaleUpParams, autoscaleParams}
			nodePoolCreationErrCh := make(chan error, deployedNodePoolsNumber)
			nodePoolCreateErrGroup, nodePoolCreateGroupCtx := errgroup.WithContext(ctx)
			for _, nodePoolParams := range allNodePoolParams {
				nodePoolCreateErrGroup.Go(func() error {
					createErr := tc.CreateNodePoolFromParam(
						nodePoolCreateGroupCtx,
						GinkgoLogr,
						*resourceGroup.Name,
						managedResourceGroupName,
						customerClusterName,
						nodePoolParams,
						45*time.Minute,
					)
					if createErr != nil {
						nodePoolCreationErrCh <- createErr
					}
					return createErr
				})
			}
			_ = nodePoolCreateErrGroup.Wait() // ignoring error here, will collect all node pool creation errors in the nodePoolCreationErrCh channel
			close(nodePoolCreationErrCh)
			var nodePoolCreationErrors []error
			for nodePoolCreationErr := range nodePoolCreationErrCh {
				nodePoolCreationErrors = append(nodePoolCreationErrors, nodePoolCreationErr)
			}
			Expect(nodePoolCreationErrors).To(BeEmpty(), "node pool creation errors: %v", nodePoolCreationErrors)

			By("verifying nodes count")
			Expect(verifiers.VerifyNodeCount(customerClusterName, clusterInitialNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed())

			By("verifying nodes ready statuses")
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())

			By("scaling down, scaling up, and enabling node pool autoscaling in parallel")
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			// will verify these responses after updates
			var scaleDownNodePoolResp, scaleUpNodePoolResp, autoscaleNodePoolResp *hcpsdk20240610preview.NodePool

			nodePoolUpdateErrCh := make(chan error, deployedNodePoolsNumber)
			nodePoolUpdateErrGroup, nodePoolUpdateGroupCtx := errgroup.WithContext(ctx)

			// scale down
			nodePoolUpdateErrGroup.Go(func() error {
				var scaleDownNodePoolUpdateErr error
				scaleDownNodePoolUpdate := hcpsdk20240610preview.NodePoolUpdate{
					Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
						Replicas: to.Ptr(int32(scaleDownNodePoolUpdatedReplicas)),
					},
				}
				scaleDownNodePoolResp, scaleDownNodePoolUpdateErr = framework.UpdateNodePoolAndWait(nodePoolUpdateGroupCtx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					scaleDownNodePoolName,
					scaleDownNodePoolUpdate,
					20*time.Minute,
				)
				if scaleDownNodePoolUpdateErr != nil {
					nodePoolUpdateErrCh <- scaleDownNodePoolUpdateErr
				}
				return scaleDownNodePoolUpdateErr
			})

			// scale up
			nodePoolUpdateErrGroup.Go(func() error {
				var scaleUpNodePoolUpdateErr error
				scaleUpNodePoolUpdate := hcpsdk20240610preview.NodePoolUpdate{
					Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
						Replicas: to.Ptr(int32(scaleUpNodePoolUpdatedReplicas)),
					},
				}
				scaleUpNodePoolResp, scaleUpNodePoolUpdateErr = framework.UpdateNodePoolAndWait(nodePoolUpdateGroupCtx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					scaleUpNodePoolName,
					scaleUpNodePoolUpdate,
					20*time.Minute,
				)
				if scaleUpNodePoolUpdateErr != nil {
					nodePoolUpdateErrCh <- scaleUpNodePoolUpdateErr
				}
				return scaleUpNodePoolUpdateErr
			})

			// enable autoscaling
			nodePoolUpdateErrGroup.Go(func() error {
				var autoscaleNodePoolUpdateErr error
				autoscaleNodePoolUpdate := hcpsdk20240610preview.NodePoolUpdate{
					Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
						Replicas: to.Ptr(int32(autoscaleNodePoolUpdatedReplicas)),
						AutoScaling: &hcpsdk20240610preview.NodePoolAutoScaling{
							Min: to.Ptr(int32(autoscaleNodePoolMinReplicas)),
							Max: to.Ptr(int32(autoscaleNodePoolMaxReplicas)),
						},
					},
				}
				autoscaleNodePoolResp, autoscaleNodePoolUpdateErr = framework.UpdateNodePoolAndWait(nodePoolUpdateGroupCtx,
					nodePoolsClient,
					*resourceGroup.Name,
					customerClusterName,
					autoscaleNodePoolName,
					autoscaleNodePoolUpdate,
					20*time.Minute,
				)
				if autoscaleNodePoolUpdateErr != nil {
					nodePoolUpdateErrCh <- autoscaleNodePoolUpdateErr
				}
				return autoscaleNodePoolUpdateErr
			})

			_ = nodePoolUpdateErrGroup.Wait() // ignoring error here, will collect all node pool update errors in the nodePoolUpdateErrCh channel
			close(nodePoolUpdateErrCh)
			var nodePoolUpdateErrors []error
			for nodePoolUpdateErr := range nodePoolUpdateErrCh {
				nodePoolUpdateErrors = append(nodePoolUpdateErrors, nodePoolUpdateErr)
			}
			Expect(nodePoolUpdateErrors).To(BeEmpty(), "node pool update errors: %v", nodePoolUpdateErrors)

			By("verifying scale down node pool state")
			Expect(scaleDownNodePoolResp).NotTo(BeNil(), "scale down node pool response is nil")
			Expect(scaleDownNodePoolResp.Properties).NotTo(BeNil(), "scale down node pool 'Properties' field is nil")
			Expect(scaleDownNodePoolResp.Properties.Replicas).NotTo(BeNil(), "scale down node pool 'Properties.Replicas' field is nil")
			Expect(*scaleDownNodePoolResp.Properties.Replicas).To(Equal(int32(scaleDownNodePoolUpdatedReplicas)))

			By("verifying scale up node pool state")
			Expect(scaleUpNodePoolResp).NotTo(BeNil(), "scale up node pool response is nil")
			Expect(scaleUpNodePoolResp.Properties).NotTo(BeNil(), "scale up node pool 'Properties' field is nil")
			Expect(scaleUpNodePoolResp.Properties.Replicas).NotTo(BeNil(), "scale up node pool 'Properties.Replicas' field is nil")
			Expect(*scaleUpNodePoolResp.Properties.Replicas).To(Equal(int32(scaleUpNodePoolUpdatedReplicas)))

			By("verifying autoscale node pool state")
			Expect(autoscaleNodePoolResp).NotTo(BeNil(), "autoscale node pool response is nil")
			Expect(autoscaleNodePoolResp.Properties).NotTo(BeNil(), "autoscale node pool 'Properties' field is nil")
			Expect(autoscaleNodePoolResp.Properties.AutoScaling).NotTo(BeNil(), "autoscale node pool 'Properties.AutoScaling' field is nil")
			Expect(autoscaleNodePoolResp.Properties.AutoScaling.Min).NotTo(BeNil(), "autoscale node pool 'Properties.AutoScaling.Min' field is nil")
			Expect(autoscaleNodePoolResp.Properties.AutoScaling.Max).NotTo(BeNil(), "autoscale node pool 'Properties.AutoScaling.Max' field is nil")
			Expect(*autoscaleNodePoolResp.Properties.AutoScaling.Min).To(Equal(int32(autoscaleNodePoolMinReplicas)))
			Expect(*autoscaleNodePoolResp.Properties.AutoScaling.Max).To(Equal(int32(autoscaleNodePoolMaxReplicas)))

			By("verifying nodes count")
			if err := verifiers.VerifyNodeCount(customerClusterName, clusterFinalNodeCount).Verify(ctx, adminRESTConfig); err != nil {
				// if nodes count verifier fails, logging ready and schedulable statuses of all nodes
				kubeClient, clientErr := kubernetes.NewForConfig(adminRESTConfig)
				if clientErr != nil {
					err = fmt.Errorf("%w; failed to create kube client for node details: %v", err, clientErr)
				} else {
					nodes, listErr := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
					if listErr != nil {
						err = fmt.Errorf("%w; failed to list nodes for node details: %v", err, listErr)
					} else {
						nodeStatuses := make([]string, 0, len(nodes.Items))
						for _, node := range nodes.Items {
							readyStr := "NotReady"
							for _, c := range node.Status.Conditions {
								if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
									readyStr = "Ready"
									break
								}
							}
							schedulableStr := "Schedulable"
							if node.Spec.Unschedulable {
								schedulableStr = "Unschedulable"
							}
							nodeStatuses = append(nodeStatuses, fmt.Sprintf("%s (%s, %s)", node.Name, readyStr, schedulableStr))
						}
						err = fmt.Errorf("%w; node details: %s", err, strings.Join(nodeStatuses, "; "))
					}
				}
				Expect(err).NotTo(HaveOccurred())
			}

			By("verifying nodes ready statuses")
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed())
		})
})
