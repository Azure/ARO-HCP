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

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/common"
)

// DeletedVaultsResourceType is the ARM resource type for deleted Key Vaults.
const DeletedVaultsResourceType = "Microsoft.KeyVault/deletedVaults"

// PurgeDeletedStepConfig configures deleted Key Vault purge behavior.
type PurgeDeletedStepConfig struct {
	ResourceGroupName string
	VaultsClient      *armkeyvault.VaultsClient

	Name            string
	Retries         int
	ContinueOnError bool
	Verify          runner.VerifyFn
}

type purgeDeletedStep struct {
	cfg             PurgeDeletedStepConfig
	name            string
	retries         int
	continueOnError bool
	verify          runner.VerifyFn
}

var _ runner.Step = (*purgeDeletedStep)(nil)

// NewPurgeDeletedStep builds the deleted Key Vault purge step.
func NewPurgeDeletedStep(cfg PurgeDeletedStepConfig) (runner.Step, error) {
	if strings.TrimSpace(cfg.ResourceGroupName) == "" {
		return nil, fmt.Errorf("resource group name is required")
	}
	if cfg.VaultsClient == nil {
		return nil, fmt.Errorf("vaults client is required")
	}

	stepName := cfg.Name
	if strings.TrimSpace(stepName) == "" {
		stepName = "Purge soft-deleted Key Vaults"
	}

	return &purgeDeletedStep{
		cfg:             cfg,
		name:            stepName,
		retries:         cfg.Retries,
		continueOnError: cfg.ContinueOnError,
		verify:          cfg.Verify,
	}, nil
}

// MustNewPurgeDeletedStep builds the step and panics on invalid config.
func MustNewPurgeDeletedStep(cfg PurgeDeletedStepConfig) runner.Step {
	step, err := NewPurgeDeletedStep(cfg)
	if err != nil {
		panic(err)
	}
	return step
}

func (s *purgeDeletedStep) Name() string {
	return s.name
}

func (s *purgeDeletedStep) RetryLimit() int {
	if s.retries < runner.DefaultRetries {
		return runner.DefaultRetries
	}
	return s.retries
}

func (s *purgeDeletedStep) ContinueOnError() bool {
	return s.continueOnError
}

func (s *purgeDeletedStep) Verify(ctx context.Context) error {
	if s.verify == nil {
		return nil
	}
	return s.verify(ctx)
}

func (s *purgeDeletedStep) Discover(ctx context.Context) ([]runner.Target, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}
	skipReporter := common.NewDiscoverySkipReporter(s.Name())
	defer skipReporter.Flush(logger)

	pager := s.cfg.VaultsClient.NewListDeletedPager(nil)
	targets := []runner.Target{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list deleted vaults: %w", err)
		}
		for i, vault := range page.Value {
			if vault == nil || vault.Name == nil || vault.Properties == nil || vault.Properties.Location == nil || vault.Properties.VaultID == nil {
				skipReporter.Record(
					logger,
					"invalid_deleted_vault_payload",
					"index", i,
				)
				continue
			}
			vaultID := *vault.Properties.VaultID
			// ARM resource group names are case-insensitive, so match casing-insensitively
			// to avoid skipping vaults whose ID segment casing differs from the configured name.
			rgSegment := fmt.Sprintf("/resourceGroups/%s/", s.cfg.ResourceGroupName)
			if !strings.Contains(strings.ToLower(vaultID), strings.ToLower(rgSegment)) {
				continue
			}
			targets = append(targets, runner.Target{
				ID:       vaultID,
				Name:     *vault.Name,
				Type:     DeletedVaultsResourceType,
				Location: *vault.Properties.Location,
			})
		}
	}
	return targets, nil
}

func (s *purgeDeletedStep) Delete(ctx context.Context, target runner.Target, wait bool) error {
	if target.Location == "" {
		return fmt.Errorf("missing location for vault %s", target.Name)
	}
	return purgeDeletedVault(ctx, s.cfg.VaultsClient, target.Name, target.Location, wait)
}

// purgeDeletedVault purges a single soft-deleted Key Vault. A 404 is treated as
// success because it means the vault is already gone (concurrently purged or
// expired out of soft-delete retention).
func purgeDeletedVault(ctx context.Context, client *armkeyvault.VaultsClient, name, location string, wait bool) error {
	poller, err := client.BeginPurgeDeleted(ctx, name, location, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return nil
		}
		return err
	}
	if wait {
		_, err = poller.PollUntilDone(ctx, nil)
		return err
	}
	return nil
}
