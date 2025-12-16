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
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"

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

type bicepDeploymentScope int

const (
	// BicepDeploymentScopeResourceGroup deploys into a specific resource group.
	BicepDeploymentScopeResourceGroup bicepDeploymentScope = iota
	// BicepDeploymentScopeSubscription deploys at subscription scope.
	BicepDeploymentScopeSubscription
)

type bicepDeploymentConfig struct {
	scope            bicepDeploymentScope
	resourceGroup    string
	deploymentName   string
	parameters       map[string]interface{}
	timeout          time.Duration
	debugDetailLevel string
	location         string
	template         []byte
}

type BicepDeploymentOption func(*bicepDeploymentConfig)

func WithDeploymentName(name string) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.deploymentName = name
	}
}

func WithScope(scope bicepDeploymentScope) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.scope = scope
	}
}

func WithClusterResourceGroup(resourceGroupName string) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.resourceGroup = resourceGroupName
	}
}

func WithParameters(parameters map[string]interface{}) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.parameters = parameters
	}
}

func WithTimeout(timeout time.Duration) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.timeout = timeout
	}
}

func WithDebugDetailLevel(level string) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.debugDetailLevel = level
	}
}

func WithLocation(location string) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.location = location
	}
}

func WithTemplateFromFS(fs embed.FS, path string) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.template = Must(fs.ReadFile(path))
	}
}

func WithTemplateFromBytes(template []byte) BicepDeploymentOption {
	return func(cfg *bicepDeploymentConfig) {
		cfg.template = template
	}
}

func (tc *perItOrDescribeTestContext) CreateBicepTemplateAndWait(
	ctx context.Context,
	opts ...BicepDeploymentOption,
) (*armresources.DeploymentExtended, error) {
	cfg := &bicepDeploymentConfig{
		scope:            BicepDeploymentScopeResourceGroup,
		timeout:          30 * time.Minute,
		debugDetailLevel: "requestContent",
		parameters:       map[string]interface{}{},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.deploymentName == "" {
		return nil, fmt.Errorf("deployment name must be specified")
	}
	if cfg.scope == BicepDeploymentScopeResourceGroup && cfg.resourceGroup == "" {
		return nil, fmt.Errorf("resource group name must be specified for resource-group scoped deployments")
	}
	if cfg.scope == BicepDeploymentScopeSubscription && cfg.location == "" {
		return nil, fmt.Errorf("location must be specified for subscription-scoped deployments")
	}

	ctx, cancel := context.WithTimeoutCause(ctx, cfg.timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateBicepTemplateAndWait for deployment %s in resource group %s", cfg.timeout.Minutes(), cfg.deploymentName, cfg.resourceGroup))
	defer cancel()

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy ARM template %s/%s", cfg.resourceGroup, cfg.deploymentName), startTime, finishTime)
	}()
	tc.RecordKnownDeployment(cfg.resourceGroup, cfg.deploymentName)

	deploymentsClient := tc.GetARMResourcesClientFactoryOrDie(ctx).NewDeploymentsClient()

	bicepParameters := map[string]interface{}{}
	for k, v := range cfg.parameters {
		bicepParameters[k] = map[string]interface{}{
			"value": v,
		}
	}

	// TODO deads2k: couldn't work out why, but for some reason this works when passed as a map, not when sending json. My guess is newlines.
	bicepTemplateMap := map[string]interface{}{}
	if err := json.Unmarshal(cfg.template, &bicepTemplateMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Bicep template JSON: %w", err)
	}

	deploymentProperties := armresources.Deployment{
		Location: to.Ptr(cfg.location),
		Properties: &armresources.DeploymentProperties{
			DebugSetting: &armresources.DebugSetting{DetailLevel: to.Ptr(cfg.debugDetailLevel)},
			Template:     bicepTemplateMap,
			Parameters:   bicepParameters,
			Mode:         to.Ptr(armresources.DeploymentModeIncremental),
		},
	}

	switch cfg.scope {
	case BicepDeploymentScopeResourceGroup:
		pollerResp, err := deploymentsClient.BeginCreateOrUpdate(
			ctx,
			cfg.resourceGroup,
			cfg.deploymentName,
			deploymentProperties,
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("failed creating deployment %q in resourcegroup=%q: %w", cfg.deploymentName, cfg.resourceGroup, err)
		}
		resp, err := pollerResp.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: StandardPollInterval,
		})
		if err != nil {
			return nil, fmt.Errorf("failed waiting for deployment %q in resourcegroup=%q to finish: %w", cfg.deploymentName, cfg.resourceGroup, err)
		}

		return &resp.DeploymentExtended, nil

	case BicepDeploymentScopeSubscription:
		pollerResp, err := deploymentsClient.BeginCreateOrUpdateAtSubscriptionScope(
			ctx,
			cfg.deploymentName,
			deploymentProperties,
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("failed creating deployment %q at subscription scope: %w", cfg.deploymentName, err)
		}
		resp, err := pollerResp.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: StandardPollInterval,
		})
		if err != nil {
			return nil, fmt.Errorf("failed waiting for deployment %q at subscription scope to finish: %w", cfg.deploymentName, err)
		}

		return &resp.DeploymentExtended, nil

	default:
		return nil, fmt.Errorf("unsupported deployment scope %v", cfg.scope)
	}

}

