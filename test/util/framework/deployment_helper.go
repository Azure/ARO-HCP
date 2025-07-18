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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/davecgh/go-spew/spew"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

// CreateBicepTemplateAndWait creates a Bicep template deployment in the specified resource group and waits for completion.
func CreateBicepTemplateAndWait(
	ctx context.Context,
	deploymentsClient *armresources.DeploymentsClient,
	resourceGroupName string,
	deploymentName string,
	bicepTemplateJSON []byte,
	parameters map[string]string,
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
