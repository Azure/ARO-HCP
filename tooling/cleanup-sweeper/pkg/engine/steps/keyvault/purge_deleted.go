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

package keyvault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	armhelpers "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/arm"
)

const DeletedVaultsResourceType = "Microsoft.KeyVault/deletedVaults"

type PurgeDeletedStep struct {
	runner.DeletionStep
}

type PurgeDeletedStepConfig struct {
	ResourceGroupName string
	VaultsClient      *armkeyvault.VaultsClient

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

var _ runner.StepOptionsProvider = PurgeDeletedStepConfig{}

func (c PurgeDeletedStepConfig) StepOptions() runner.StepOptions {
	return runner.StepOptions{
		Name:            c.Name,
		Retries:         c.Retries,
		ContinueOnError: c.ContinueOnError,
		Verify:          c.Verify,
	}
}

func NewPurgeDeletedStep(cfg PurgeDeletedStepConfig) *PurgeDeletedStep {
	stepOptions := cfg.StepOptions()
	if stepOptions.Name == "" {
		stepOptions.Name = "Purge soft-deleted Key Vaults"
	}

	targetLocations := map[string]string{}

	step := &PurgeDeletedStep{
		DeletionStep: runner.DeletionStep{
			ResourceType: DeletedVaultsResourceType,
			Options:      stepOptions,
		},
	}

	step.DiscoverFn = func(ctx context.Context, _ string) ([]runner.Target, error) {
		pager := cfg.VaultsClient.NewListDeletedPager(nil)
		targets := []runner.Target{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list deleted vaults: %w", err)
			}
			for _, vault := range page.Value {
				if vault == nil || vault.Name == nil || vault.Properties == nil || vault.Properties.Location == nil || vault.Properties.VaultID == nil {
					continue
				}
				vaultID := *vault.Properties.VaultID
				if !strings.Contains(vaultID, fmt.Sprintf("/resourceGroups/%s/", cfg.ResourceGroupName)) {
					continue
				}
				targets = append(targets, runner.Target{
					ID:   vaultID,
					Name: *vault.Name,
					Type: DeletedVaultsResourceType,
				})
				targetLocations[*vault.Name] = *vault.Properties.Location
			}
		}
		return targets, nil
	}

	step.DeleteFn = func(ctx context.Context, target runner.Target, wait bool) error {
		location, ok := targetLocations[target.Name]
		if !ok {
			return fmt.Errorf("missing purge metadata for vault %s", target.Name)
		}
		poller, err := cfg.VaultsClient.BeginPurgeDeleted(ctx, target.Name, location, nil)
		if err != nil {
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				return nil
			}
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
