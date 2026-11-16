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

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// waitForDeployment waits for a previous invocation to stop writing before we
// observe resources. Failed/canceled deployments may be retried with a fresh
// template; a successful old deployment is not proof that today's inputs match.
func waitForDeployment(ctx context.Context, deploymentsClient *armdeployments.DeploymentsClient, deploymentOperationsClient *armdeployments.DeploymentOperationsClient, resourceGroup, name string) error {
	logger := logr.FromContextOrDiscard(ctx)
	return utils.TrackError(wait.PollUntilContextCancel(ctx, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		result, err := deploymentsClient.Get(ctx, resourceGroup, name, nil)
		if err != nil {
			var responseErr *azcore.ResponseError
			if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
				return true, nil
			}
			return false, utils.TrackError(fmt.Errorf("getting deployment %s: %w", name, err))
		}
		if result.Properties == nil || ptr.Deref(result.Properties.ProvisioningState, "") == "" {
			return false, utils.TrackError(fmt.Errorf("deployment %s has no provisioning state", name))
		}
		switch *result.Properties.ProvisioningState {
		case armdeployments.ProvisioningStateFailed, armdeployments.ProvisioningStateCanceled:
			logger.Info("recovering unsuccessful deployment", "deployment", name, "provisioningState", *result.Properties.ProvisioningState, "deploymentError", result.Properties.Error)
			logDeploymentOperations(ctx, deploymentOperationsClient, resourceGroup, name)
			return true, nil
		case armdeployments.ProvisioningStateSucceeded:
			return true, nil
		default:
			logger.Info("waiting for existing deployment", "deployment", name, "provisioningState", *result.Properties.ProvisioningState)
			return false, nil
		}
	}))
}

// logDeploymentOperations makes one paginated pass, never a polling loop. Keep
// diagnostics bounded and best-effort, including when the deployment timed out.
func logDeploymentOperations(ctx context.Context, client *armdeployments.DeploymentOperationsClient, resourceGroup, name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	logger := logr.FromContextOrDiscard(ctx).WithValues("deployment", name)
	pager := client.NewListPager(resourceGroup, name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			logger.Error(utils.TrackError(err), "collecting deployment operations")
			return
		}
		for _, operation := range page.Value {
			if operation == nil || operation.Properties == nil {
				continue
			}
			properties := operation.Properties
			// Explicit fields exclude request/response bodies, which can contain secrets.
			logger.Info("deployment operation",
				"operationID", operation.OperationID,
				"resource", properties.TargetResource,
				"provisioningOperation", properties.ProvisioningOperation,
				"provisioningState", properties.ProvisioningState,
				"timestamp", properties.Timestamp,
				"duration", properties.Duration,
				"statusCode", properties.StatusCode,
				"statusMessage", properties.StatusMessage,
			)
		}
	}
}

// waitForManagedCluster returns the latest terminal snapshot without submitting writes.
func waitForManagedCluster(ctx context.Context, client *armcontainerservice.ManagedClustersClient, resourceGroup, clusterName string, existing *armcontainerservice.ManagedCluster) (*armcontainerservice.ManagedCluster, error) {
	logger := logr.FromContextOrDiscard(ctx)
	started := time.Now()

	// Respect cancellation even when the initial observation is already terminal.
	if err := ctx.Err(); err != nil {
		return nil, utils.TrackError(err)
	}

	// Reuse the caller's initial GET when the cluster is already terminal.
	if done, err := managedClusterProvisioningDone(existing); done || err != nil {
		return existing, utils.TrackError(err)
	}

	logger.Info("waiting for cluster provisioning", "provisioningState", *existing.Properties.ProvisioningState)

	// Poll active provisioning until it finishes or the caller's deadline expires.
	err := wait.PollUntilContextCancel(ctx, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		response, err := client.Get(ctx, resourceGroup, clusterName, nil)
		if err != nil {
			return false, utils.TrackError(fmt.Errorf("refreshing cluster during provisioning: %w", err))
		}
		existing = &response.ManagedCluster
		done, err := managedClusterProvisioningDone(existing)
		if err == nil && !done {
			logger.Info("waiting for cluster provisioning", "provisioningState", *existing.Properties.ProvisioningState, "elapsed", time.Since(started).String())
		}
		return done, utils.TrackError(err)
	})
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return existing, nil
}

// managedClusterProvisioningDone validates the observation and identifies terminal states.
func managedClusterProvisioningDone(cluster *armcontainerservice.ManagedCluster) (bool, error) {
	if cluster.Properties == nil || len(ptr.Deref(cluster.Properties.ProvisioningState, "")) == 0 {
		return false, utils.TrackError(fmt.Errorf("cluster has no provisioning state"))
	}
	switch *cluster.Properties.ProvisioningState {
	case provisioningStateSucceeded, provisioningStateFailed, provisioningStateCanceled:
		return true, nil
	default:
		return false, nil
	}
}
