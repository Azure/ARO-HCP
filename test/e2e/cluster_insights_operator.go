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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/verifiers"
)

var _ = Describe("Customer", func() {
	It("should be able to see an HCP cluster registered with console.redhat.com via the Insights Operator",
		labels.RequireNothing,
		labels.High,
		labels.Positive,
		labels.AroRpApiCompatible,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "insights-hcp-cluster"
				cloudOpenShiftHost  = "cloud.openshift.com"

				insightsOperatorTimeout = 10 * time.Minute
			)
			tc := framework.NewTestContext()

			By("checking pull secret file exists and contains cloud.openshift.com token")
			pullSecretFilePath := filepath.Join(tc.PullSecretPath(), "pull-secret")
			if _, err := os.Stat(pullSecretFilePath); errors.Is(err, os.ErrNotExist) {
				Skip(fmt.Sprintf("Pull secret file not found at %s, skipping test", pullSecretFilePath))
			}

			pullSecretFileData, err := os.ReadFile(pullSecretFilePath)
			Expect(err).NotTo(HaveOccurred(), "failed to read pull-secret file from %s", pullSecretFilePath)

			var pullSecretConfig framework.DockerConfigJSON
			err = json.Unmarshal(pullSecretFileData, &pullSecretConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to parse pull-secret file")

			_, hasToken := pullSecretConfig.Auths[cloudOpenShiftHost]

			if tc.UsePooledIdentities() {
				err = tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "insights-test", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group for Insights Operator test")

			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName
			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup,
				clusterParams,
				map[string]any{},
				TestArtifactsFS,
				framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create customer resources for Insights Operator cluster")

			By("creating the cluster")
			err = tc.CreateHCPClusterFromParam20260901(ctx,
				GinkgoLogr,
				*resourceGroup.Name,
				clusterParams,
				nil,
				framework.ClusterCreationTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create HCP cluster for Insights Operator test")

			By("getting credentials")
			adminRESTConfig, err := tc.GetAdminRESTConfigForHCPCluster20260901(
				ctx,
				tc.Get20260901ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
				*resourceGroup.Name,
				customerClusterName,
				framework.GetAdminRESTConfigTimeout,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to get admin REST config for Insights Operator cluster")

			By("ensuring the cluster is viable")
			err = verifiers.VerifyHCPCluster(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to verify HCP cluster viability")

			if !hasToken {
				By("verifying Insights Operator is deployed and in Disabled/NoToken state (conformance)")
				err = verifiers.VerifyInsightsOperatorDisabledNoToken(insightsOperatorTimeout).
					Verify(ctx, adminRESTConfig)
				Expect(err).NotTo(HaveOccurred(), "Insights Operator was not deployed or did not reach Disabled/NoToken state")
				return
			}

			By("verifying the Insights Operator is healthy")
			err = verifiers.VerifyInsightsOperatorHealthy(insightsOperatorTimeout).
				Verify(ctx, adminRESTConfig)
			Expect(err).NotTo(HaveOccurred(), "Insights Operator is not healthy")
		})
})
