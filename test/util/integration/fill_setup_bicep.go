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

	"k8s.io/apimachinery/pkg/util/rand"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/log"
)

// FallbackCreateClusterWithBicep creates a complete cluster using the demo.bicep file if setup file loading fails.
// Returns a filled SetupModel and error.
func FallbackCreateClusterWithBicep(ctx context.Context, bicepJSONFileName string) (SetupModel, error) {
	var setup SetupModel
	// 1. Generate names
	clusterName := "e2e-cluster-" + rand.String(8)
	// Using default nodepool name as per bicep default
	nodepoolName := "nodepool-1"

	// 2. Pass as parameters to bicep
	parameters := map[string]interface{}{
		"clusterName": clusterName,
	}

	// 3. Create a resource group
	location := os.Getenv("LOCATION")
	if location == "" {
		location = "uksouth" // default fallback
	}
	// Use framework's invocation context for resource group creation
	tc := framework.NewTestContext()
	resourceGroup, err := tc.NewResourceGroup(ctx, "e2e-bicep", location)
	if err != nil {
		return setup, fmt.Errorf("failed to create resource group: %w", err)
	}
	resourceGroupName := *resourceGroup.Name

	log.Logger.Infof("Using resource group: %s, cluster name: %s", resourceGroupName, clusterName)

	// 4. Read the pre-built ARM template JSON from test-artifacts (relative to e2e directory)
	var jsonFile string
	if bicepJSONFileName != "" {
		jsonFile = bicepJSONFileName + ".json"
	} else {
		jsonFile = "demo.json"
	}
	jsonPath := filepath.Join("bicep-templates", jsonFile)
	templateBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		return setup, fmt.Errorf("failed to read pre-built ARM template: %w", err)
	}

	// 5. Deploy the ARM template using the Azure SDK
	deploymentName := "aro-hcp-e2e-setup"
	deploymentsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient()
	deploymentResult, err := tc.CreateBicepTemplateAndWait(
		ctx,
		resourceGroupName,
		deploymentName,
		templateBytes,
		parameters,
		45*time.Minute,
	)
	if err != nil {
		return setup, fmt.Errorf("failed to deploy ARM template: %w", err)
	}

	// Get bicep deployment info and write to test-artifacts/bicep-deployment-dump.json
	deployment, err := deploymentsClient.Get(ctx, resourceGroupName, deploymentName, nil)
	if err != nil {
		log.Logger.Warnf("Failed to get deployment info: %v", err)
	} else {
		data, err := json.MarshalIndent(deployment, "", "  ")
		if err != nil {
			log.Logger.Warnf("Failed to marshal deployment info: %v", err)
		} else {
			err = os.WriteFile("test-artifacts/bicep-deployment-dump.json", data, 0644)
			if err != nil {
				log.Logger.Warnf("Failed to write deployment dump to file: %v", err)
			}
		}
	}

	// Extract outputs
	userAssignedIdentitiesValueBytes, err := framework.GetOutputValueBytes(deploymentResult, "userAssignedIdentitiesValue")
	if err != nil {
		log.Logger.Warnf("Failed to extract userAssignedIdentitiesValue from deployment outputs: %v", err)
	}
	identityValueBytes, err := framework.GetOutputValueBytes(deploymentResult, "identityValue")
	if err != nil {
		log.Logger.Warnf("Failed to extract identityValue from deployment outputs: %v", err)
	}

	// Convert userAssignedIdentitiesValue to hcpsdk20240610preview.UserAssignedIdentitiesProfile
	var uamis hcpsdk20240610preview.UserAssignedIdentitiesProfile
	if userAssignedIdentitiesValueBytes != nil {
		err := json.Unmarshal(userAssignedIdentitiesValueBytes, &uamis)
		if err != nil {
			log.Logger.Warnf("Failed to unmarshal userAssignedIdentitiesValue: %v", err)
		}
	}

	// Convert identityValue to hcpsdk20240610preview.ManagedServiceIdentity
	identityUAMIs := hcpsdk20240610preview.ManagedServiceIdentity{}
	if identityValueBytes != nil {
		err := json.Unmarshal(identityValueBytes, &identityUAMIs)
		if err != nil {
			log.Logger.Warnf("Failed to unmarshal identityValue: %v", err)
		}
	}

	// Fetch ARM resources for ARMData
	clusterClient := tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient()
	clusterResp, err := clusterClient.Get(ctx, resourceGroupName, clusterName, nil)
	var clusterData hcpsdk20240610preview.HcpOpenShiftCluster
	if err != nil {
		log.Logger.Warnf("Failed to get cluster ARM data: %v", err)
	} else {
		clusterData = clusterResp.HcpOpenShiftCluster
	}

	nodepoolClient := tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient()
	nodepoolResp, err := nodepoolClient.Get(ctx, resourceGroupName, clusterName, nodepoolName, nil)
	var nodepoolData hcpsdk20240610preview.NodePool
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
