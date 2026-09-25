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

package identitypool

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

const (
	deploymentStackNamePrefix = "aro-hcp-slot-msi-pool"
)

type ApplyOptions struct {
	Template        map[string]interface{}
	IdentityPools   []identityPool
	AzureCredential azcore.TokenCredential
}

func (o *ApplyOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	for _, pool := range o.IdentityPools {
		logger.Info(
			"applying identity pool",
			"environment", pool.Environment,
			"subscriptionName", pool.SubscriptionName,
			"subscriptionID", pool.SubscriptionID,
			"defaultRegion", pool.Region,
			"provisioningRegion", pool.ProvisioningRegion,
			"slotCount", len(pool.Slots),
		)

		stackClient, err := armdeploymentstacks.NewClient(pool.SubscriptionID, o.AzureCredential, nil)
		if err != nil {
			return fmt.Errorf("failed creating deployment stack client for %q: %w", pool.SubscriptionName, err)
		}

		for _, slot := range pool.Slots {
			if err := o.applySlot(ctx, logger, stackClient, pool, slot); err != nil {
				return err
			}
		}
	}

	return nil
}

func (o *ApplyOptions) applySlot(ctx context.Context, logger logr.Logger, stackClient *armdeploymentstacks.Client, pool identityPool, slot slots.ExpandedSlot) error {
	parameters := map[string]*armdeploymentstacks.DeploymentParameter{
		"poolSize": {
			Value: slot.IdentityContainerCount,
		},
		"resourceGroupBaseName": {
			Value: slot.IdentityContainerPrefix,
		},
	}

	stackName := fmt.Sprintf("%s-%s", deploymentStackNamePrefix, slot.ResourceName)
	stack := armdeploymentstacks.DeploymentStack{
		Location: to.Ptr(pool.ProvisioningRegion),
		Properties: &armdeploymentstacks.DeploymentStackProperties{
			Template:   o.Template,
			Parameters: parameters,
			DenySettings: &armdeploymentstacks.DenySettings{
				Mode:               to.Ptr(armdeploymentstacks.DenySettingsModeNone),
				ApplyToChildScopes: to.Ptr(false),
				ExcludedActions:    []*string{},
				ExcludedPrincipals: []*string{},
			},
			ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
				Resources:        to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnum("delete")),
				ResourceGroups:   to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnum("delete")),
				ManagementGroups: to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnum("delete")),
			},
		},
	}

	logger.Info(
		"applying deployment stack",
		"subscriptionID", pool.SubscriptionID,
		"subscriptionName", pool.SubscriptionName,
		"stackName", stackName,
		"provisioningRegion", pool.ProvisioningRegion,
		"slotName", slot.ResourceName,
		"identityContainerCount", slot.IdentityContainerCount,
	)

	poller, err := stackClient.BeginCreateOrUpdateAtSubscription(
		ctx,
		stackName,
		stack,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed starting deployment stack apply for slot %q: %w", slot.ResourceName, err)
	}

	_, err = poller.PollUntilDone(ctx, &azruntime.PollUntilDoneOptions{Frequency: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("failed waiting for deployment stack apply for slot %q: %w", slot.ResourceName, err)
	}

	return nil
}
