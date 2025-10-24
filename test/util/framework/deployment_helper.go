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

package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/davecgh/go-spew/spew"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func GetOutputValueString(deploymentInfo *armresources.DeploymentExtended, outputName string) (string, error) {
	outputMap, ok := deploymentInfo.Properties.Outputs.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("failed to cast deployment outputs to map[string]interface{}, was %T", deploymentInfo.Properties.Outputs)
	}

	ret, found, err := unstructured.NestedString(outputMap, outputName, "value")
	if err != nil {
		return "", fmt.Errorf("failed to get output value for %q: %w", outputName, err)
	}
	if !found {
		return "", fmt.Errorf("output %q not found", outputName)
	}
	return ret, nil
}

func GetOutputValue(deploymentInfo *armresources.DeploymentExtended, outputName string) (interface{}, error) {
	outputMap, ok := deploymentInfo.Properties.Outputs.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("failed to cast deployment outputs to map[string]interface{}, was %T", deploymentInfo.Properties.Outputs)
	}

	ret, found, err := unstructured.NestedFieldCopy(outputMap, outputName, "value")
	if err != nil {
		return "", fmt.Errorf("failed to get output value for %q: %w", outputName, err)
	}
	if !found {
		return "", fmt.Errorf("output %q not found", outputName)
	}
	return ret, nil
}

func GetOutputValueBytes(deploymentInfo *armresources.DeploymentExtended, outputName string) ([]byte, error) {
	outputMap, ok := deploymentInfo.Properties.Outputs.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to cast deployment outputs to map[string]interface{}, was %T", deploymentInfo.Properties.Outputs)
	}

	val, found, err := unstructured.NestedFieldNoCopy(outputMap, outputName, "value")
	if err != nil {
		return nil, fmt.Errorf("failed to get output value for %q: %w", outputName, err)
	}
	if !found {
		return nil, fmt.Errorf("output %q not found", outputName)
	}

	bytes, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output value for %q: %w", outputName, err)
	}

	return bytes, nil
}

// CreateBicepTemplateAndWait creates a Bicep template deployment in the specified resource group and waits for completion.
func CreateBicepTemplateAndWait(
	ctx context.Context,
	deploymentsClient *armresources.DeploymentsClient,
	resourceGroupName string,
	deploymentName string,
	bicepTemplateJSON []byte,
	parameters map[string]interface{},
	timeout time.Duration,
) (*armresources.DeploymentExtended, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bicepParameters := map[string]interface{}{}
	for k, v := range parameters {
		bicepParameters[k] = map[string]interface{}{
			"value": v,
		}
	}

	// TODO deads2k: couldn't work out why, but for some reason this works when passed as a map, not when sending json. My guess is newlines.
	bicepTemplateMap := map[string]interface{}{}
	if err := json.Unmarshal(bicepTemplateJSON, &bicepTemplateMap); err != nil {
		panic(err)
	}

	deploymentProperties := armresources.Deployment{
		Properties: &armresources.DeploymentProperties{
			DebugSetting: &armresources.DebugSetting{DetailLevel: to.Ptr("requestContent")},
			Template:     bicepTemplateMap,
			Parameters:   bicepParameters,
			Mode:         to.Ptr(armresources.DeploymentModeIncremental), // or Complete
		},
	}

	pollerResp, err := deploymentsClient.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		deploymentName,
		deploymentProperties,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating deployment %q in resourcegroup=%q: %w", deploymentName, resourceGroupName, err)
	}
	operationResult, err := pollerResp.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: StandardPollInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("failed waiting for deployment %q in resourcegroup=%q to finish: %w", deploymentName, resourceGroupName, err)
	}

	switch m := any(operationResult).(type) {
	case armresources.DeploymentsClientCreateOrUpdateResponse:
		// TODO someone may want this return value.  We'll have to work it out then.
		//fmt.Printf("#### got back: %v\n", spew.Sdump(m))
		return &m.DeploymentExtended, nil
	default:
		fmt.Printf("#### unknown type %T: content=%v", m, spew.Sdump(m))
		return nil, fmt.Errorf("unknown type %T", m)
	}
}

