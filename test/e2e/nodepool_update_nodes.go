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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/v20240610preview/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

const (
	// consoleURLTimeout bounds how long we wait for the console URL to be
	// published on the cluster resource and to answer its first request.
	consoleURLTimeout = 15 * time.Minute
	// consoleProbeTimeout bounds a single console HTTPS request.
	consoleProbeTimeout = 10 * time.Second
	// consoleProbePolling is how often the console is probed while a node pool
	// update is in flight. Short enough to catch a brief outage, long enough to
	// avoid hammering the console for the duration of a scaling operation.
	consoleProbePolling = 15 * time.Second
)

var _ = Describe("Customer", func() {
	It("should be able to update nodepool replicas and autoscaling",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.Slow,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "np-update-nodes-hcp-cluster"
				// Upper casing is intentional to regress casing bug: ARO-29572
				customerNodePoolName = "np-update-NODES"
				oneNodePoolName      = "np-one-node"
			)

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "nodepool-update-nodes", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group nodepool-update-nodes")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20240610()
			clusterParams.ClusterName = customerClusterName
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			clusterParams.ManagedResourceGroupName = managedResourceGroupName

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20240610(ctx,
				resourceGroup,
				clusterParams,
				map[string]interface{}{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			By("creating the HCP cluster")
			err = tc.CreateHCPClusterFromParam20240610(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster %s", customerClusterName)

			By("getting admin credentials for the cluster")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for cluster %s", customerClusterName)

			By("creating the node pools in parallel")
			mainNodeCount := 2
			oneNodeCount := 1

			mainNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			mainNodePoolParams.NodePoolName = customerNodePoolName
			mainNodePoolParams.Replicas = int32(mainNodeCount)

			oneNodePoolParams := framework.NewDefaultNodePoolParams20240610()
			oneNodePoolParams.NodePoolName = oneNodePoolName
			oneNodePoolParams.Replicas = int32(oneNodeCount)

			errCh := make(chan error, 2)
			group, groupCtx := errgroup.WithContext(ctx)
			for _, nodePoolParams := range []framework.NodePoolParams20240610{mainNodePoolParams, oneNodePoolParams} {
				group.Go(func() error {
					createErr := tc.CreateNodePoolFromParam20240610(
						groupCtx,
						GinkgoLogr,
						*resourceGroup.Name,
						managedResourceGroupName,
						customerClusterName,
						nodePoolParams,
						framework.NodePoolCreationTimeout,
					)
					if createErr != nil {
						errCh <- createErr
					}
					return createErr
				})
			}
			_ = group.Wait()
			close(errCh)
			var creationErrors []error
			for createErr := range errCh {
				creationErrors = append(creationErrors, createErr)
			}
			Expect(creationErrors).To(BeEmpty(), "nodepool creation errors: %v", creationErrors)

			By("verifying nodes count and ready status")
			totalNodeCount := mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify initial node count of %d", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after initial creation")

			By("waiting for the OpenShift web console to become available")
			clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
			nodePoolsClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()

			var consoleURL string
			var lastConsoleWaitErr string
			Eventually(func(ctx context.Context) error {
				err := func() error {
					clusterResp, err := clusterClient.Get(ctx, *resourceGroup.Name, customerClusterName, nil)
					if err != nil {
						return fmt.Errorf("failed to get cluster: %w", err)
					}
					if clusterResp.Properties == nil || clusterResp.Properties.Console == nil || clusterResp.Properties.Console.URL == nil {
						return fmt.Errorf("cluster console URL not yet published")
					}
					consoleURL = *clusterResp.Properties.Console.URL

					if err := framework.TestHTTPSConnectivity(ctx, consoleURL, consoleProbeTimeout, true); err != nil {
						return fmt.Errorf("console %s not reachable: %w", consoleURL, err)
					}
					return nil
				}()
				// Delta-only logging: only report when the failure reason changes.
				if err != nil {
					if err.Error() != lastConsoleWaitErr {
						GinkgoLogr.Info("waiting for console", "error", err.Error())
						lastConsoleWaitErr = err.Error()
					}
					return err
				}
				GinkgoLogr.Info("console is available", "url", consoleURL)
				return nil
			}).WithContext(ctx).WithTimeout(consoleURLTimeout).WithPolling(30*time.Second).Should(Succeed(),
				"OpenShift web console should become available for cluster %s", customerClusterName)

			// updateNodePoolWatchingConsole applies update to nodePoolName and, while the
			// update is in flight, continuously probes the OpenShift web console. The
			// update runs on a separate goroutine so the Consistently poll loop — which
			// must stay on the spec goroutine, where Gomega failures are reported — runs
			// concurrently with it.
			//
			// Two Gomega details drive the shape of this loop:
			//   - Consistently's duration defaults to 100ms and is NOT derived from the
			//     context, so WithTimeout must bound the watch explicitly. Without it the
			//     loop returns after a single probe and never observes the update.
			//   - A cancelled context makes Consistently *fail* ("Context was cancelled"),
			//     so context cancellation cannot be used to end the watch. Instead the
			//     poll body calls StopTrying().Successfully() once the update goroutine
			//     has signalled completion, which is Consistently's documented early exit.
			updateNodePoolWatchingConsole := func(ctx context.Context, nodePoolName string, update hcpsdk20240610preview.NodePoolUpdate) *hcpsdk20240610preview.NodePool {
				GinkgoHelper()

				updateFinished := make(chan struct{})
				var updateResp *hcpsdk20240610preview.NodePool
				var updateErr error
				go func() {
					defer GinkgoRecover()
					defer close(updateFinished)
					updateResp, updateErr = framework.UpdateNodePoolAndWait20240610(ctx,
						nodePoolsClient,
						*resourceGroup.Name,
						customerClusterName,
						nodePoolName,
						update,
						framework.NodePoolScalingTimeout,
					)
				}()

				probeStart := time.Now()
				Consistently(func(g Gomega, ctx context.Context) {
					select {
					case <-updateFinished:
						StopTrying(fmt.Sprintf("node pool %s update finished after %s",
							nodePoolName, time.Since(probeStart).Round(time.Second))).Successfully().Now()
					default:
					}

					probeErr := framework.TestHTTPSConnectivity(ctx, consoleURL, consoleProbeTimeout, true)
					g.Expect(probeErr).NotTo(HaveOccurred(),
						"OpenShift web console %s became unreachable %s into the update of node pool %s",
						consoleURL, time.Since(probeStart).Round(time.Second), nodePoolName)
				}).WithContext(ctx).WithTimeout(framework.NodePoolScalingTimeout).WithPolling(consoleProbePolling).Should(Succeed(),
					"OpenShift web console must remain available while node pool %s is updated", nodePoolName)

				// Receiving on updateFinished both waits for a still-running update (if the
				// watch above stopped on its timeout) and orders the goroutine's writes to
				// updateResp/updateErr before the reads below.
				<-updateFinished
				Expect(updateErr).NotTo(HaveOccurred(), "failed to update node pool %s", nodePoolName)
				Expect(updateResp).NotTo(BeNil(), "update response for node pool %s was nil", nodePoolName)
				return updateResp
			}

			By("scaling up the nodepool replicas from 2 to 3 replicas while watching the console")
			mainNodeCount = 3
			update := hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(mainNodeCount)),
				},
			}
			scaleUpResp := updateNodePoolWatchingConsole(ctx, customerNodePoolName, update)
			Expect(scaleUpResp.Properties).NotTo(BeNil(), "scale up response Properties was nil")
			Expect(scaleUpResp.Properties.Replicas).NotTo(BeNil(), "scale up response Properties.Replicas was nil")
			Expect(*scaleUpResp.Properties.Replicas).To(Equal(int32(mainNodeCount)), "expected scale up response replicas to equal %d", mainNodeCount)

			By("verifying nodes count and ready status")
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of %d after scale up", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after scale up")

			By("scaling down the nodepool replicas from 3 to 2 replicas while watching the console")
			mainNodeCount = 2
			update = hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(mainNodeCount)),
				},
			}
			scaleDownResp := updateNodePoolWatchingConsole(ctx, customerNodePoolName, update)
			Expect(scaleDownResp.Properties).NotTo(BeNil(), "scale down response Properties was nil")
			Expect(scaleDownResp.Properties.Replicas).NotTo(BeNil(), "scale down response Properties.Replicas was nil")
			Expect(*scaleDownResp.Properties.Replicas).To(Equal(int32(mainNodeCount)), "expected scale down response replicas to equal %d", mainNodeCount)

			By("verifying nodes count and ready status")
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of %d after scale down", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after scale down")

			By("updating the one-replica nodepool replicas to 0 and enabling autoscaling with a PATCH while watching the console")
			update = hcpsdk20240610preview.NodePoolUpdate{
				Properties: &hcpsdk20240610preview.NodePoolPropertiesUpdate{
					Replicas: to.Ptr(int32(0)),
					AutoScaling: &hcpsdk20240610preview.NodePoolAutoScaling{
						Min: to.Ptr(int32(2)),
						Max: to.Ptr(int32(3)),
					},
				},
			}
			autoscaleResp := updateNodePoolWatchingConsole(ctx, oneNodePoolName, update)
			Expect(autoscaleResp.Properties).NotTo(BeNil(), "autoscale response Properties was nil")
			Expect(autoscaleResp.Properties.AutoScaling).NotTo(BeNil(), "autoscale response Properties.AutoScaling was nil")
			Expect(autoscaleResp.Properties.AutoScaling.Min).NotTo(BeNil(), "autoscale response Properties.AutoScaling.Min was nil")
			Expect(autoscaleResp.Properties.AutoScaling.Max).NotTo(BeNil(), "autoscale response Properties.AutoScaling.Max was nil")
			Expect(*autoscaleResp.Properties.AutoScaling.Min).To(Equal(int32(2)), "expected autoscale response min to equal 2")
			Expect(*autoscaleResp.Properties.AutoScaling.Max).To(Equal(int32(3)), "expected autoscale response max to equal 3")

			By("verifying nodes count and ready status")
			oneNodeCount = 2
			totalNodeCount = mainNodeCount + oneNodeCount
			Expect(verifiers.VerifyNodeCount(customerClusterName, totalNodeCount).Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify node count of %d after enabling autoscaling", totalNodeCount)
			Expect(verifiers.VerifyNodesReady().Verify(ctx, adminRESTConfig)).To(Succeed(), "failed to verify all nodes are ready after enabling autoscaling")

			By("verifying the OpenShift web console is still available after all node pool updates")
			Expect(framework.TestHTTPSConnectivity(ctx, consoleURL, consoleProbeTimeout, true)).To(Succeed(),
				"OpenShift web console %s should still be reachable after all node pool updates", consoleURL)
		})
})
