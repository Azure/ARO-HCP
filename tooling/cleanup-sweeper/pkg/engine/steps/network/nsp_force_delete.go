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

package network

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	armhelpers "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/arm"
)

const NSPResourceType = "Microsoft.Network/networkSecurityPerimeters"

type NSPForceDeleteStep struct {
	runner.DeletionStep
}

type NSPForceDeleteStepConfig struct {
	ResourceGroupName string
	ResourcesClient   *armresources.Client
	LocksClient       *armlocks.ManagementLocksClient
	NSPClient         *armnetwork.SecurityPerimetersClient

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

var _ runner.StepOptionsProvider = NSPForceDeleteStepConfig{}

func (c NSPForceDeleteStepConfig) StepOptions() runner.StepOptions {
	return runner.StepOptions{
		Name:            c.Name,
		Retries:         c.Retries,
		ContinueOnError: c.ContinueOnError,
		Verify:          c.Verify,
	}
}

func NewNSPForceDeleteStep(cfg NSPForceDeleteStepConfig) *NSPForceDeleteStep {
	stepOptions := cfg.StepOptions()
	if stepOptions.Name == "" {
		stepOptions.Name = "Delete network security perimeters"
	}

	step := &NSPForceDeleteStep{
		DeletionStep: runner.DeletionStep{
			ResourceType: NSPResourceType,
			Options:      stepOptions,
		},
	}
	step.DiscoverFn = func(ctx context.Context, resourceType string) ([]runner.Target, error) {
		resources, err := armhelpers.ListByType(ctx, cfg.ResourcesClient, cfg.ResourceGroupName, resourceType)
		if err != nil {
			return nil, err
		}
		targets := make([]runner.Target, 0, len(resources))
		for _, resource := range resources {
			if resource == nil || resource.ID == nil || resource.Name == nil || resource.Type == nil {
				continue
			}
			targets = append(targets, runner.Target{
				ID:   *resource.ID,
				Name: *resource.Name,
				Type: *resource.Type,
			})
		}
		return targets, nil
	}
	step.SkipFn = func(ctx context.Context, target runner.Target) (skip bool, reason string, err error) {
		if armhelpers.HasLocks(ctx, cfg.LocksClient, target.ID) {
			return true, "locked", nil
		}
		return false, "", nil
	}
	step.DeleteFn = func(ctx context.Context, target runner.Target, wait bool) error {
		poller, err := cfg.NSPClient.BeginDelete(ctx, cfg.ResourceGroupName, target.Name, &armnetwork.SecurityPerimetersClientBeginDeleteOptions{
			ForceDeletion: to.Ptr(true),
		})
		if err != nil {
			return err
		}
		if wait {
			return armhelpers.PollUntilDone(ctx, poller)
		}
		return nil
	}
	step.VerifyFn = func(ctx context.Context) error {
		if stepOptions.Verify == nil {
			return nil
		}
		return stepOptions.Verify(ctx)
	}

	return step
}
