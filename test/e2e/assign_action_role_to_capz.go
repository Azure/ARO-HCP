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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

func lookupCAPZPrincipalID0901(
	ctx context.Context,
	msiClient *armmsi.UserAssignedIdentitiesClient,
	clusterParams framework.ClusterParams20260901,
) (string, error) {
	if clusterParams.UserAssignedIdentitiesProfile == nil || clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators == nil {
		return "", fmt.Errorf("CAPZ identity profile not found in cluster params")
	}
	capzMIResourceIDStr, ok := clusterParams.UserAssignedIdentitiesProfile.ControlPlaneOperators[framework.ClusterApiAzureMiName]
	if !ok || capzMIResourceIDStr == nil {
		return "", fmt.Errorf("CAPZ identity not found in cluster params control plane operators")
	}
	capzMIResourceID, err := azcorearm.ParseResourceID(*capzMIResourceIDStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse CAPZ MI resource ID %q: %w", *capzMIResourceIDStr, err)
	}
	capzMI, err := msiClient.Get(ctx, capzMIResourceID.ResourceGroupName, capzMIResourceID.Name, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get CAPZ MI: %w", err)
	}
	if capzMI.Properties == nil || capzMI.Properties.PrincipalID == nil {
		return "", fmt.Errorf("CAPZ MI has no principal ID")
	}
	return *capzMI.Properties.PrincipalID, nil
}

var _ = Describe("Customer", func() {
	It("capz should be grantable managed identity operator on an external customer-owned managed identity",
		labels.RequireNothing,
		labels.Medium,
		labels.Positive,
		labels.MIContainers(1),
		func(ctx context.Context) {
			const (
				customerClusterName = "action-assign-capz-cluster"

				// ACR pull requires OCP >= 4.22 (kubelet credential provider only from 4.22).
				minOpenshiftVersion = "4.22"

				miLookupTimeout       = 1 * time.Minute // observed <20s, 3x safety
				roleAssignmentTimeout = 2 * time.Minute // observed <20s, 6x safety

				MIName = "action-assign-capz-mi"
			)

			// Setup
			By("creating cluster parameters")
			clusterParams := framework.NewDefaultClusterParams20260901()
			clusterParams.ClusterName = customerClusterName

			tc := framework.NewTestContext()

			if tc.UsePooledIdentities() {
				err := tc.AssignIdentityContainers(ctx, 1, framework.IdentityContainerAssignmentRetryInterval)
				Expect(err).NotTo(HaveOccurred(), "failed to assign pooled identity containers")
			}

			By("creating a resource group")
			resourceGroup, err := tc.NewResourceGroup(ctx, "acr-pull", tc.Location())
			Expect(err).NotTo(HaveOccurred(), "failed to create resource group")

			clusterParams.ManagedResourceGroupName = framework.SuffixName(*resourceGroup.Name, "-managed", 64)

			By("creating customer resources")
			clusterParams, err = tc.CreateClusterCustomerResources20260901(ctx,
				resourceGroup, clusterParams, map[string]any{},
				TestArtifactsFS, framework.RBACScopeResourceGroup,
			)
			Expect(err).NotTo(HaveOccurred(), "failed to create cluster customer resources")

			subscriptionID, err := tc.SubscriptionID(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to get subscription ID")
			cred, err := tc.AzureCredential()
			Expect(err).NotTo(HaveOccurred(), "failed to get Azure credential")

			msiClient, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionID, cred, nil)
			Expect(err).NotTo(HaveOccurred(), "failed to create MSI client")

			By("creating the managed identity")
			mIResourceID, err := createACRPullMI(ctx, msiClient, *resourceGroup.Name, MIName, resourceGroup.Location)
			Expect(err).NotTo(HaveOccurred(), "failed to create the MI")

			var capzPrincipalID string
			Eventually(func() error {
				var lookupErr error
				capzPrincipalID, lookupErr = lookupCAPZPrincipalID0901(ctx, msiClient, clusterParams)
				return lookupErr
			}).WithContext(ctx).WithTimeout(miLookupTimeout).WithPolling(15*time.Second).Should(Succeed(),
				"failed to look up CAPZ principal ID")

			roleAssignmentsClient := newRoleAssignmentsClient(subscriptionID, cred)

			By("granting Managed Identity Operator on managed identity to CAPZ")
			grantBuiltInRoleWithRetry(ctx, roleAssignmentsClient, subscriptionID,
				mIResourceID, capzPrincipalID, managedIdentityOperatorRoleID,
				"Managed Identity Operator role assignment on the MI", roleAssignmentTimeout)
		})
})
