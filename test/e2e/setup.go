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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	. "github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/util/rand"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/environment"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/integration"
	"github.com/Azure/ARO-HCP/test/util/labels"
	"github.com/Azure/ARO-HCP/test/util/log"
)

var (
	clients        *api.ClientFactory
	subscriptionID string
	creds          azcore.TokenCredential
	e2eSetup       integration.SetupModel
	testEnv        environment.Environment
)

func prepareEnvironmentConf(testEnv environment.Environment) azcore.ClientOptions {
	c := cloud.AzurePublic
	if environment.Development.Compare(testEnv) {
		c = cloud.Configuration{
			ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {
					Audience: "https://management.core.windows.net/",
					Endpoint: testEnv.Url(),
				},
			},
		}
	}
	opts := azcore.ClientOptions{
		Cloud:                           c,
		InsecureAllowCredentialWithHTTP: environment.Development.Compare(testEnv),
	}

	return opts
}

func setup(ctx context.Context) error {
	var (
		found bool
		err   error
		opts  azcore.ClientOptions
	)

	if subscriptionID, found = os.LookupEnv("CUSTOMER_SUBSCRIPTION"); !found {
		subscriptionID = "00000000-0000-0000-0000-000000000000"
	}
	testEnv = environment.Environment(strings.ToLower(os.Getenv("AROHCP_ENV")))
	if testEnv == "" {
		testEnv = environment.Development
	}

	opts = prepareEnvironmentConf(testEnv)
	envOptions := &azidentity.EnvironmentCredentialOptions{
		ClientOptions: opts,
	}
	creds, err = azidentity.NewEnvironmentCredential(envOptions)

	if _, found := os.LookupEnv("LOCAL_DEVELOPMENT"); found {
		creds, err = azidentity.NewAzureCLICredential(nil)
	}
	if err != nil {
		return err
	}

	armOptions := &azcorearm.ClientOptions{
		ClientOptions: opts,
	}
	clients, err = api.NewClientFactory(subscriptionID, creds, armOptions)
	if err != nil {
		return err
	}

	// Use GinkgoLabelFilter to check for the 'requirenothing' label
	labelFilter := GinkgoLabelFilter()
	if labels.RequireNothing.MatchesLabelFilter(labelFilter) {
		// Skip loading the e2esetup file
		e2eSetup = integration.SetupModel{} // zero value
	} else {
		e2eSetup, err = integration.LoadE2ESetupFile(os.Getenv("SETUP_FILEPATH"))
		if err != nil {
			if _, found := os.LookupEnv("FALLBACK_TO_BICEP"); found {
				// Fallback: create a complete cluster using bicep
				log.Logger.Warnf("Failed to load e2e setup file: %v. Falling back to bicep deployment.", err)
				if err := fallbackCreateClusterWithBicep(ctx); err != nil {
					return fmt.Errorf("failed to create cluster with bicep fallback: %w", err)
				}
			} else {
				return fmt.Errorf("failed to load e2e setup file and FALLBACK_TO_BICEP is not set: %w", err)
			}
		}
	}

	return nil
}

