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

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"k8s.io/apimachinery/pkg/util/rand"

	api "github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/log"
)

// FallbackCreateClusterWithBicep creates a complete cluster using the demo.bicep file if setup file loading fails.
// Returns a filled SetupModel and error.
func FallbackCreateClusterWithBicep(ctx context.Context, subscriptionID string, creds azcore.TokenCredential, clients *api.ClientFactory, bicepJSONFileName string) (SetupModel, error) {
	var setup SetupModel
	// 1. Generate names
	clusterName := "e2e-cluster-" + rand.String(8)
	// Using default nodepool name as per bicep default
	nodepoolName := "nodepool-1"

	// 2. Pass as parameters to bicep
	parameters := map[string]string{
		"clusterName": clusterName,
	}

	// 3. Create a resource group name
	location := os.Getenv("LOCATION")
	if location == "" {
		location = "uksouth" // default fallback
	}
	resourceGroupName := "e2e-bicep-" + rand.String(12)

	log.Logger.Infof("Using resource group: %s, cluster name: %s", resourceGroupName, clusterName)

	// 4. Create the resource group using Azure SDK
	resourceGroupsClient, err := armresources.NewResourceGroupsClient(subscriptionID, creds, nil)
	if err != nil {
		return setup, fmt.Errorf("failed to create resource groups client: %w", err)
	}
	_, err = framework.CreateResourceGroup(ctx, resourceGroupsClient, resourceGroupName, location, 20*time.Minute)
	if err != nil {
		return setup, fmt.Errorf("failed to create resource group: %w", err)
	}

	// 5. Read the pre-built ARM template JSON from test-artifacts (relative to e2e directory)
	var jsonFile string
	if bicepJSONFileName != "" {
		jsonFile = bicepJSONFileName + ".json"
	} else {
		jsonFile = "demo.json"
	}
	jsonPath := filepath.Join("test-artifacts", "generated-test-artifacts", jsonFile)
	templateBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		return setup, fmt.Errorf("failed to read pre-built ARM template: %w", err)
	}

	// 6. Deploy the ARM template using the Azure SDK
	deploymentsClient, err := armresources.NewDeploymentsClient(subscriptionID, creds, nil)
	if err != nil {
		return setup, fmt.Errorf("failed to create deployments client: %w", err)
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
		return setup, fmt.Errorf("failed to deploy ARM template: %w", err)
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

	setup = SetupModel{
		E2ESetup: E2ESetup{
			Name: "e2e-bicep-default",
			Tags: []string{"e2e-bicep-default"},
		},
		CustomerEnv: CustomerEnv{
			CustomerRGName:   resourceGroupName,
			CustomerVNetName: "customer-vnet", // as per bicep default
			CustomerNSGName:  "customer-nsg",  // as per bicep default
			UAMIs:            uamis,
			IdentityUAMIs:    identityUAMIs,
		},
		Cluster: Cluster{
			Name:    clusterName,
			ARMData: clusterData,
		},
		Nodepools: []Nodepool{
			{
				Name:    nodepoolName,
				ARMData: nodepoolData,
			},
		},
	}

	// Marshal setup to JSON and write to test-artifacts/e2e-setup.json
	setupJSON, err := json.MarshalIndent(setup, "", "  ")
	if err != nil {
		log.Logger.Warnf("Failed to marshal SetupModel to JSON: %v", err)
	} else {
		outputPath := filepath.Join("test-artifacts", "e2e-setup.json")
		if err := os.WriteFile(outputPath, setupJSON, 0644); err != nil {
			log.Logger.Warnf("Failed to write SetupModel JSON to file: %v", err)
		}
	}

	return setup, nil
}
