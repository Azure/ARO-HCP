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

	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("ExternalAuth Full E2E", func() {
	It("creates a full HCP cluster and applies ExternalAuth config",
		labels.RequireNothing, labels.Critical, labels.Positive,
		func(ctx context.Context) {
			ic := framework.NewInvocationContext()

			const (
				region                    = "uksouth"
				customerNSGName           = "customer-nsg-name"
				customerVnetName          = "customer-vnet-name"
				customerVnetSubnetName    = "customer-vnet-subnet1"
				customerClusterName       = "external-auth-cluster"
				customerNodePoolName      = "np-1"
				externalAuthID            = "entra"
				issuerURL                 = "https://login.microsoftonline.com/fa5d3dd8-b8ec-4407-a55c-ced639f1c8c5/v2.0"
				cliAudience               = "<client-id>"
				usernameClaim             = "email"
				groupsClaim               = "groups"
				externalAuthComponentName = "console"
				externalAuthComponentNS   = "openshift-console"
				externalAuthClientSecret  = ""
				externalAuthClientID      = ""
			)

			By("creating resource group")
			resourceGroup, err := ic.NewResourceGroup(ctx, "external-auth", region)
			Expect(err).NotTo(HaveOccurred())

			By("provisioning infra")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name, "infra",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/customer-infra.json")),
				map[string]interface{}{
					"customerNsgName":        customerNSGName,
					"customerVnetName":       customerVnetName,
					"customerVnetSubnetName": customerVnetSubnetName,
				}, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("creating HCP cluster")
			managedRG := framework.SuffixName(*resourceGroup.Name, "-managed", 64)
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name, "hcp-cluster",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/cluster.json")),
				map[string]interface{}{
					"nsgName":                  customerNSGName,
					"vnetName":                 customerVnetName,
					"subnetName":               customerVnetSubnetName,
					"clusterName":              customerClusterName,
					"managedResourceGroupName": managedRG,
				}, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("creating node pool")
			_, err = framework.CreateBicepTemplateAndWait(ctx,
				ic.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient(),
				*resourceGroup.Name, "node-pool",
				framework.Must(TestArtifactsFS.ReadFile("test-artifacts/generated-test-artifacts/standard-cluster-create/nodepool.json")),
				map[string]interface{}{
					"clusterName":  customerClusterName,
					"nodePoolName": customerNodePoolName,
				}, 45*time.Minute)
			Expect(err).NotTo(HaveOccurred())

			By("applying ExternalAuth config via RP")
			authPayload := map[string]interface{}{
				"id": externalAuthID,
				"issuer": map[string]interface{}{
					"url":       issuerURL,
					"audiences": []string{cliAudience},
				},
				"claim": map[string]interface{}{
					"mappings": map[string]interface{}{
						"userName": map[string]interface{}{"claim": usernameClaim},
						"groups":   map[string]interface{}{"claim": groupsClaim},
					},
				},
				"clients": []map[string]interface{}{
					{
						"component": map[string]interface{}{
							"name":      externalAuthComponentName,
							"namespace": externalAuthComponentNS,
						},
						"id":     externalAuthClientID,
						"secret": externalAuthClientSecret,
					},
				},
			}

			body, err := json.Marshal(authPayload)
			Expect(err).NotTo(HaveOccurred())

			rpEndpoint := fmt.Sprintf("https://localhost:8443/api/aro_hcp/v1alpha1/clusters/%s/external_auth_config/external_auths", customerClusterName)
			req, err := http.NewRequestWithContext(ctx, "POST", rpEndpoint, bytes.NewReader(body))
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			By("verifying ExternalAuth config was applied")
			verifyReq, err := http.NewRequestWithContext(ctx, "GET", rpEndpoint, nil)
			Expect(err).NotTo(HaveOccurred())
			verifyResp, err := client.Do(verifyReq)
			Expect(err).NotTo(HaveOccurred())
			defer verifyResp.Body.Close()
			Expect(verifyResp.StatusCode).To(Equal(http.StatusOK))

			respData, err := io.ReadAll(verifyResp.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(respData)).To(ContainSubstring(externalAuthID))
		})
})
