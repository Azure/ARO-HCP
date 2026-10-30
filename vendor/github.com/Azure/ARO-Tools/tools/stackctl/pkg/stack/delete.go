// Copyright 2026 Microsoft Corporation
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

// Package stack removes Azure deployment stacks.
package stack

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
)

// ActionOnUnmanage is what happens to the resources a removed stack manages.
type ActionOnUnmanage string

const (
	// ActionDetach leaves every managed resource in place.
	ActionDetach ActionOnUnmanage = "detach"
	// ActionDelete deletes every managed resource.
	ActionDelete ActionOnUnmanage = "delete"
)

// ParseActionOnUnmanage accepts exactly "detach" or "delete". There is no default, so callers must choose.
func ParseActionOnUnmanage(s string) (ActionOnUnmanage, error) {
	switch a := ActionOnUnmanage(s); a {
	case ActionDetach, ActionDelete:
		return a, nil
	default:
		return "", fmt.Errorf("invalid action on unmanage %q: must be %q or %q", s, ActionDetach, ActionDelete)
	}
}

// Result describes what DeleteAtResourceGroup did.
type Result string

const (
	// ResultDeleted means the stack existed and was removed; its resources were detached or deleted as requested.
	ResultDeleted Result = "Deleted"
	// ResultNotFound means the stack or its resource group did not exist, so nothing changed.
	ResultNotFound Result = "NotFound"
)

// deleteOptions applies the same action to resources, resource groups and management groups, matching how ARMStack
// create and update apply actionOnUnmanage.
func deleteOptions(action ActionOnUnmanage) (*armdeploymentstacks.ClientBeginDeleteAtResourceGroupOptions, error) {
	switch action {
	case ActionDetach:
		return &armdeploymentstacks.ClientBeginDeleteAtResourceGroupOptions{
			UnmanageActionResources:        to.Ptr(armdeploymentstacks.UnmanageActionResourceModeDetach),
			UnmanageActionResourceGroups:   to.Ptr(armdeploymentstacks.UnmanageActionResourceGroupModeDetach),
			UnmanageActionManagementGroups: to.Ptr(armdeploymentstacks.UnmanageActionManagementGroupModeDetach),
		}, nil
	case ActionDelete:
		return &armdeploymentstacks.ClientBeginDeleteAtResourceGroupOptions{
			UnmanageActionResources:        to.Ptr(armdeploymentstacks.UnmanageActionResourceModeDelete),
			UnmanageActionResourceGroups:   to.Ptr(armdeploymentstacks.UnmanageActionResourceGroupModeDelete),
			UnmanageActionManagementGroups: to.Ptr(armdeploymentstacks.UnmanageActionManagementGroupModeDelete),
		}, nil
	default:
		return nil, fmt.Errorf("invalid action on unmanage %q: must be %q or %q", action, ActionDetach, ActionDelete)
	}
}

// DeleteAtResourceGroup removes the deployment stack name from resourceGroup and applies action to every resource,
// resource group and management group it manages. A stack or resource group that does not exist is not an error.
func DeleteAtResourceGroup(ctx context.Context, client *armdeploymentstacks.Client, resourceGroup, name string, action ActionOnUnmanage) (Result, error) {
	logger := logr.FromContextOrDiscard(ctx).WithValues("resourceGroup", resourceGroup, "deploymentStack", name, "actionOnUnmanage", action)

	options, err := deleteOptions(action)
	if err != nil {
		return "", err
	}

	stack, err := client.GetAtResourceGroup(ctx, resourceGroup, name, nil)
	if code, ok := notFoundCode(err); ok {
		logger.Info("Deployment stack not found, nothing to delete.", "errorCode", code)
		return ResultNotFound, nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get deployment stack %s in resource group %s: %w", name, resourceGroup, err)
	}

	var managed int
	if stack.Properties != nil {
		managed = len(stack.Properties.Resources)
	}
	logger.Info("Deleting deployment stack.", "managedResources", managed)

	poller, err := client.BeginDeleteAtResourceGroup(ctx, resourceGroup, name, options)
	if code, ok := notFoundCode(err); ok {
		logger.Info("Deployment stack disappeared before it could be deleted, nothing to delete.", "errorCode", code)
		return ResultNotFound, nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to start deleting deployment stack %s in resource group %s: %w", name, resourceGroup, err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		return "", fmt.Errorf("failed to delete deployment stack %s in resource group %s: %w", name, resourceGroup, err)
	}

	logger.Info("Deployment stack deleted.", "managedResources", managed)
	return ResultDeleted, nil
}

// notFoundCode reports whether err is an ARM 404 saying the stack or its resource group does not exist, and returns
// the ARM error code so the log shows which one it was. Other 404s, such as SubscriptionNotFound, are not a missing
// stack and must fail the command.
func notFoundCode(err error) (string, bool) {
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
		return "", false
	}
	switch respErr.ErrorCode {
	case "DeploymentStackResourceNotFound", "ResourceGroupNotFound":
		return respErr.ErrorCode, true
	default:
		return "", false
	}
}