func ListAllDeployments(
	ctx context.Context,
	deploymentsClient *armresources.DeploymentsClient,
	resourceGroupName string,
	timeout time.Duration,
) ([]*armresources.DeploymentExtended, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during ListAllDeployments for resource group %s", timeout.Minutes(), resourceGroupName))
	defer cancel()

	deploymentsPager := deploymentsClient.NewListByResourceGroupPager(resourceGroupName, nil)

	allDeployments := []*armresources.DeploymentExtended{}
	for deploymentsPager.More() {
		deploymentPage, err := deploymentsPager.NextPage(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed listing deployments in resourcegroup=%q, caused by: %w, error: %w", resourceGroupName, context.Cause(ctx), err)
			}
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
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during ListAllOperations for deployment %s in resource group %s", timeout.Minutes(), deploymentName, resourceGroupName))
	defer cancel()

	operationsPager := deploymentOperationsClient.NewListPager(resourceGroupName, deploymentName, nil)

	allOperations := []*armresources.DeploymentOperation{}
	for operationsPager.More() {
		operationsPage, err := operationsPager.NextPage(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("failed listing operations in resourcegroup=%q deployment=%q, caused by: %w, error: %w", resourceGroupName, deploymentName, context.Cause(ctx), err)
			}
			return nil, fmt.Errorf("failed listing operations in resourcegroup=%q deployment=%q: %w", resourceGroupName, deploymentName, err)
		}
		allOperations = append(allOperations, operationsPage.Value...)
	}

	return allOperations, nil
}

func (tc *perItOrDescribeTestContext) CreateHCPClusterFromParam(
	ctx context.Context,
	logger logr.Logger,
	resourceGroupName string,
	parameters ClusterParams,
	timeout time.Duration,
) error {
	if timeout > 0*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateHCPClusterFromParam for cluster %s in resource group %s", timeout.Minutes(), parameters.ClusterName, resourceGroupName))
		defer cancel()
	}
	clusterName := parameters.ClusterName

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy HCP cluster %s/%s", resourceGroupName, clusterName), startTime, finishTime)
	}()

	cluster := BuildHCPClusterFromParams(parameters, tc.Location())

	if _, err := CreateHCPClusterAndWait(
		ctx,
		logger,
		tc.Get20240610ClientFactoryOrDie(ctx).NewHcpOpenShiftClustersClient(),
		resourceGroupName,
		clusterName,
		cluster,
		timeout,
	); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed to create HCP cluster %s, caused by: %w, error: %w", clusterName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed to create HCP cluster %s: %w", clusterName, err)
	}
	return nil
}

func (tc *perItOrDescribeTestContext) CreateNodePoolFromParam(
	ctx context.Context,
	resourceGroupName string,
	hcpClusterName string,
	parameters NodePoolParams,
	timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, fmt.Errorf("timeout '%f' minutes exceeded during CreateNodePoolFromParam for node pool %s in resource group %s", timeout.Minutes(), parameters.NodePoolName, resourceGroupName))
	defer cancel()

	startTime := time.Now()
	defer func() {
		finishTime := time.Now()
		tc.RecordTestStep(fmt.Sprintf("Deploy node pool %s", parameters.NodePoolName), startTime, finishTime)
	}()

	nodePoolName := parameters.NodePoolName
	if nodePoolName == "" {
		return fmt.Errorf("nodePoolName parameter not found or empty")
	}

	nodePool := BuildNodePoolFromParams(parameters, tc.Location())

	if _, err := CreateNodePoolAndWait(
		ctx,
		tc.Get20240610ClientFactoryOrDie(ctx).NewNodePoolsClient(),
		resourceGroupName,
		hcpClusterName,
		nodePoolName,
		nodePool,
		timeout,
	); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("failed to create NodePool %s, caused by: %w, error: %w", nodePoolName, context.Cause(ctx), err)
		}
		return fmt.Errorf("failed to create NodePool %s: %w", nodePoolName, err)
	}

	return nil
}