func ListAllDeployments(
	ctx context.Context,
	deploymentsClient *armresources.DeploymentsClient,
	resourceGroupName string,
	timeout time.Duration,
) ([]*armresources.DeploymentExtended, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	deploymentsPager := deploymentsClient.NewListByResourceGroupPager(resourceGroupName, nil)

	allDeployments := []*armresources.DeploymentExtended{}
	for deploymentsPager.More() {
		deploymentPage, err := deploymentsPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing deployments in resourcegroup=%q: %w", resourceGroupName, err)
		}
		allDeployments = append(allDeployments, deploymentPage.Value...)
	}

	return allDeployments, nil
}

func ListAllOperations(
	ctx context.Context,
	deploymentOperationsClient *armresources.DeploymentOperationsClient,
	resourceGroupName string,
	deploymentName string,
	timeout time.Duration,
) ([]*armresources.DeploymentOperation, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	operationsPager := deploymentOperationsClient.NewListPager(resourceGroupName, deploymentName, nil)

	allOperations := []*armresources.DeploymentOperation{}
	for operationsPager.More() {
		operationsPage, err := operationsPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed listing operations in resourcegroup=%q deployment=%q: %w", resourceGroupName, deploymentName, err)
		}
		allOperations = append(allOperations, operationsPage.Value...)
	}

	return allOperations, nil
}

// CreateHCPClusterFromBicepDev creates an HCP cluster from bicep template in development environment
// by converting it to direct API calls to localhost:8443
func CreateHCPClusterFromBicepDev(
	ctx context.Context,
	testContext *perItOrDescribeTestContext,
	resourceGroupName string,
	bicepTemplateJSON []byte,
	parameters map[string]interface{},
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Extract cluster name from parameters
	clusterName, ok := parameters["clusterName"].(string)
	if !ok {
		return fmt.Errorf("clusterName parameter not found or not a string")
	}

	fmt.Printf("DEBUG: Creating HCP cluster %s via direct API in dev environment\n", clusterName)

	// Get subscription ID from test context
	subscriptionId, err := testContext.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return fmt.Errorf("failed to get subscription ID: %w", err)
	}

	// Convert bicep template to HCP cluster object
	cluster, err := BuildHCPClusterFromBicepTemplate(ctx, bicepTemplateJSON, parameters, testContext.Location(), subscriptionId, resourceGroupName, testContext)
	if err != nil {
		return fmt.Errorf("failed to build HCP cluster from bicep: %w", err)
	}
	// Create the cluster directly via API
	_, err = CreateHCPClusterAndWait(
		ctx,
		testContext.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
		resourceGroupName,
		clusterName,
		cluster,
		timeout,
	)

	if err != nil {
		return fmt.Errorf("failed to create HCP cluster %s: %w", clusterName, err)
	}
	return nil
}

// CreateNodePoolFromBicepDev creates a NodePool from bicep template in development environment
// by converting it to direct API calls to localhost:8443
func CreateNodePoolFromBicepDev(
	ctx context.Context,
	testContext *perItOrDescribeTestContext,
	resourceGroupName string,
	hcpClusterName string,
	bicepTemplateJSON []byte,
	parameters map[string]interface{},
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Extract nodepool name from parameters
	nodePoolName, ok := parameters["nodePoolName"].(string)
	if !ok {
		return fmt.Errorf("nodePoolName parameter not found or not a string")
	}

	fmt.Printf("DEBUG: Creating NodePool %s via direct API in dev environment\n", nodePoolName)

	// Get subscription ID from test context
	subscriptionId, err := testContext.getSubscriptionIDUnlocked(ctx)
	if err != nil {
		return fmt.Errorf("failed to get subscription ID: %w", err)
	}

	// Convert bicep template to NodePool object
	nodePool, err := BuildNodePoolFromBicepTemplate(ctx, bicepTemplateJSON, parameters, testContext.Location(), subscriptionId, resourceGroupName)
	if err != nil {
		return fmt.Errorf("failed to build NodePool from bicep: %w", err)
	}

	// Create the nodepool directly via API
	_, err = CreateNodePoolAndWait(
		ctx,
		testContext.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
		resourceGroupName,
		hcpClusterName,
		nodePoolName,
		nodePool,
		timeout,
	)
	if err != nil {
		return fmt.Errorf("failed to create NodePool %s: %w", nodePoolName, err)
	}

	return nil
}
