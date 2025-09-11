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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	hcpsdk20240610preview "github.com/Azure/ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp"
	"github.com/Azure/ARO-HCP/test/util/framework"
	"github.com/Azure/ARO-HCP/test/util/log"
)

// CreateClusterWithBicep creates a complete cluster using the bicep JSON file.
// Returns a filled SetupModel and error.
func CreateClusterWithBicep(ctx context.Context, bicepJSONFile string, resourceGroupName string, deploymentsClient *armresources.DeploymentsClient, clientFactory *hcpsdk20240610preview.ClientFactory) (SetupModel, error) {
	var setup SetupModel
	// 1. Generate names
	clusterName := "e2e-cluster-" + rand.String(8)
	// Using default nodepool name as per bicep default
	nodepoolName := "nodepool-1"

	// 2. Pass as parameters to bicep
	parameters := map[string]interface{}{
		"clusterName": clusterName,
	}

	log.Logger.Infof("Using resource group: %s, cluster name: %s", resourceGroupName, clusterName)

	// 3. Read the pre-built ARM template JSON from test-artifacts (relative to test directory)
	jsonPath := filepath.Join("test", "e2e", "test-artifacts", "generated-test-artifacts", bicepJSONFile)
	templateBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		return setup, fmt.Errorf("failed to read pre-built ARM template: %w", err)
	}

	// 4. Deploy the ARM template using the Azure SDK
	deploymentName := "aro-hcp-e2e-setup"
	deploymentResult, err := framework.CreateBicepTemplateAndWait(
		ctx,
		deploymentsClient,
		resourceGroupName,
		deploymentName,
		templateBytes,
		parameters,
		45*time.Minute,
	)
	if err != nil {
		return setup, fmt.Errorf("failed to deploy ARM template: %w", err)
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
	clusterClient := clientFactory.NewHcpOpenShiftClustersClient()
	clusterResp, err := clusterClient.Get(ctx, resourceGroupName, clusterName, nil)
	var clusterData hcpsdk20240610preview.HcpOpenShiftCluster
	if err != nil {
		log.Logger.Warnf("Failed to get cluster ARM data: %v", err)
	} else {
		clusterData = clusterResp.HcpOpenShiftCluster
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
		Nodepools: []Nodepool{},
	}

	nodepoolClient := clientFactory.NewNodePoolsClient()
	nodepoolResp, err := nodepoolClient.Get(ctx, resourceGroupName, clusterName, nodepoolName, nil)
	if err != nil {
		log.Logger.Warnf("Failed to get nodepool ARM data: %v", err)
	} else {
		setup.Nodepools = append(setup.Nodepools, Nodepool{
			Name:    nodepoolName,
			ARMData: nodepoolResp.NodePool,
		})
	}

	// Marshal setup to JSON and write to artifacts directory
	setupJSON, err := json.MarshalIndent(setup, "", "  ")
	if err != nil {
		log.Logger.Warnf("Failed to marshal SetupModel to JSON: %v", err)
	} else {
		outputPath := os.Getenv("SETUP_FILEPATH")
		if outputPath == "" {
			outputPath = filepath.Join("test", "e2e", "test-artifacts", "e2e-setup.json")
		}
		if err := os.WriteFile(outputPath, setupJSON, 0644); err != nil {
			log.Logger.Warnf("Failed to write SetupModel JSON to file: %v", err)
		}
	}

	return setup, nil
}
