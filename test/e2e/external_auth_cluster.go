<<<<<<< HEAD
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
	"embed"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

//go:embed test-artifacts
var externalAuthTestArtifacts embed.FS

var _ = Describe("Customer", func() {
	It("should be able to create an HCP cluster with Entra external auth",
		labels.Integration,
		labels.ExternalAuth,
		func(ctx context.Context) {
			const (
				clusterName  = "external-auth-cluster"
				nodePoolName = "np-1"
				nsgName      = "customer-nsg"
				vnetName     = "customer-vnet"
				subnetName   = "customer-subnet"
			)

			ic := framework.NewInvocationContext()
			rootCtx := framework.NewRootInvocationContext() // Use root for env vars

			By("creating a resource group")
			resourceGroup, err := ic.NewResourceGroup(ctx, "external-auth", "uksouth")
			Expect(err).NotTo(HaveOccurred())

			By("deploying the HCP cluster with Entra external auth config")
			managedResourceGroupName := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name,
				"hcp-cluster",
				framework.Must(externalAuthTestArtifacts.ReadFile("external-auth/external-auth-cluster.json")),
				map[string]interface{}{
					"clusterName":              clusterName,
					"managedResourceGroupName": managedResourceGroupName,
					"nsgName":                  nsgName,
					"vnetName":                 vnetName,
					"subnetName":               subnetName,
					"entraClientId":            rootCtx.TestUserClientIDValue(), // fixed
					"entraTenantId":            rootCtx.TenantIDValue(),
				},
				45*time.Minute,
			)
			Expect(err).NotTo(HaveOccurred())
		})
})