// fallbackCreateClusterWithBicep creates a complete cluster using the demo.bicep file if setup file loading fails.
func fallbackCreateClusterWithBicep(ctx context.Context) error {
	// 1. Generate names
	clusterName := "e2e-cluster-" + rand.String(8)
	nodepoolName := "nodepool-" + rand.String(6)

	// 2. Pass as parameters to bicep
	parameters := map[string]string{
		"clusterName":     clusterName,
		"nodepoolName":    nodepoolName,
		"persistTagValue": "false",
	}

	// 3. Create a resource group name
	location := os.Getenv("LOCATION")
	if location == "" {
		location = "eastus" // default fallback
	}
	resourceGroupName := "e2e-bicep-" + rand.String(12)

	// 4. Create the resource group using Azure SDK
	resourceGroupsClient, err := armresources.NewResourceGroupsClient(subscriptionID, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create resource groups client: %w", err)
	}
	_, err = framework.CreateResourceGroup(ctx, resourceGroupsClient, resourceGroupName, location, 20*time.Minute)
	if err != nil {
		return fmt.Errorf("failed to create resource group: %w", err)
	}

	// 5. Read the pre-built ARM template JSON (demo.json) from test-artifacts (relative to e2e directory)
	jsonPath := filepath.Join("test-artifacts", "generated-test-artifacts", "demo.json")
	templateBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("failed to read pre-built ARM template: %w", err)
	}

	// 6. Deploy the ARM template using the Azure SDK
	deploymentsClient, err := armresources.NewDeploymentsClient(subscriptionID, creds, nil)
	if err != nil {
		return fmt.Errorf("failed to create deployments client: %w", err)
	}
	deploymentResult, err := framework.CreateBicepTemplateAndWait(
		ctx,
		deploymentsClient,
		resourceGroupName,
		"aro-hcp-e2e-setup",
		templateBytes,
		parameters,
		45*time.Minute,
	)
	if err != nil {
		return fmt.Errorf("failed to deploy ARM template: %w", err)
	}

	// Extract outputs
	userAssignedIdentitiesValueStr, err := framework.GetOutputValueString(deploymentResult, "userAssignedIdentitiesValue")
	if err != nil {
		log.Logger.Warnf("Failed to extract userAssignedIdentitiesValue from deployment outputs: %v", err)
	}
	identityValueStr, err := framework.GetOutputValueString(deploymentResult, "identityValue")
	if err != nil {
		log.Logger.Warnf("Failed to extract identityValue from deployment outputs: %v", err)
	}

	// Convert userAssignedIdentitiesValueStr to api.UserAssignedIdentitiesProfile
	var uamis api.UserAssignedIdentitiesProfile
	if userAssignedIdentitiesValueStr != "" {
		err := json.Unmarshal([]byte(userAssignedIdentitiesValueStr), &uamis)
		if err != nil {
			log.Logger.Warnf("Failed to unmarshal userAssignedIdentitiesValueStr: %v", err)
		}
	}

	// Convert identityValueStr to map[string]api.UserAssignedIdentity
	identityUAMIs := map[string]api.UserAssignedIdentity{}
	if identityValueStr != "" {
		err := json.Unmarshal([]byte(identityValueStr), &identityUAMIs)
		if err != nil {
			log.Logger.Warnf("Failed to unmarshal identityValueStr: %v", err)
		}
	}

	// Fetch ARM resources for ARMData
	clusterClient := clients.NewHcpOpenShiftClustersClient()
	clusterResp, err := clusterClient.Get(ctx, resourceGroupName, clusterName, nil)
	var clusterData api.HcpOpenShiftCluster
	if err != nil {
		log.Logger.Warnf("Failed to get cluster ARM data: %v", err)
	} else {
		clusterData = clusterResp.HcpOpenShiftCluster
	}

	nodepoolClient := clients.NewNodePoolsClient()
	nodepoolResp, err := nodepoolClient.Get(ctx, resourceGroupName, clusterName, nodepoolName, nil)
	var nodepoolData api.NodePool
	if err != nil {
		log.Logger.Warnf("Failed to get nodepool ARM data: %v", err)
	} else {
		nodepoolData = nodepoolResp.NodePool
	}

	e2eSetup = integration.SetupModel{
		E2ESetup: integration.E2ESetup{
			Name: "e2e-bicep-default",
			Tags: []string{"e2e-bicep-default"},
		},
		CustomerEnv: integration.CustomerEnv{
			CustomerRGName:   resourceGroupName,
			CustomerVNetName: "customer-vnet", // as per bicep default
			CustomerNSGName:  "customer-nsg",  // as per bicep default
			UAMIs:            uamis,
			IdentityUAMIs:    identityUAMIs,
		},
		Cluster: integration.Cluster{
			Name:    clusterName,
			ARMData: clusterData,
		},
		Nodepools: []integration.Nodepool{
			{
				Name:    nodepoolName,
				ARMData: nodepoolData,
			},
		},
	}

	return nil
}
