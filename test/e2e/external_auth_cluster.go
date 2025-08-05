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
